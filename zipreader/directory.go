package zipreader

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"unicode/utf8"

	"github.com/polestorm/gozipgo/internal/format"
)

// windowSize to rozmiar bufora czytania katalogu: mieści część stałą nagłówka i trzy części zmienne o największej
// długości (3 × 65 535), więc jeden nagłówek zawsze jest w buforze w całości.
const windowSize = 256 << 10

// window czyta obszar pliku [pos, end) po kolei przez bufor stałego rozmiaru.
type window struct {
	ra     io.ReaderAt
	buf    []byte
	pos    int64 // przesunięcie w pliku pierwszego nieprzeczytanego bajtu
	end    int64
	lo, hi int // buf[lo:hi] to bajty od pos
}

// next zwraca następne n bajtów obszaru; wycinek jest ważny do kolejnego wywołania. Wywołujący sprawdza wcześniej,
// że n bajtów zostało.
func (w *window) next(n int) ([]byte, error) {
	if w.hi-w.lo < n {
		copy(w.buf, w.buf[w.lo:w.hi])
		w.hi -= w.lo
		w.lo = 0
		fill := int(min(int64(len(w.buf)-w.hi), w.end-w.pos-int64(w.hi)))
		if err := readFull(w.ra, w.buf[w.hi:w.hi+fill], w.pos+int64(w.hi)); err != nil {
			return nil, err
		}
		w.hi += fill
	}
	b := w.buf[w.lo : w.lo+n]
	w.lo += n
	w.pos += int64(n)
	return b, nil
}

