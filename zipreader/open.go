package zipreader

import (
	"errors"
	"hash/crc32"
	"io"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/inflate"
)

// ErrNoEntry is returned by Open and OpenRaw for an Entry that is not an entry of the Reader.
var ErrNoEntry = errors.New("zipreader: not an entry of this reader")

// ErrClosed is returned by Read after Close.
var ErrClosed = errors.New("zipreader: read after Close")

// lookup zwraca wpis czytnika odpowiadający e: liczy się indeks, a nazwa i położenie muszą się zgadzać.
func (r *Reader) lookup(e Entry) *Entry {
	if e.Index < 0 || e.Index >= len(r.entries) {
		return nil
	}
	own := &r.entries[e.Index]
	if own.Name != e.Name || own.LocalHeaderOffset != e.LocalHeaderOffset {
		return nil
	}
	return own
}

// OpenRaw returns the data of the entry as stored in the archive, CompressedSize bytes, without decompressing
// them and without checking the CRC-32. It works for entries Open refuses (Entry.Unsupported) and is not
// subject to the limits on content. It reads no byte outside the data of the entry.
func (r *Reader) OpenRaw(e Entry) (io.Reader, error) {
	own := r.lookup(e)
	if own == nil {
		return nil, ErrNoEntry
	}
	return &rawReader{ra: r.ra, off: int64(own.DataOffset), left: own.CompressedSize}, nil
}

// rawReader czyta obszar danych wpisu. Jak readFull, nie zależy od podziału odczytów źródła, a koniec źródła
// przed końcem obszaru zgłasza jako io.ErrUnexpectedEOF.
type rawReader struct {
	ra   io.ReaderAt
	off  int64
	left uint64
}

