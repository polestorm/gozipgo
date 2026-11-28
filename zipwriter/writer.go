// Package zipwriter writes ZIP archives. It is written from the specification of the format, without
// archive/zip and without compress/flate, and it has one aim besides correctness: the same calls give the same
// bytes, on every system and with every version of Go.
//
//	w := zipwriter.New(out)
//	err := w.Add(zipwriter.Header{Name: "word/document.xml", Method: zipwriter.Deflate}, content)
//	...
//	err = w.Close()
//
// What the writer puts in the archive besides the names and the content is fixed: it never reads the clock
// (the date of an entry is the one in its Header, 1980-01-01 00:00:00 when none is given), it writes no extra
// fields except ZIP64 where the format requires it, and the compressor has one algorithm with constant
// parameters. The writer does not touch the file system: it writes to an io.Writer and never seeks.
//
// An archive written without an error is one that zipreader accepts in its strict mode, with the same names and
// content: the names follow the rules of the reader, the content given to AddRaw is checked while it is copied,
// and every DEFLATE stream the writer produces is decoded while it is written and compared with the content it
// was made from (a difference is the error write-self-check; Options.SkipSelfCheck turns the comparison off).
// The comparison is of the bytes, not of a checksum. For an entry written with Create the writer keeps a copy of
// the content until the decoder has read it back: usually 1 to 2 MiB, and more for content that compresses very
// well (what 128 KiB of compressed data hold, about 130 MiB at a ratio of 1000 to 1).
// The reader has limits that the format does not have, and an archive must fit in them to be read: with the
// default Options of zipreader a name of at most 4,096 bytes, at most 65,536 entries, a central directory of at
// most 64 MiB, and for the content that is read, at most 1 GiB per entry and 4 GiB in total. The writer does not
// apply them; whoever reads a larger archive raises the limits of the reader.
//
// Entries can be written in three ways. Add takes the whole content and records the sizes and the CRC-32 in the
// local header. Create returns a writer for content of unknown length and records them in a data descriptor
// after the content (bit 3 of the flags); some programs that read an archive as a stream cannot read a Stored
// entry written that way. AddRaw copies data that are already compressed, for example from
// zipreader.Reader.OpenRaw.
//
// A Writer must not be used from several goroutines at the same time.
package zipwriter

import (
	"hash/crc32"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/polestorm/gozipgo/internal/deflate"
	"github.com/polestorm/gozipgo/internal/entryname"
	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/inflate"
)

// Compression methods of an entry (Header.Method).
const (
	Stored  uint16 = 0 // the content as it is
	Deflate uint16 = 8 // DEFLATE (RFC 1951), by the module's own compressor
)

// Timestamp is the date and time of an entry as the format records them: local time without a time zone, with
// a resolution of two seconds. The zero value stands for 1980-01-01 00:00:00, the earliest value of the format.
type Timestamp struct {
	Year, Month, Day     int // 1980 to 2107
	Hour, Minute, Second int // Second must be even
}

// Header describes an entry to write.
type Header struct {
	// Name is the name of the entry in UTF-8, with "/" between path segments. It follows the rules of the reader:
	// not empty, no zero byte or control character, no backslash, not absolute, no empty, "." or ".." segment,
	// and no conflict with an earlier entry (the same name, or a file where another entry needs a directory).
	// A name that ends with "/" is a directory: it has no content and is written Stored.
	Name string
	// Method is Stored or Deflate.
	Method uint16
	// Modified is the date and time recorded for the entry. The writer never reads the clock.
	Modified Timestamp
	// Zip64 writes the local header of the entry in the ZIP64 form. Create needs it for an entry that may reach
	// 4 GiB, compressed or not, because the header is written before the size is known; Add and AddRaw use the
	// form on their own when a size requires it.
	Zip64 bool
}

// RawInfo describes data given to AddRaw.
type RawInfo struct {
	CompressedSize   uint64 // number of bytes of the data
	UncompressedSize uint64 // number of bytes of the content
	CRC32            uint32 // CRC-32 of the content
}