// readDirectory czyta nagłówki katalogu centralnego po kolei (DIR-007) i buduje listę wpisów. Liczba nagłówków
// i ich łączny rozmiar muszą się równać wartościom z końca katalogu.
func (r *Reader) readDirectory(dir directory, opts Options) error {
	maxName := limitOf(int64(opts.MaxNameLength), DefaultMaxNameLength)
	maxComment := limitOf(int64(opts.MaxCommentLength), DefaultMaxCommentLength)
	maxExtra := limitOf(int64(opts.MaxExtraLength), DefaultMaxExtraLength)

	// Rozmiar katalogu jest już sprawdzony względem pliku i limitu; bufor nie jest większy niż katalog.
	w := &window{ra: r.ra, pos: int64(dir.offset), end: int64(dir.offset + dir.size),
		buf: make([]byte, min(dir.size, windowSize))}
	// Liczba wpisów jest już ograniczona limitem i rozmiarem katalogu, który leży w pliku (46 bajtów na wpis),
	// więc alokacja jest proporcjonalna do danych, które wywołujący naprawdę podał (SAFE-006).
	r.entries = make([]Entry, 0, dir.entries)
	for i := 0; uint64(i) < dir.entries; i++ {
		at := w.pos
		left := w.end - w.pos
		if left < 4 {
			return entryError(CodeDirectoryCountMismatch, i, "", at, "the central directory ends before the declared number of headers")
		}
		// Sygnaturę czytamy osobno, żeby rozpoznać rekordy, które nie są nagłówkami wpisów.
		b, err := w.next(4)
		if err != nil {
			return err
		}
		switch binary.LittleEndian.Uint32(b) {
		case format.SigCentralHeader:
		case format.SigDigitalSignature:
			return entryError(CodeUnsupportedDigitalSignature, i, "", at, "a digital signature record in the central directory")
		case format.SigArchiveExtraData:
			return entryError(CodeUnsupportedEncryption, i, "", at,
				"an archive extra data record: the central directory is encrypted or compressed")
		default:
			return entryError(CodeDirectorySignature, i, "", at, "not a central directory header")
		}
		if left < format.CentralHeaderSize {
			return entryError(CodeDirectoryTruncated, i, "", at, "a central directory header runs past the end of the directory")
		}
		if b, err = w.next(format.CentralHeaderSize - 4); err != nil {
			return err
		}
		h := b[: format.CentralHeaderSize-4 : format.CentralHeaderSize-4]
		e := Entry{
			Index:             i,
			DirectoryOffset:   at,
			VersionMadeBy:     binary.LittleEndian.Uint16(h[0:]),
			VersionNeeded:     binary.LittleEndian.Uint16(h[2:]),
			Flags:             binary.LittleEndian.Uint16(h[4:]),
			Method:            binary.LittleEndian.Uint16(h[6:]),
			Modified:          timestamp(binary.LittleEndian.Uint16(h[10:]), binary.LittleEndian.Uint16(h[8:])),
			CRC32:             binary.LittleEndian.Uint32(h[12:]),
			CompressedSize:    uint64(binary.LittleEndian.Uint32(h[16:])),
			UncompressedSize:  uint64(binary.LittleEndian.Uint32(h[20:])),
			InternalAttrs:     binary.LittleEndian.Uint16(h[32:]),
			ExternalAttrs:     binary.LittleEndian.Uint32(h[34:]),
			LocalHeaderOffset: uint64(binary.LittleEndian.Uint32(h[38:])),
		}
		nameLen := int(binary.LittleEndian.Uint16(h[24:]))
		extraLen := int(binary.LittleEndian.Uint16(h[26:]))
		commentLen := int(binary.LittleEndian.Uint16(h[28:]))
		diskStart := binary.LittleEndian.Uint16(h[30:])
		if int64(nameLen+extraLen+commentLen) > left-format.CentralHeaderSize {
			return entryError(CodeDirectoryTruncated, i, "", at, "a central directory header runs past the end of the directory")
		}
		if e.Flags&format.FlagMaskedHeaders != 0 {
			return entryError(CodeUnsupportedEncryption, i, "", at, "bit 13 of the flags: the central directory is encrypted")
		}
		if e.Flags&format.FlagsReserved != 0 {
			return entryError(CodeUnsupportedFlag, i, "", at, "a reserved or unused bit of the general purpose flag is set")
		}
		if maxName >= 0 && int64(nameLen) > maxName {
			return entryError(CodeLimitNameLength, i, "", at, "the name of the entry is longer than the limit")
		}
		if maxExtra >= 0 && int64(extraLen) > maxExtra {
			return entryError(CodeLimitExtraLength, i, "", at, "the extra field of the entry is longer than the limit")
		}
		if maxComment >= 0 && int64(commentLen) > maxComment {
			return entryError(CodeLimitCommentLength, i, "", at, "the comment of the entry is longer than the limit")
		}
		if b, err = w.next(nameLen + extraLen + commentLen); err != nil {
			return err
		}
		rawName, extra, comment := b[:nameLen], b[nameLen:nameLen+extraLen], b[nameLen+extraLen:]

		name, relaxedName, code, msg := checkName(rawName, e.Flags&format.FlagUTF8 != 0, opts.UnflaggedNames, opts.CodePage)
		if code != "" {
			return entryError(code, i, string(rawName), at, msg)
		}
		e.Name = name
		e.IsDir = name[len(name)-1] == '/'

		x, xerr := parseExtra(extra)
		if xerr != "" {
			return entryError(CodeExtraField, i, name, at, xerr)
		}
		// ZIP64 (DIR-008): dokładnie te pola, których odpowiedniki w nagłówku mają wartość graniczną, w kolejności
		// z APPNOTE 4.5.3: rozmiar po rozpakowaniu, rozmiar skompresowany, przesunięcie, numer dysku. Dłuższe pole
		// jest przyjmowane tylko w postaci pełnej, która daje te same wartości (zip64FullForm).
		disk := uint32(diskStart)
		want := 0
		for _, f := range [...]struct {
			sentinel bool
			size     int
		}{{e.UncompressedSize == format.Sentinel32, 8}, {e.CompressedSize == format.Sentinel32, 8},
			{e.LocalHeaderOffset == format.Sentinel32, 8}, {diskStart == format.Sentinel16, 4}} {
			if f.sentinel {
				want += f.size
			}
		}
		switch {
		case x.zip64Count > 1:
			return entryError(CodeZip64Inconsistent, i, name, at, "more than one ZIP64 extra field")
		case x.zip64Count == 0 && want != 0:
			return entryError(CodeZip64Inconsistent, i, name, at, "a field refers to the ZIP64 extra field, but there is none")
		case x.zip64Count == 1 && want == 0:
			return entryError(CodeZip64Inconsistent, i, name, at, "a ZIP64 extra field, but no field of the header refers to it")
		case len(x.zip64) != want && !zip64FullForm(x.zip64, want,
			[4]bool{e.UncompressedSize == format.Sentinel32, e.CompressedSize == format.Sentinel32,
				e.LocalHeaderOffset == format.Sentinel32, diskStart == format.Sentinel16},
			[4]uint64{e.UncompressedSize, e.CompressedSize, e.LocalHeaderOffset, uint64(diskStart)}):
			return entryError(CodeZip64Inconsistent, i, name, at,
				"the ZIP64 extra field does not hold exactly the fields the header refers to")
		}
		z := x.zip64
		if e.UncompressedSize == format.Sentinel32 {
			e.UncompressedSize, z = binary.LittleEndian.Uint64(z), z[8:]
		}
		if e.CompressedSize == format.Sentinel32 {
			e.CompressedSize, z = binary.LittleEndian.Uint64(z), z[8:]
		}
		if e.LocalHeaderOffset == format.Sentinel32 {
			e.LocalHeaderOffset, z = binary.LittleEndian.Uint64(z), z[8:]
		}
		if diskStart == format.Sentinel16 {
			disk = binary.LittleEndian.Uint32(z)
		}
		if disk != 0 {
			return entryError(CodeUnsupportedMultiVolume, i, name, at, "the entry starts on another volume")
		}
		if e.LocalHeaderOffset > ^uint64(0)-dir.base {
			e.LocalHeaderOffset = ^uint64(0) // poza plikiem: odrzuci to sprawdzenie nagłówków lokalnych
		} else {
			e.LocalHeaderOffset += dir.base
		}
		if name != string(rawName) {
			e.rawName = string(rawName)
		}
		// Unicode Path (NAME-008): jeśli jest, musi dawać tę samą nazwę co pole nazwy.
		if x.unicodePathCount > 0 {
			p := x.unicodePath
			if x.unicodePathCount > 1 || len(p) < 5 || p[0] != 1 ||
				binary.LittleEndian.Uint32(p[1:]) != crc32.ChecksumIEEE(rawName) || string(p[5:]) != name {
				return entryError(CodeNameUnicodePath, i, name, at,
					"the Unicode Path extra field does not give the name of the entry")
			}
		}
		if e.Flags&format.FlagUTF8 != 0 && !utf8.Valid(comment) {
			return entryError(CodeCommentEncoding, i, name, at, "the comment of an entry with the UTF-8 flag is not UTF-8")
		}
		if e.IsDir && (e.UncompressedSize != 0 || e.CompressedSize != 0) {
			return entryError(CodeDirectoryEntrySize, i, name, at, "a directory entry with a non-zero size")
		}
		switch {
		case e.Flags&(format.FlagEncrypted|format.FlagStrongEncryption) != 0 || x.encryption:
			e.Unsupported = CodeUnsupportedEncryption
		case e.Flags&format.FlagPatchData != 0 || x.patch:
			e.Unsupported = CodeUnsupportedPatch
		case e.Method != format.MethodStored && e.Method != format.MethodDeflate:
			e.Unsupported = CodeUnsupportedMethod
		case e.VersionNeeded > format.VersionZip64:
			// Starszy bajt pola nie ma znaczenia w specyfikacji (APPNOTE 4.4.3); część programów zapisuje tam
			// numer systemu. Z opcją liczy się tylko młodszy bajt, a jej zadziałanie jest raportowane.
			if opts.AllowVersionHostByte && e.VersionNeeded&0xFF <= format.VersionZip64 {
				r.relaxed = append(r.relaxed, Relaxation{Option: "AllowVersionHostByte", Entry: i})
			} else {
				e.Unsupported = CodeUnsupportedVersion
			}
		}
		e.Extra = append([]byte(nil), extra...)
		e.Comment = string(comment)
		if relaxedName {
			r.relaxed = append(r.relaxed, Relaxation{Option: "UnflaggedNames", Entry: i})
		}
		r.entries = append(r.entries, e)
	}
	if w.pos != w.end {
		// Po ostatnim nagłówku APPNOTE 4.3.12 dopuszcza rekord podpisu cyfrowego: rozpoznajemy go, żeby dać kod
		// mówiący, co to jest, a nie tylko że rozmiar się nie zgadza.
		if at := w.pos; w.end-at >= 4 {
			b, err := w.next(4)
			if err != nil {
				return err
			}
			if binary.LittleEndian.Uint32(b) == format.SigDigitalSignature {
				return archiveError(CodeUnsupportedDigitalSignature, at, "a digital signature record in the central directory")
			}
			w.pos = at
		}
		return archiveError(CodeDirectoryCountMismatch, w.pos, "the central directory is larger than the declared number of headers")
	}
	return nil
}

