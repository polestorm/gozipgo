package zipreader

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"sort"

	"github.com/polestorm/gozipgo/internal/format"
)

// span czyta małe kawałki pliku przez bufor: nagłówki lokalne czytamy w kolejności położenia, więc kolejne
// odczyty zwykle trafiają w to, co już jest w buforze.
type span struct {
	ra    io.ReaderAt
	limit int64 // odczyty nie wychodzą poza [0, limit)
	buf   []byte
	start int64
}

// get zwraca n bajtów od przesunięcia off; wycinek jest ważny do następnego wywołania. Wywołujący sprawdził, że
// off+n ≤ limit.
func (s *span) get(off int64, n int) ([]byte, error) {
	if off >= s.start && off+int64(n) <= s.start+int64(len(s.buf)) {
		return s.buf[off-s.start:][:n], nil
	}
	size := int(min(int64(max(n, 8192)), s.limit-off))
	if cap(s.buf) < size {
		s.buf = make([]byte, size)
	}
	s.buf = s.buf[:0]
	if err := readFull(s.ra, s.buf[:size], off); err != nil {
		return nil, err
	}
	s.buf, s.start = s.buf[:size], off
	return s.buf[:n], nil
}

// checkLayout czyta nagłówek lokalny i deskryptor danych każdego wpisu, porównuje je z katalogiem (ENT-001…003)
// i sprawdza układ pliku (ENT-004, ENT-005): obszary wpisów, w kolejności położenia, zaczynają się na początku
// pliku, przylegają do siebie i kończą tam, gdzie zaczyna się katalog. Obszar wpisu to nagłówek lokalny z nazwą
// i polami dodatkowymi, dane o rozmiarze z katalogu i, z bitem 3, deskryptor; jego koniec wyznacza początek
// następnego obszaru, więc postać deskryptora wynika z długości, a nie ze zgadywania po wartościach.
func (r *Reader) checkLayout(dir directory, opts Options) error {
	dirStart := dir.offset
	prefixed := dir.base > 0
	if len(r.entries) == 0 {
		if dirStart != 0 {
			if !opts.AllowPrefix {
				return archiveError(CodeUnreferencedData, 0, "data before the central directory of an archive with no entries")
			}
			prefixed = true
		}
		if prefixed {
			r.relaxed = append(r.relaxed, Relaxation{Option: "AllowPrefix", Entry: -1})
		}
		return nil
	}
	// Najpierw, w kolejności katalogu: przesunięcie musi zostawiać miejsce na nagłówek przed katalogiem. Bez tego
	// wpis wskazujący poza dane wyglądałby jak dane, na które nic nie wskazuje, po wpisie poprzednim.
	for i := range r.entries {
		if e := &r.entries[i]; e.LocalHeaderOffset > dirStart || dirStart-e.LocalHeaderOffset < format.LocalHeaderSize {
			return entryError(CodeLocalHeaderMismatch, i, e.Name, int64(min(e.LocalHeaderOffset, uint64(r.size))),
				"the local header offset does not leave room for a header before the central directory")
		}
	}
	order := make([]int, len(r.entries))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		x, y := &r.entries[order[a]], &r.entries[order[b]]
		if x.LocalHeaderOffset != y.LocalHeaderOffset {
			return x.LocalHeaderOffset < y.LocalHeaderOffset
		}
		return x.Index < y.Index
	})
	if first := r.entries[order[0]].LocalHeaderOffset; first != 0 {
		if !opts.AllowPrefix {
			return entryError(CodeUnreferencedData, order[0], r.entries[order[0]].Name, 0, "data before the first entry")
		}
		prefixed = true
	}
	s := &span{ra: r.ra, limit: int64(dirStart)}
	var unusedZip64 []int // wpisy, do których zastosowano AllowUnusedZip64
	for k, i := range order {
		e := &r.entries[i]
		off := e.LocalHeaderOffset
		fail := func(code, message string) error { return entryError(code, i, e.Name, int64(off), message) }
		// Obszar wpisu kończy się tam, gdzie zaczyna się następny (w kolejności położenia) albo katalog.
		next, other := dirStart, -1
		if k+1 < len(order) {
			other = order[k+1]
			next = r.entries[other].LocalHeaderOffset
		}
		overlap := func() error {
			if other < 0 {
				return fail(CodeEntriesOverlap, "the entry runs into the central directory")
			}
			err := entryError(CodeEntriesOverlap, max(i, other), r.entries[max(i, other)].Name, int64(next),
				"two entries share bytes of the file")
			err.Other = min(i, other)
			return err
		}
		b, err := s.get(int64(off), format.LocalHeaderSize)
		if err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(b) != format.SigLocalHeader {
			return fail(CodeLocalHeaderMismatch, "no local file header at the offset the directory gives")
		}
		flags := binary.LittleEndian.Uint16(b[6:])
		method := binary.LittleEndian.Uint16(b[8:])
		crc := binary.LittleEndian.Uint32(b[14:])
		csize := uint64(binary.LittleEndian.Uint32(b[18:]))
		usize := uint64(binary.LittleEndian.Uint32(b[22:]))
		nameLen := int(binary.LittleEndian.Uint16(b[26:]))
		extraLen := int(binary.LittleEndian.Uint16(b[28:]))
		switch {
		case flags != e.Flags:
			return fail(CodeLocalHeaderMismatch, "the flags of the local header differ from the directory")
		case method != e.Method:
			return fail(CodeLocalHeaderMismatch, "the compression method of the local header differs from the directory")
		}
		raw := e.Name
		if e.rawName != "" {
			raw = e.rawName
		}
		headerLen := uint64(format.LocalHeaderSize + nameLen + extraLen)
		if nameLen != len(raw) {
			return fail(CodeLocalHeaderMismatch, "the name of the local header differs from the directory")
		}
		if dirStart-off < headerLen {
			return fail(CodeLocalHeaderMismatch, "the local header runs into the central directory")
		}
		if next-off < headerLen {
			return overlap()
		}
		if b, err = s.get(int64(off)+format.LocalHeaderSize, nameLen+extraLen); err != nil {
			return err
		}
		if string(b[:nameLen]) != raw {
			return fail(CodeLocalHeaderMismatch, "the name of the local header differs from the directory")
		}
		x, xerr := parseExtra(b[nameLen:])
		if xerr != "" {
			return fail(CodeExtraField, xerr)
		}
		// ZIP64 w nagłówku lokalnym (APPNOTE 4.5.3): pole jest wymagane, gdy któreś pole rozmiaru ma wartość
		// graniczną, i zawsze zawiera oba rozmiary. Pole rozmiaru bez wartości granicznej musi się równać
		// wartości z pola ZIP64 (tak zapisuje je zipfile Pythona), inaczej nagłówek miałby dwa odczytania.
		sentinel := csize == format.Sentinel32 || usize == format.Sentinel32
		switch {
		case x.zip64Count > 1:
			return fail(CodeZip64Inconsistent, "more than one ZIP64 extra field in the local header")
		case x.zip64Count == 0 && sentinel:
			return fail(CodeZip64Inconsistent, "a size of the local header refers to the ZIP64 extra field, but there is none")
		case x.zip64Count == 1:
			if len(x.zip64) != 16 {
				return fail(CodeZip64Inconsistent, "the ZIP64 extra field of the local header does not hold exactly the two sizes")
			}
			zusize, zcsize := binary.LittleEndian.Uint64(x.zip64), binary.LittleEndian.Uint64(x.zip64[8:])
			switch {
			case (usize == format.Sentinel32 || usize == zusize) && (csize == format.Sentinel32 || csize == zcsize):
				usize, csize = zusize, zcsize
			case opts.AllowUnusedZip64 && !sentinel && zusize == 0 && zcsize == 0:
				// Pole zarezerwowane i niewypełnione: obowiązują rozmiary z nagłówka (APPNOTE 4.5.3 każe czytać
				// pole tylko przy wartości granicznej). Poluzowanie jest raportowane.
				unusedZip64 = append(unusedZip64, i)
			default:
				return fail(CodeZip64Inconsistent, "the ZIP64 extra field of the local header differs from its size fields")
			}
		}
		descriptor := flags&format.FlagDataDescriptor != 0
		// Bez bitu 3 pola równają się katalogowi; z bitem 3 są zerowe albo równe katalogowi (APPNOTE 4.4.4).
		agrees := func(local, central uint64) bool { return local == central || descriptor && local == 0 }
		if !agrees(uint64(crc), uint64(e.CRC32)) || !agrees(csize, e.CompressedSize) || !agrees(usize, e.UncompressedSize) {
			return fail(CodeLocalHeaderMismatch, "the CRC-32 or a size of the local header differs from the directory")
		}
		if x.unicodePathCount > 0 {
			p := x.unicodePath
			if x.unicodePathCount > 1 || len(p) < 5 || p[0] != 1 ||
				binary.LittleEndian.Uint32(p[1:]) != crc32.ChecksumIEEE([]byte(raw)) || string(p[5:]) != e.Name {
				return fail(CodeNameUnicodePath, "the Unicode Path extra field of the local header does not give the name of the entry")
			}
		}
		if (x.encryption || x.patch) && e.Unsupported != CodeUnsupportedEncryption && e.Unsupported != CodeUnsupportedPatch {
			return fail(CodeLocalHeaderMismatch, "the local header has an encryption or patch extra field the directory does not have")
		}
		if next-off-headerLen < e.CompressedSize {
			return overlap()
		}
		e.DataOffset = off + headerLen
		dataEnd := e.DataOffset + e.CompressedSize
		rest := next - dataEnd
		if descriptor {
			if rest != 12 && rest != 16 && rest != 20 && rest != 24 {
				return entryError(CodeDescriptorMismatch, i, e.Name, int64(dataEnd),
					"the bytes after the data are not a data descriptor in one of its four forms")
			}
			d, err := s.get(int64(dataEnd), int(rest))
			if err != nil {
				return err
			}
			if rest == 16 || rest == 24 {
				if binary.LittleEndian.Uint32(d) != format.SigDataDescriptor {
					return entryError(CodeDescriptorMismatch, i, e.Name, int64(dataEnd), "the data descriptor has no signature where its length requires one")
				}
				d = d[4:]
			}
			dcrc := binary.LittleEndian.Uint32(d)
			var dcsize, dusize uint64
			if len(d) == 12 {
				dcsize, dusize = uint64(binary.LittleEndian.Uint32(d[4:])), uint64(binary.LittleEndian.Uint32(d[8:]))
			} else {
				dcsize, dusize = binary.LittleEndian.Uint64(d[4:]), binary.LittleEndian.Uint64(d[12:])
			}
			if dcrc != e.CRC32 || dcsize != e.CompressedSize || dusize != e.UncompressedSize {
				return entryError(CodeDescriptorMismatch, i, e.Name, int64(dataEnd), "the data descriptor differs from the directory")
			}
		} else if rest != 0 {
			return entryError(CodeUnreferencedData, i, e.Name, int64(dataEnd), "data after the entry that belong to no entry")
		}
		if e.Method == format.MethodStored && e.Unsupported == "" && e.CompressedSize != e.UncompressedSize {
			return fail(CodeSizeMismatch, "a Stored entry whose compressed and uncompressed sizes differ")
		}
	}
	if prefixed {
		r.relaxed = append(r.relaxed, Relaxation{Option: "AllowPrefix", Entry: -1})
	}
	// Wpisy były sprawdzane w kolejności położenia; lista poluzowań idzie w kolejności katalogu.
	sort.Ints(unusedZip64)
	for _, i := range unusedZip64 {
		r.relaxed = append(r.relaxed, Relaxation{Option: "AllowUnusedZip64", Entry: i})
	}
	return nil
}