// Writer writes one archive.
type Writer struct {
	dst    io.Writer
	off    uint64 // bajty zapisane do dst
	err    error  // błąd trwały: archiwum jest już zepsute albo dst odmówiło zapisu
	closed bool
	open   *entryWriter

	entries []record
	// Zbiory nazw bez końcowego ukośnika do wykrywania konfliktów (NAME-005). Tylko wyszukiwanie, nigdy
	// iteracja: wynik nie zależy od kolejności map.
	files, dirs, parents map[string]struct{}

	comp    *deflate.Writer
	check   checker // kontrola strumienia kompresora bieżącego wpisu
	nocheck bool    // Options.SkipSelfCheck
	inf     *inflate.Reader
	scratch []byte // nagłówki i kopiowanie
	packed  []byte // treść skompresowana w Add
}

// record to to, co o wpisie trafia do katalogu centralnego.
type record struct {
	name          string
	flags, method uint16
	date, time    uint16
	crc           uint32
	csize, usize  uint64
	offset        uint64
	dir           bool
	zip64         bool // nagłówek lokalny w postaci ZIP64
}

// Options configures a Writer. The zero value is the default. No option changes the bytes of the archive.
type Options struct {
	// SkipSelfCheck turns off the check of the compressor: without it the writer does not decode the DEFLATE
	// streams it produces, so a fault of the compressor would give an archive whose entry does not unpack to
	// its content, and no error. Writing compressed entries takes about half the time. It is meant for a
	// caller that verifies the archive in another way, for example by reading it back.
	SkipSelfCheck bool
}

// New returns a Writer that writes an archive to w, with the default Options. The archive is complete after
// Close.
func New(w io.Writer) *Writer {
	return NewWithOptions(w, Options{})
}

// NewWithOptions returns a Writer that writes an archive to w. The archive is complete after Close.
func NewWithOptions(w io.Writer, opts Options) *Writer {
	return &Writer{dst: w, nocheck: opts.SkipSelfCheck,
		files: map[string]struct{}{}, dirs: map[string]struct{}{}, parents: map[string]struct{}{}}
}

func (w *Writer) fail(code string, h *Header, message string) *Error {
	return &Error{Code: code, Entry: len(w.entries), Name: h.Name, Message: message}
}

// write zapisuje p do dst i liczy przesunięcie. Błąd dst jest trwały.
func (w *Writer) write(p []byte) error {
	if w.err != nil {
		return w.err
	}
	n, err := w.dst.Write(p)
	if n < 0 || n > len(p) {
		n, err = 0, io.ErrShortWrite
	}
	w.off += uint64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	return err
}

// sink to Writer widziany jako io.Writer przez kompresor.
type sink struct{ w *Writer }

func (s sink) Write(p []byte) (int, error) {
	if err := s.w.write(p); err != nil {
		return 0, err
	}
	if !s.w.nocheck {
		s.w.check.feed(p)
	}
	return len(p), nil
}

// begin sprawdza stan pisarza i nagłówek (ZW-002, ZW-003) i zwraca rekord nowego wpisu. Niczego nie zapisuje,
// więc po jego błędzie pisarz jest w tym samym stanie co przed wywołaniem. Kolejność sprawdzeń jest stała: stan,
// nazwa (pusta, bajt zerowy, kodowanie, znak sterujący, odwrotny ukośnik, ścieżka bezwzględna, segmenty,
// długość), metoda, data, konflikt nazw.
func (w *Writer) begin(h *Header) (record, error) {
	var r record
	switch {
	case w.err != nil:
		return r, w.err
	case w.closed:
		return r, &Error{Code: CodeAfterClose, Entry: -1, Message: "the writer is closed"}
	case w.open != nil:
		return r, w.fail(CodeEntryOpen, h, "the previous entry has not been closed")
	}
	name := h.Name
	if name == "" {
		return r, w.fail(CodeNameEmpty, h, "an entry without a name")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return r, w.fail(CodeNameNUL, h, "a zero byte in the name")
	}
	if !utf8.ValidString(name) {
		return r, w.fail(CodeNameEncoding, h, "the name is not valid UTF-8")
	}
	if code, message := entryname.Check(name); code != "" {
		return r, w.fail(code, h, message)
	}
	if len(name) > format.Sentinel16 {
		return r, w.fail(CodeNameLength, h, "the name is longer than 65,535 bytes")
	}
	if h.Method != Stored && h.Method != Deflate {
		return r, w.fail(CodeMethod, h, "the method is neither Stored nor Deflate")
	}
	r.date, r.time = 1<<5|1, 0 // 1980-01-01 00:00:00
	if t := h.Modified; t != (Timestamp{}) {
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
		if t.Year < 1980 || t.Year > 2107 || t.Month < 1 || t.Month > 12 || t.Day < 1 || t.Day > days ||
			t.Hour < 0 || t.Hour > 23 || t.Minute < 0 || t.Minute > 59 || t.Second < 0 || t.Second > 58 || t.Second%2 != 0 {
			return r, w.fail(CodeTimestamp, h, "the date or time is outside what the format records (1980 to 2107, even seconds)")
		}
		r.date = uint16((t.Year-1980)<<9 | t.Month<<5 | t.Day)
		r.time = uint16(t.Hour<<11 | t.Minute<<5 | t.Second/2)
	}
	r.name = name
	r.dir = name[len(name)-1] == '/'
	r.method = h.Method
	if r.dir {
		r.method = Stored
	}
	for i := 0; i < len(name); i++ {
		if name[i] >= 0x80 {
			r.flags |= format.FlagUTF8
			break
		}
	}
	r.zip64 = h.Zip64

	key := strings.TrimSuffix(name, "/")
	_, isFile := w.files[key]
	_, isDir := w.dirs[key]
	_, isParent := w.parents[key]
	conflict := isFile || isDir || !r.dir && isParent
	for i := 0; i < len(key) && !conflict; i++ {
		if key[i] == '/' {
			_, conflict = w.files[key[:i]]
		}
	}
	if conflict {
		return r, w.fail(CodeNameDuplicate, h, "the name conflicts with the name of an earlier entry")
	}
	return r, nil
}