// zip64FullForm mówi, czy pole ZIP64 dłuższe niż wymagane jest „postacią pełną”, którą zapisują niektóre programy:
// pola po kolei od pierwszego (rozmiar po rozpakowaniu, skompresowany, przesunięcie, numer dysku), także te, do
// których nagłówek nie odsyła. Przyjmujemy ją tylko wtedy, gdy oba sposoby czytania dają to samo: czytanie
// „po kolei tylko pól granicznych” (APPNOTE 4.5.3, z pierwszych want bajtów) i czytanie pozycyjne zgadzają się
// w polach granicznych, a pola pozycyjne, do których nagłówek nie odsyła, równają się wartościom z nagłówka.
func zip64FullForm(z []byte, want int, sentinel [4]bool, header [4]uint64) bool {
	sizes := [4]int{8, 8, 8, 4}
	fields, n := 0, 0
	for fields < 4 && n < len(z) {
		n += sizes[fields]
		fields++
	}
	if n != len(z) || len(z) < want {
		return false
	}
	read := func(b []byte, size int) uint64 {
		if size == 4 {
			return uint64(binary.LittleEndian.Uint32(b))
		}
		return binary.LittleEndian.Uint64(b)
	}
	compact, positional := z[:want], z
	for i, size := range sizes {
		var p uint64
		if i < fields {
			p, positional = read(positional, size), positional[size:]
		}
		switch {
		case sentinel[i]:
			if i >= fields || read(compact, size) != p {
				return false
			}
			compact = compact[size:]
		case i < fields && p != header[i]:
			return false
		}
	}
	return true
}

