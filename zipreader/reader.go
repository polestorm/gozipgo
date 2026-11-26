// Package zipreader reads ZIP archives that are not trusted. It is written from the ZIP file format
// specification (APPNOTE.TXT 6.3.10) and does not use archive/zip.
//
// An archive is accepted only when its structures agree with each other; anything else is rejected with an
// *Error that carries a fixed code. Limits are on by default: the zero Options is the strict, safe setting.
// The package reads from an io.ReaderAt and never touches the file system or the network.
//
// Status: this version reads the end of central directory records (with ZIP64) and the central directory and
// gives the list of entries. It does not yet read local file headers or the contents of entries, so it does not
// yet verify that the local headers agree with the directory; that comes with Open in the next version.
package zipreader

import (
	"encoding/binary"
	"io"

	"github.com/polestorm/gozipgo/internal/format"
)

// Default limits, used when the corresponding field of Options is zero.
const (
	DefaultMaxEntries       = 65536
	DefaultMaxDirectorySize = 64 << 20
	DefaultMaxNameLength    = 4096
	DefaultMaxCommentLength = 65535
	DefaultMaxExtraLength   = 65535
)

// Options configures a Reader. The zero value is strict and has every limit at its default. For each limit,
// zero means the default and a negative value means no limit.
type Options struct {
	// MaxEntries limits the number of entries the end of central directory record declares.
	MaxEntries int
	// MaxDirectorySize limits the size of the central directory in bytes.
	MaxDirectorySize int64
	// MaxNameLength limits the length of an entry name in bytes, as stored.
	MaxNameLength int
	// MaxCommentLength limits the length in bytes of the archive comment and of each entry comment.
	MaxCommentLength int
	// MaxExtraLength limits the length in bytes of the extra field of an entry.
	MaxExtraLength int

	// UnflaggedNames says how to read a name that has bytes outside ASCII in an entry without the UTF-8 flag
	// (bit 11). The default, ASCIIOnly, rejects such a name with name-encoding. Each entry another setting was
	// applied to is reported by Reader.Relaxed.
	UnflaggedNames NameEncoding
	// CodePage is the code page used when UnflaggedNames is CodePage: the character for each of the bytes
	// 0x80 to 0xFF, in the order of the bytes. Bytes below 0x80 are ASCII. A byte whose character is below
	// U+0080 or is not a valid Unicode character makes the name invalid (name-encoding), and so does a nil
	// CodePage. The library holds no code pages other than CP437; the caller supplies the one of the system
	// the archives come from, for example code page 852 for archives made by 7-Zip on a Polish Windows.
	CodePage *[128]rune

	// FoldCase makes the check for duplicate names ignore the case of ASCII letters, so "A.txt" and "a.txt"
	// conflict. It makes the reader stricter, not looser.
	FoldCase bool
}

// NameEncoding is a way to read entry names that have bytes outside ASCII and no UTF-8 flag. The specification
// gives IBM Code Page 437 for them; in practice writers store the encoding of their system there, today most
// often UTF-8 (the zip and ditto programs of macOS do), and the archive does not say which. A name of this kind
// therefore has more than one reading, and the caller has to choose one.
type NameEncoding int

const (
	// ASCIIOnly rejects names with bytes outside ASCII in entries without the UTF-8 flag.
	ASCIIOnly NameEncoding = iota
	// CP437 decodes such names as IBM Code Page 437, as the specification says.
	CP437
	// UTF8 accepts such names when they are valid UTF-8 and rejects them otherwise.
	UTF8
	// CodePage decodes such names with the table in Options.CodePage.
	CodePage
)

// Relaxation says that an option loosened the rules for an entry of an accepted archive.
type Relaxation struct {
	Option string // the name of the field of Options, for example "UnflaggedNames"
	Entry  int    // the index of the entry, or -1 for the archive as a whole
}

// Timestamp is the modification date and time of an entry as stored in the archive: MS-DOS local time with no
// time zone, in steps of two seconds. The fields are given as stored; Valid is false when they do not form a
// calendar date and time of day (for example month 0), and they are not corrected.
type Timestamp struct {
	// Year is 1980 to 2107; Month and Day are as stored (0 to 15 and 0 to 31).
	Year, Month, Day int
	// Hour, Minute and Second are as stored (0 to 31, 0 to 63 and even numbers 0 to 62).
	Hour, Minute, Second int
	// Valid says whether the fields form a calendar date and a time of day.
	Valid bool
}