// register zapamiętuje nazwę przyjętego wpisu.
func (w *Writer) register(r *record) {
	key := strings.TrimSuffix(r.name, "/")
	if r.dir {
		w.dirs[key] = struct{}{}
	} else {
		w.files[key] = struct{}{}
	}
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			w.parents[key[:i]] = struct{}{}
		}
	}
}

func le16(b []byte, v uint16) []byte {
	return append(b, byte(v), byte(v>>8))
}

func le32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func le64(b []byte, v uint64) []byte {
	return le32(le32(b, uint32(v)), uint32(v>>32))
}

// clamp zwraca v jako pole 32-bitowe albo wartość graniczną, gdy v się w nim nie mieści (APPNOTE 4.4.1.4).
func clamp(v uint64) uint32 {
	if v >= format.Sentinel32 {
		return format.Sentinel32
	}
	return uint32(v)
}

// versionNeeded: 4.5 dla ZIP64, 2.0 dla Deflate i katalogów, 1.0 dla pozostałych (APPNOTE 4.4.3.2).
func (r *record) versionNeeded(zip64 bool) uint16 {
	switch {
	case zip64:
		return format.VersionZip64
	case r.method == Deflate || r.dir:
		return 20
	}
	return 10
}

// header zapisuje nagłówek lokalny (APPNOTE 4.3.7) i rejestruje wpis. Dla wpisu strumieniowego (bit 3) pola
// CRC i rozmiarów są zerowe; w postaci ZIP64 pola rozmiarów mają wartość graniczną, a pole dodatkowe oba
// rozmiary (4.5.3).
func (w *Writer) header(r *record) error {
	r.offset = w.off
	streamed := r.flags&format.FlagDataDescriptor != 0
	b := w.scratch[:0]
	b = le32(b, format.SigLocalHeader)
	b = le16(b, r.versionNeeded(r.zip64))
	b = le16(b, r.flags)
	b = le16(b, r.method)
	b = le16(b, r.time)
	b = le16(b, r.date)
	b = le32(b, r.crc)
	if r.zip64 {
		b = le32(le32(b, format.Sentinel32), format.Sentinel32)
	} else {
		b = le32(le32(b, uint32(r.csize)), uint32(r.usize))
	}
	b = le16(b, uint16(len(r.name)))
	if r.zip64 {
		b = le16(b, 20)
	} else {
		b = le16(b, 0)
	}
	b = append(b, r.name...)
	if r.zip64 {
		b = le16(le16(b, format.ExtraZip64), 16)
		if streamed {
			b = le64(le64(b, 0), 0)
		} else {
			b = le64(le64(b, r.usize), r.csize)
		}
	}
	w.scratch = b
	w.register(r)
	w.entries = append(w.entries, *r)
	return w.write(b)
}