// extraInfo to wynik przejścia po polach dodatkowych nagłówka.
type extraInfo struct {
	zip64            []byte // dane pola ZIP64 (0x0001)
	zip64Count       int
	unicodePath      []byte // dane pola Unicode Path (0x7075)
	unicodePathCount int
	encryption       bool // pole silnego szyfrowania (0x0017) albo AE-x (0x9901)
	patch            bool // pole opisu łatki (0x000f)
}

// parseExtra sprawdza strukturę obszaru pól dodatkowych (FMT-005, APPNOTE 4.5.1): ciąg bloków (identyfikator,
// długość, dane), który wypełnia obszar dokładnie. Zwraca niepusty komunikat, gdy struktura jest zła.
func parseExtra(b []byte) (extraInfo, string) {
	var x extraInfo
	for len(b) > 0 {
		if len(b) < 4 {
			return x, "the extra field ends inside a block header"
		}
		id, n := binary.LittleEndian.Uint16(b), int(binary.LittleEndian.Uint16(b[2:]))
		if n > len(b)-4 {
			return x, "a block of the extra field runs past its end"
		}
		data := b[4 : 4+n]
		switch id {
		case format.ExtraZip64:
			x.zip64, x.zip64Count = data, x.zip64Count+1
		case format.ExtraUnicodePath:
			x.unicodePath, x.unicodePathCount = data, x.unicodePathCount+1
		case format.ExtraStrongEncryption, format.ExtraAEx:
			x.encryption = true
		case format.ExtraPatchDescriptor:
			x.patch = true
		}
		b = b[4+n:]
	}
	return x, ""
}

// timestamp rozkłada datę i czas MS-DOS na pola (DIR-010). Wartości są wydawane tak, jak zapisano.
func timestamp(date, time uint16) Timestamp {
	t := Timestamp{
		Year: 1980 + int(date>>9), Month: int(date >> 5 & 15), Day: int(date & 31),
		Hour: int(time >> 11), Minute: int(time >> 5 & 63), Second: 2 * int(time&31),
	}
	days := 31
	switch t.Month {
	case 4, 6, 9, 11:
		days = 30
	case 2:
		days = 28
		// Zakres pola to lata 1980–2107: z lat podzielnych przez 100 przestępny jest 2000, a 2100 nie.
		if t.Year%4 == 0 && t.Year != 2100 {
			days = 29
		}
	}
	t.Valid = t.Month >= 1 && t.Month <= 12 && t.Day >= 1 && t.Day <= days && t.Hour <= 23 && t.Minute <= 59 && t.Second <= 58
	return t
}