// Entry is one entry of the central directory.
type Entry struct {
	// Index is the position of the entry in the central directory, from 0.
	Index int
	// Name is the name of the entry in UTF-8, with "/" as the separator. It is never empty, absolute or
	// escaping ("..").
	Name string
	// IsDir is true when the name ends with "/".
	IsDir bool
	// Method is the compression method: 0 is Stored, 8 is Deflate. Other values are listed but cannot be
	// opened (see Unsupported).
	Method uint16
	// Flags is the general purpose bit flag as stored.
	Flags uint16
	// CRC32 is the checksum of the uncompressed content the directory declares.
	CRC32 uint32
	// CompressedSize and UncompressedSize are the sizes the directory declares, with ZIP64 resolved.
	CompressedSize   uint64
	UncompressedSize uint64
	// Modified is the stored date and time.
	Modified Timestamp
	// Comment is the comment of the entry as stored: UTF-8 when the UTF-8 flag (bit 11) is set, otherwise
	// bytes the reader does not interpret.
	Comment string
	// Extra is the extra field of the central directory header as stored. Its structure has been checked; the
	// ZIP64 block is already applied to the sizes and the offset.
	Extra []byte
	// VersionMadeBy, VersionNeeded, InternalAttrs and ExternalAttrs are given as stored and not interpreted.
	VersionMadeBy uint16
	VersionNeeded uint16
	InternalAttrs uint16
	ExternalAttrs uint32
	// LocalHeaderOffset is the position of the local file header the directory declares, with ZIP64 resolved.
	LocalHeaderOffset uint64
	// DirectoryOffset is the position in the file of the central directory header of the entry.
	DirectoryOffset int64
	// Unsupported is empty when the entry uses only features in scope. Otherwise it is the code of the error
	// that opening the entry gives: unsupported-encryption, unsupported-patch, unsupported-method or
	// unsupported-version (the first that applies, in this order).
	Unsupported string
}

// Reader gives access to one archive.
type Reader struct {
	ra      io.ReaderAt
	size    int64
	entries []Entry
	comment string
	relaxed []Relaxation
}

// New reads the end of central directory records and the central directory of the archive in the first size
// bytes of ra and checks them. It returns an *Error when the archive breaks a rule of the format, exceeds a limit
// or uses an archive-wide feature that is out of scope; an error of ra is returned unchanged (a source that ends
// before size gives io.ErrUnexpectedEOF).
//
// When an archive has more than one fault, the one found first in this fixed order is reported: the end of
// central directory record, the ZIP64 records, multi-volume fields, the bounds of the directory, the declared
// count against the declared size, limits, the directory headers in order (within a header: fixed fields, name,
// extra field, comment), duplicate names.
func New(ra io.ReaderAt, size int64, opts Options) (*Reader, error) {
	r := &Reader{ra: ra, size: size}
	end, err := findEnd(ra, size)
	if err != nil {
		return nil, err
	}
	dir, err := resolveDirectory(ra, end)
	if err != nil {
		return nil, err
	}
	if n := limitOf(int64(opts.MaxCommentLength), DefaultMaxCommentLength); n >= 0 && int64(len(end.comment)) > n {
		return nil, archiveError(CodeLimitCommentLength, end.offset, "the archive comment is longer than the limit")
	}
	if n := limitOf(int64(opts.MaxEntries), DefaultMaxEntries); n >= 0 && dir.entries > uint64(n) {
		return nil, archiveError(CodeLimitEntries, end.offset, "the archive declares more entries than the limit")
	}
	if n := limitOf(opts.MaxDirectorySize, DefaultMaxDirectorySize); n >= 0 && dir.size > uint64(n) {
		return nil, archiveError(CodeLimitDirectorySize, end.offset, "the central directory is larger than the limit")
	}
	r.comment = string(end.comment)
	if err := r.readDirectory(dir, opts); err != nil {
		return nil, err
	}
	if err := checkDuplicates(r.entries, opts.FoldCase); err != nil {
		return nil, err
	}
	return r, nil
}

// Entries returns the entries in the order of the central directory. The slice belongs to the Reader and must
// not be modified.
func (r *Reader) Entries() []Entry { return r.entries }

// Comment returns the comment of the archive as stored; the reader does not interpret its bytes.
func (r *Reader) Comment() string { return r.comment }

// Relaxed lists where an option of Options loosened the rules for this archive, in the order of the entries.
// It is empty when the archive was accepted by the strict rules alone.
func (r *Reader) Relaxed() []Relaxation { return r.relaxed }

// limitOf zamienia wartość opcji na limit: zero to wartość domyślna, ujemna to brak limitu (−1).
func limitOf(v, def int64) int64 {
	switch {
	case v == 0:
		return def
	case v < 0:
		return -1
	}
	return v
}