func (s *rawReader) Read(p []byte) (int, error) {
	if s.left == 0 {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if uint64(len(p)) > s.left {
		p = p[:s.left]
	}
	n, err := s.ra.ReadAt(p, s.off)
	if n < 0 || n > len(p) {
		return 0, io.ErrNoProgress
	}
	s.off += int64(n)
	s.left -= uint64(n)
	if n > 0 {
		return n, nil // błąd, jeśli jest trwały, wróci przy następnym odczycie
	}
	switch err {
	case nil:
		return 0, io.ErrNoProgress
	case io.EOF:
		return 0, io.ErrUnexpectedEOF
	}
	return 0, err
}

// Open returns the content of the entry. Reading checks the content against the directory: a stream that is
// longer than the declared size fails with zip-size-mismatch as soon as the declared size is exceeded, and the
// read that would return io.EOF returns an error instead when the content is shorter, the compressed data do not
// end with the DEFLATE stream, or the CRC-32 differs (zip-checksum). A caller that stops reading before io.EOF
// has no confirmation that the content is intact.
//
// Open refuses, with an *Error, an entry that uses a feature out of scope (the code in Entry.Unsupported), an
// entry larger than Options.MaxEntrySize and an entry above Options.MaxRatio; the declared sizes can be used for
// that because reading enforces them. Reading fails with limit-total-size when the content read through the
// Reader exceeds Options.MaxTotalSize.
//
// Open may be called any number of times, also at the same time from several goroutines; each call gives an
// independent reader. Close releases nothing and does not close the archive. Open reads no byte outside the data
// of the entry.
func (r *Reader) Open(e Entry) (io.ReadCloser, error) {
	own := r.lookup(e)
	if own == nil {
		return nil, ErrNoEntry
	}
	at := int64(own.LocalHeaderOffset)
	if own.Unsupported != "" {
		return nil, entryError(own.Unsupported, own.Index, own.Name, at, "the entry uses a feature that is out of scope")
	}
	if r.maxEntrySize >= 0 && own.UncompressedSize > uint64(r.maxEntrySize) {
		return nil, entryError(CodeLimitEntrySize, own.Index, own.Name, at, "the entry is larger than the limit")
	}
	if r.maxRatio >= 0 && own.UncompressedSize > ratioFloor {
		// Porównanie bez przepełnienia: rozmiar / stosunek > rozmiar skompresowany.
		if q := uint64(r.maxRatio); q == 0 || own.UncompressedSize/q > own.CompressedSize ||
			own.UncompressedSize/q == own.CompressedSize && own.UncompressedSize%q != 0 {
			return nil, entryError(CodeLimitRatio, own.Index, own.Name, at, "the compression ratio of the entry is above the limit")
		}
	}
	c := &contentReader{r: r, e: own, left: own.UncompressedSize}
	raw := &rawReader{ra: r.ra, off: int64(own.DataOffset), left: own.CompressedSize}
	if own.Method == format.MethodDeflate {
		c.inf = inflate.NewReader(raw)
		c.src = c.inf
	} else {
		c.src = raw
	}
	return c, nil
}

// contentReader wydaje treść wpisu i sprawdza ją w locie (ENT-006…009).
type contentReader struct {
	r    *Reader
	e    *Entry
	src  io.Reader
	inf  *inflate.Reader // nil dla Stored
	left uint64          // bajty treści, których jeszcze nie wydano
	crc  uint32
	err  error
}

func (c *contentReader) fail(code, message string) error {
	c.err = entryError(code, c.e.Index, c.e.Name, int64(c.e.DataOffset), message)
	return c.err
}

// Close kończy odczyt; nie zamyka archiwum.
func (c *contentReader) Close() error {
	if c.err == nil || c.err == io.EOF {
		c.err = ErrClosed
	}
	return nil
}

func (c *contentReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if c.left == 0 {
		return 0, c.finish()
	}
	if uint64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.src.Read(p)
	if n > 0 {
		// Limit łączny: bajty ponad limit nie są wydawane.
		if limit := c.r.maxTotalSize; limit >= 0 {
			if over := c.r.total.Add(int64(n)) - limit; over > 0 {
				n -= int(min(int64(n), over))
				c.err = entryError(CodeLimitTotalSize, c.e.Index, c.e.Name, int64(c.e.DataOffset),
					"the content read from the archive is larger than the limit")
				if n == 0 {
					return 0, c.err
				}
			}
		}
		c.crc = crc32.Update(c.crc, crc32.IEEETable, p[:n])
		c.left -= uint64(n)
		return n, nil
	}
	switch {
	case err == nil:
		c.err = io.ErrNoProgress
	case err == io.EOF:
		// Strumień skończył się przed zadeklarowanym rozmiarem.
		if terr := c.trailing(); terr != nil {
			return 0, terr
		}
		return 0, c.fail(CodeSizeMismatch, "the content is shorter than the declared size")
	default:
		c.err = c.translate(err)
	}
	return 0, c.err
}

// finish sprawdza koniec treści po wydaniu zadeklarowanej liczby bajtów: strumień nie może dać nic więcej,
// dane skompresowane muszą się kończyć razem z nim, a CRC-32 musi się zgadzać.
func (c *contentReader) finish() error {
	var probe [1]byte
	n, err := c.src.Read(probe[:])
	switch {
	case n > 0:
		return c.fail(CodeSizeMismatch, "the content is longer than the declared size")
	case err == nil:
		c.err = io.ErrNoProgress
		return c.err
	case err != io.EOF:
		c.err = c.translate(err)
		return c.err
	}
	if terr := c.trailing(); terr != nil {
		return terr
	}
	if c.crc != c.e.CRC32 {
		return c.fail(CodeChecksum, "the CRC-32 of the content differs from the declared one")
	}
	c.err = io.EOF
	return c.err
}

// trailing sprawdza, że strumień DEFLATE zużył wszystkie dane skompresowane wpisu (ENT-007).
func (c *contentReader) trailing() error {
	if c.inf != nil && uint64(c.inf.Consumed()) != c.e.CompressedSize {
		return c.fail(CodeInflateTrailingData, "the DEFLATE stream ends before the compressed data of the entry do")
	}
	return nil
}

// translate zamienia wadę strumienia DEFLATE na błąd ze stałym kodem; inne błędy (źródła) zostają bez zmiany.
func (c *contentReader) translate(err error) error {
	ie, ok := err.(*inflate.Error)
	if !ok {
		return err
	}
	code := CodeInflateTruncated
	switch ie.Kind {
	case inflate.BlockType:
		code = CodeInflateBlockType
	case inflate.StoredLength:
		code = CodeInflateStoredLength
	case inflate.CodeLengths:
		code = CodeInflateCodeLengths
	case inflate.Distance:
		code = CodeInflateDistance
	}
	return entryError(code, c.e.Index, c.e.Name, int64(c.e.DataOffset)+ie.Offset, ie.Error())
}