// Add writes an entry with the given content. The sizes and the CRC-32 are in the local header and the entry has
// no data descriptor. With Deflate the compressed content is held in memory until it is written, and it is
// written only after it has been decoded and the result compared with content byte by byte: if that fails, Add
// returns write-self-check and the archive is as it was before the call.
func (w *Writer) Add(h Header, content []byte) error {
	r, err := w.begin(&h)
	if err != nil {
		return err
	}
	if r.dir && len(content) > 0 {
		return w.fail(CodeDirectoryContent, &h, "content for a directory entry")
	}
	data := content
	if r.method == Deflate {
		buf := (*appender)(&w.packed)
		*buf = (*buf)[:0]
		w.compressor(buf)
		w.comp.Write(content)
		w.comp.Close()
		data = w.packed
	}
	r.crc = crc32.ChecksumIEEE(content)
	if r.method == Deflate && !w.nocheck {
		// Nic jeszcze nie zostało zapisane, więc po tym błędzie archiwum da się dokończyć.
		// Cała treść jest pod ręką, więc porównujemy bajty, nie sumy.
		w.check.expect(content)
		w.check.feed(data)
		if !w.check.finish() {
			return w.fail(CodeSelfCheck, &h, selfCheckMessage)
		}
	}
	r.usize, r.csize = uint64(len(content)), uint64(len(data))
	r.zip64 = r.zip64 || r.usize >= format.Sentinel32 || r.csize >= format.Sentinel32
	if err := w.header(&r); err != nil {
		return err
	}
	return w.write(data)
}

// appender zbiera wyjście kompresora w pamięci.
type appender []byte

func (a *appender) Write(p []byte) (int, error) {
	*a = append(*a, p...)
	return len(p), nil
}

// compressor przygotowuje kompresor i kontrolę jego strumienia dla nowego wpisu.
func (w *Writer) compressor(dst io.Writer) {
	if !w.nocheck {
		w.check.reset()
	}
	if w.comp == nil {
		w.comp = deflate.NewWriter(dst)
		return
	}
	w.comp.Reset(dst)
}

const selfCheckMessage = "the compressed data do not decode to the content of the entry"

// Create starts an entry whose content is written to the returned writer, in any number of calls. The entry
// must be closed before the next entry is started and before Close of the Writer. The sizes and the CRC-32 are
// recorded in a data descriptor after the content. An entry that reaches 4 GiB, compressed or not, fails with
// write-entry-size unless Header.Zip64 is set. A directory (a name ending with "/") takes no content and has no
// data descriptor. Close of a Deflate entry fails with write-self-check when the data written for it do not
// decode to its content, byte for byte; the archive cannot be completed then.
func (w *Writer) Create(h Header) (io.WriteCloser, error) {
	r, err := w.begin(&h)
	if err != nil {
		return nil, err
	}
	if !r.dir {
		r.flags |= format.FlagDataDescriptor
	}
	if err := w.header(&r); err != nil {
		return nil, err
	}
	e := &entryWriter{w: w, index: len(w.entries) - 1, start: w.off}
	if r.method == Deflate {
		w.compressor(sink{w})
	}
	w.open = e
	return e, nil
}

// entryWriter przyjmuje treść wpisu strumieniowego.
type entryWriter struct {
	w      *Writer
	index  int
	start  uint64 // przesunięcie początku danych
	crc    uint32
	usize  uint64
	closed bool
}

func (e *entryWriter) fail(code, message string) *Error {
	return &Error{Code: code, Entry: e.index, Name: e.w.entries[e.index].name, Message: message}
}

// limit to największy rozmiar, który mieści się w polu 32-bitowym i nie jest wartością graniczną.
const limit = format.Sentinel32 - 1