// readFull czyta len(buf) bajtów od przesunięcia off. Wynik nie zależy od tego, jak ra dzieli odczyty (DET-004).
// Błąd ra jest zwracany bez zmiany (SAFE-007); koniec danych przed zapełnieniem bufora też jest błędem ra, bo
// wywołujący podał rozmiar, którego źródło nie ma.
func readFull(ra io.ReaderAt, buf []byte, off int64) error {
	for len(buf) > 0 {
		n, err := ra.ReadAt(buf, off)
		if n < 0 || n > len(buf) {
			return io.ErrNoProgress // źródło łamie kontrakt io.ReaderAt
		}
		buf, off = buf[n:], off+int64(n)
		if len(buf) == 0 {
			return nil
		}
		if err != nil {
			if err == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

// endRecord to rekord końca katalogu centralnego (APPNOTE 4.3.16) z położeniem w pliku.
type endRecord struct {
	offset        int64
	disk, dirDisk uint16
	entriesOnDisk uint16
	entries       uint16
	dirSize       uint32
	dirOffset     uint32
	comment       []byte
}

// findEnd szuka rekordu końca katalogu w ostatnich 22 + 65 535 bajtach pliku (DIR-001, DIR-002). Kandydatem jest
// każde wystąpienie sygnatury, po którym rekord wraz z komentarzem o zadeklarowanej długości kończy się dokładnie
// na końcu pliku. Musi być dokładnie jeden kandydat: sygnatura w komentarzu albo w danych, która tego warunku nie
// spełnia, nie jest drugą interpretacją, bo żaden czytnik nie może jej wziąć za rekord kończący ten plik.
func findEnd(ra io.ReaderAt, size int64) (*endRecord, error) {
	if size < format.EndSize {
		return nil, archiveError(CodeNoDirectoryEnd, 0, "the file is shorter than an end of central directory record")
	}
	n := min(size, format.EndSize+format.MaxCommentLength)
	tail := make([]byte, n)
	base := size - n
	if err := readFull(ra, tail, base); err != nil {
		return nil, err
	}
	found, trailing := -1, int64(-1)
	for p := len(tail) - format.EndSize; p >= 0; p-- {
		if binary.LittleEndian.Uint32(tail[p:]) != format.SigEnd {
			continue
		}
		recordEnd := p + format.EndSize + int(binary.LittleEndian.Uint16(tail[p+20:]))
		switch {
		case recordEnd == len(tail):
			if found >= 0 {
				return nil, archiveError(CodeMultipleDirectoryEnds, base+int64(p),
					"two end of central directory records end with the file")
			}
			found = p
		case recordEnd < len(tail) && trailing < 0:
			trailing = base + int64(recordEnd)
		}
	}
	if found < 0 {
		if trailing >= 0 {
			return nil, archiveError(CodeTrailingData, trailing,
				"data after the comment of the end of central directory record")
		}
		return nil, archiveError(CodeNoDirectoryEnd, size, "no end of central directory record that ends with the file")
	}
	b := tail[found:]
	return &endRecord{
		offset:        base + int64(found),
		disk:          binary.LittleEndian.Uint16(b[4:]),
		dirDisk:       binary.LittleEndian.Uint16(b[6:]),
		entriesOnDisk: binary.LittleEndian.Uint16(b[8:]),
		entries:       binary.LittleEndian.Uint16(b[10:]),
		dirSize:       binary.LittleEndian.Uint32(b[12:]),
		dirOffset:     binary.LittleEndian.Uint32(b[16:]),
		comment:       append([]byte(nil), b[format.EndSize:]...),
	}, nil
}

// directory to położenie i rozmiar katalogu centralnego po rozstrzygnięciu ZIP64.
type directory struct {
	offset  uint64
	size    uint64
	entries uint64
}

// resolveDirectory rozstrzyga ZIP64 (DIR-005), sprawdza pola wieloczęściowe (DIR-003), granice katalogu (DIR-004)
// i zgodność zadeklarowanej liczby wpisów z rozmiarem katalogu (DIR-006), bez czytania katalogu.
//
// Lokalizator ZIP64 jest rozpoznawany po sygnaturze 20 bajtów przed końcem katalogu, niezależnie od tego, czy
// zwykły rekord ma wartości graniczne: czytnik, który woli rekordy ZIP64, zobaczyłby go, więc albo jest w pełni
// spójny ze zwykłym rekordem, albo archiwum ma dwie interpretacje i jest odrzucane.
func resolveDirectory(ra io.ReaderAt, end *endRecord) (directory, error) {
	sentinel := end.disk == format.Sentinel16 || end.dirDisk == format.Sentinel16 ||
		end.entriesOnDisk == format.Sentinel16 || end.entries == format.Sentinel16 ||
		end.dirSize == format.Sentinel32 || end.dirOffset == format.Sentinel32
	dirEnd := uint64(end.offset) // gdzie katalog musi się kończyć
	disk, dirDisk := uint64(end.disk), uint64(end.dirDisk)
	onDisk := uint64(end.entriesOnDisk)
	dir := directory{offset: uint64(end.dirOffset), size: uint64(end.dirSize), entries: uint64(end.entries)}

	var loc [format.Zip64LocatorSize]byte
	hasLocator := false
	if end.offset >= format.Zip64LocatorSize {
		if err := readFull(ra, loc[:], end.offset-format.Zip64LocatorSize); err != nil {
			return dir, err
		}
		hasLocator = binary.LittleEndian.Uint32(loc[:]) == format.SigZip64Locator
	}
	switch {
	case !hasLocator && sentinel:
		return dir, archiveError(CodeZip64Inconsistent, end.offset,
			"the end of central directory record refers to ZIP64 records, but there is no ZIP64 locator before it")
	case hasLocator:
		locOffset := end.offset - format.Zip64LocatorSize
		if binary.LittleEndian.Uint32(loc[4:]) != 0 || binary.LittleEndian.Uint32(loc[16:]) != 1 {
			return dir, archiveError(CodeUnsupportedMultiVolume, locOffset, "the ZIP64 locator describes a multi-volume archive")
		}
		q := binary.LittleEndian.Uint64(loc[8:])
		if q > uint64(locOffset) || uint64(locOffset)-q < format.Zip64EndSize {
			return dir, archiveError(CodeZip64Inconsistent, locOffset,
				"the ZIP64 locator does not point at a record before it")
		}
		var rec [format.Zip64EndSize]byte
		if err := readFull(ra, rec[:], int64(q)); err != nil {
			return dir, err
		}
		if binary.LittleEndian.Uint32(rec[:]) != format.SigZip64End {
			return dir, archiveError(CodeZip64Inconsistent, int64(q), "no ZIP64 end of central directory record where the locator points")
		}
		if v := binary.LittleEndian.Uint16(rec[14:]); v > format.VersionZip64 {
			return dir, archiveError(CodeUnsupportedVersion, int64(q),
				"the ZIP64 end of central directory record needs a version above 4.5")
		}
		recSize := binary.LittleEndian.Uint64(rec[4:])
		room := uint64(locOffset) - q - 12 // tyle bajtów jest między polem rozmiaru a lokalizatorem
		switch {
		case recSize == format.Zip64EndSizeField && recSize == room:
		case recSize > format.Zip64EndSizeField && recSize == room:
			return dir, archiveError(CodeUnsupportedZip64Data, int64(q),
				"the ZIP64 end of central directory record has an extensible data sector")
		default:
			return dir, archiveError(CodeZip64Inconsistent, int64(q),
				"the ZIP64 end of central directory record does not end where the locator begins")
		}
		disk, dirDisk = uint64(binary.LittleEndian.Uint32(rec[16:])), uint64(binary.LittleEndian.Uint32(rec[20:]))
		onDisk = binary.LittleEndian.Uint64(rec[24:])
		z := directory{entries: binary.LittleEndian.Uint64(rec[32:]), size: binary.LittleEndian.Uint64(rec[40:]),
			offset: binary.LittleEndian.Uint64(rec[48:])}
		// Pole zwykłego rekordu bez wartości granicznej musi się równać polu ZIP64.
		differs := end.disk != format.Sentinel16 && uint64(end.disk) != disk ||
			end.dirDisk != format.Sentinel16 && uint64(end.dirDisk) != dirDisk ||
			end.entriesOnDisk != format.Sentinel16 && uint64(end.entriesOnDisk) != onDisk ||
			end.entries != format.Sentinel16 && uint64(end.entries) != z.entries ||
			end.dirSize != format.Sentinel32 && uint64(end.dirSize) != z.size ||
			end.dirOffset != format.Sentinel32 && uint64(end.dirOffset) != z.offset
		if differs {
			return dir, archiveError(CodeZip64Inconsistent, end.offset,
				"the end of central directory record and the ZIP64 record disagree")
		}
		dir, dirEnd = z, q
	}
	if disk != 0 || dirDisk != 0 || onDisk != dir.entries {
		return dir, archiveError(CodeUnsupportedMultiVolume, end.offset, "the archive is split or spanned")
	}
	if dir.offset > dirEnd || dirEnd-dir.offset != dir.size {
		return dir, archiveError(CodeDirectoryOutOfBounds, end.offset,
			"the central directory does not end where the end of central directory records begin")
	}
	if dir.entries > dir.size/format.CentralHeaderSize {
		return dir, archiveError(CodeDirectoryCountMismatch, end.offset,
			"the central directory is too small for the declared number of entries")
	}
	return dir, nil
}