func (e *entryWriter) Write(p []byte) (int, error) {
	w := e.w
	r := &w.entries[e.index]
	switch {
	case w.err != nil:
		return 0, w.err
	case e.closed:
		return 0, e.fail(CodeAfterClose, "the entry is closed")
	case len(p) == 0:
		return 0, nil
	case r.dir:
		return 0, e.fail(CodeDirectoryContent, "content for a directory entry")
	case !r.zip64 && uint64(len(p)) > limit-e.usize:
		// Treść została już częściowo zapisana, więc archiwum nie da się dokończyć.
		w.err = e.fail(CodeEntrySize, "the entry reached 4 GiB and its header is not in the ZIP64 form (Header.Zip64)")
		return 0, w.err
	}
	e.crc = crc32.Update(e.crc, crc32.IEEETable, p)
	e.usize += uint64(len(p))
	if r.method == Deflate {
		if !w.nocheck {
			// Przed kompresorem: jego wyjście trafia do kontroli jeszcze w trakcie tego wywołania.
			w.check.content(p)
		}
		if _, err := w.comp.Write(p); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if err := w.write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close kończy wpis: domyka strumień kompresora i zapisuje deskryptor danych (APPNOTE 4.3.9) z sygnaturą,
// w postaci 64-bitowej, gdy nagłówek lokalny ma postać ZIP64.
func (e *entryWriter) Close() error {
	w := e.w
	if e.closed {
		return nil
	}
	if w.err != nil {
		return w.err
	}
	r := &w.entries[e.index]
	if r.method == Deflate && !r.dir {
		if err := w.comp.Close(); err != nil {
			return err
		}
		if !w.nocheck && !w.check.finish() {
			// Dane wpisu są już w archiwum, więc nie da się go dokończyć.
			w.err = e.fail(CodeSelfCheck, selfCheckMessage)
			return w.err
		}
	}
	e.closed = true
	w.open = nil
	if r.dir {
		return nil
	}
	r.crc, r.usize, r.csize = e.crc, e.usize, w.off-e.start
	if !r.zip64 && r.csize > limit {
		w.err = e.fail(CodeEntrySize, "the compressed entry reached 4 GiB and its header is not in the ZIP64 form (Header.Zip64)")
		return w.err
	}
	b := le32(le32(w.scratch[:0], format.SigDataDescriptor), r.crc)
	if r.zip64 {
		b = le64(le64(b, r.csize), r.usize)
	} else {
		b = le32(le32(b, uint32(r.csize)), uint32(r.usize))
	}
	w.scratch = b
	return w.write(b)
}

// AddRaw writes an entry from data that are already in the form the archive stores: for Deflate a DEFLATE
// stream, for Stored the content itself. It reads info.CompressedSize bytes from raw. The data are checked
// while they are copied: a Deflate stream is decoded, and the size and the CRC-32 of the content must be the
// ones in info. When they are not, or raw ends early, AddRaw fails with write-raw-content and the archive cannot
// be completed, because part of the entry has been written.
func (w *Writer) AddRaw(h Header, raw io.Reader, info RawInfo) error {
	r, err := w.begin(&h)
	if err != nil {
		return err
	}
	if r.dir && (info.CompressedSize != 0 || info.UncompressedSize != 0) {
		return w.fail(CodeDirectoryContent, &h, "content for a directory entry")
	}
	if r.method == Stored && info.CompressedSize != info.UncompressedSize {
		return w.fail(CodeRawContent, &h, "Stored data whose two sizes differ")
	}
	r.crc, r.csize, r.usize = info.CRC32, info.CompressedSize, info.UncompressedSize
	r.zip64 = r.zip64 || r.usize >= format.Sentinel32 || r.csize >= format.Sentinel32
	bad := w.fail(CodeRawContent, &h, "the data do not have the declared size and CRC-32, or are not a DEFLATE stream")
	if err := w.header(&r); err != nil {
		return err
	}
	if cap(w.scratch) < 32<<10 {
		w.scratch = make([]byte, 32<<10)
	}
	buf := w.scratch[:32<<10]
	t := &tee{w: w, src: raw, left: r.csize}
	var content io.Reader = t
	if r.method == Deflate {
		if w.inf == nil {
			w.inf = inflate.NewReader(t)
		} else {
			w.inf.Reset(t)
		}
		content = w.inf
	}
	var crc uint32
	var size uint64
	var failed bool
	for {
		n, err := content.Read(buf)
		crc = crc32.Update(crc, crc32.IEEETable, buf[:n])
		size += uint64(n)
		if err == io.EOF {
			break
		}
		if err != nil || size > r.usize {
			failed = true
			break
		}
	}
	switch {
	case w.err != nil:
		return w.err
	case t.err != nil:
		w.err = t.err
	case failed || t.left != 0 || size != r.usize || crc != r.crc ||
		r.method == Deflate && uint64(w.inf.Consumed()) != r.csize:
		w.err = bad
	}
	return w.err
}

// tee czyta dane surowe, najwyżej left bajtów, i każdy przeczytany bajt zapisuje do archiwum.
type tee struct {
	w    *Writer
	src  io.Reader
	left uint64
	err  error // błąd src inny niż koniec danych
}

func (t *tee) Read(p []byte) (int, error) {
	if t.left == 0 || t.err != nil || t.w.err != nil {
		return 0, io.EOF
	}
	if uint64(len(p)) > t.left {
		p = p[:t.left]
	}
	n, err := t.src.Read(p)
	if n < 0 || n > len(p) {
		n, err = 0, io.ErrNoProgress
	}
	t.left -= uint64(n)
	if werr := t.w.write(p[:n]); werr != nil {
		return 0, io.EOF
	}
	if err != nil && err != io.EOF {
		t.err = err
		return n, io.EOF
	}
	if n == 0 && err == nil {
		t.err = io.ErrNoProgress
		return 0, io.EOF
	}
	return n, err
}

// Close writes the central directory and the end records, and with them completes the archive. It fails with
// write-entry-open when an entry started with Create has not been closed. It does not close the io.Writer.
// Calling Close again returns the result of the first call that completed.
func (w *Writer) Close() error {
	switch {
	case w.err != nil:
		return w.err
	case w.closed:
		return nil
	case w.open != nil:
		return w.open.fail(CodeEntryOpen, "the entry has not been closed")
	}
	w.closed = true
	start := w.off
	b := w.scratch[:0]
	for i := range w.entries {
		r := &w.entries[i]
		// Pole ZIP64 katalogu zawiera dokładnie te pola, które nie mieszczą się w nagłówku (APPNOTE 4.5.3).
		var extra int
		for _, v := range [3]uint64{r.usize, r.csize, r.offset} {
			if v >= format.Sentinel32 {
				extra += 8
			}
		}
		attrs := uint32(0)
		if r.dir {
			attrs = 0x10 // atrybut katalogu MS-DOS
		}
		b = le32(b, format.SigCentralHeader)
		b = le16(b, format.VersionZip64) // version made by: 4.5, system MS-DOS (0)
		b = le16(b, r.versionNeeded(r.zip64 || extra > 0))
		b = le16(b, r.flags)
		b = le16(b, r.method)
		b = le16(b, r.time)
		b = le16(b, r.date)
		b = le32(b, r.crc)
		b = le32(b, clamp(r.csize))
		b = le32(b, clamp(r.usize))
		b = le16(b, uint16(len(r.name)))
		if extra > 0 {
			b = le16(b, uint16(extra+4))
		} else {
			b = le16(b, 0)
		}
		b = le16(b, 0)     // komentarz
		b = le16(b, 0)     // dysk
		b = le16(b, 0)     // atrybuty wewnętrzne
		b = le32(b, attrs) // atrybuty zewnętrzne
		b = le32(b, clamp(r.offset))
		b = append(b, r.name...)
		if extra > 0 {
			b = le16(le16(b, format.ExtraZip64), uint16(extra))
			for _, v := range [3]uint64{r.usize, r.csize, r.offset} {
				if v >= format.Sentinel32 {
					b = le64(b, v)
				}
			}
		}
		if len(b) >= 32<<10 {
			if err := w.write(b); err != nil {
				return err
			}
			b = b[:0]
		}
	}
	if err := w.write(b); err != nil {
		return err
	}
	b = b[:0]
	size, count := w.off-start, uint64(len(w.entries))
	// Rekordy ZIP64 wtedy i tylko wtedy, gdy któraś wartość nie mieści się w końcu katalogu (APPNOTE 4.4.1.4).
	if count >= format.Sentinel16 || size >= format.Sentinel32 || start >= format.Sentinel32 {
		end := w.off
		b = le32(b, format.SigZip64End)
		b = le64(b, format.Zip64EndSizeField)
		b = le16(le16(b, format.VersionZip64), format.VersionZip64)
		b = le32(le32(b, 0), 0)
		b = le64(le64(b, count), count)
		b = le64(le64(b, size), start)
		b = le32(b, format.SigZip64Locator)
		b = le32(b, 0)
		b = le64(b, end)
		b = le32(b, 1)
	}
	short := uint16(format.Sentinel16)
	if count < format.Sentinel16 {
		short = uint16(count)
	}
	b = le32(b, format.SigEnd)
	b = le16(le16(b, 0), 0)
	b = le16(le16(b, short), short)
	b = le32(le32(b, clamp(size)), clamp(start))
	b = le16(b, 0)
	w.scratch = b[:0]
	return w.write(b)
}
