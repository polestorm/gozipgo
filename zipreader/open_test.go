package zipreader

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

// pattern to dane deterministyczne, które dobrze się kompresują, ale nie są jednym bajtem.
func pattern(n int) []byte {
	b := make([]byte, n)
	x := uint32(1)
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = "the quick brown fox <w:t>zażółć</w:t>\n"[x>>24%38]
	}
	return b
}

// readChunks czyta treść wpisu kawałkami o podanym rozmiarze i zwraca wydane bajty oraz błąd inny niż io.EOF.
func readChunks(r *Reader, e Entry, chunk int) ([]byte, error) {
	rc, err := r.Open(e)
	if err != nil {
		return nil, err
	}
	var out []byte
	buf := make([]byte, chunk)
	for {
		n, err := rc.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			// Błąd jest trwały.
			if _, again := rc.Read(buf); again != err {
				return out, errors.New("the error is not sticky")
			}
			return out, err
		}
	}
}

// deflateFile to wpis Deflate ze strumieniem z wyroczni.
func deflateFile(name string, data []byte, level int) testkit.File {
	return testkit.File{Name: name, Data: data, Method: format.MethodDeflate, Raw: deflated(data, level)}
}

// TestContent: treść wpisów Stored i Deflate równa danym źródłowym dla każdego rozmiaru bufora, także przez
// źródło dające po jednym bajcie.
// REQ: ENT-006 ENT-007 ENT-008 ENT-009 DET-004 INF-009
func TestContent(t *testing.T) {
	big := pattern(300000)
	files := []testkit.File{
		{Name: "empty.txt"},
		{Name: "dir/"},
		{Name: "stored.txt", Data: []byte("hello")},
		{Name: "stored-big.bin", Data: big},
		{Name: "deflate-blocks.txt", Data: []byte("hello"), Method: format.MethodDeflate},
		deflateFile("deflate-empty.txt", nil, 6),
		deflateFile("deflate-fast.txt", big, flate.BestSpeed),
		deflateFile("deflate-best.txt", big, flate.BestCompression),
		deflateFile("deflate-huffman.txt", big, flate.HuffmanOnly),
		{Name: "streamed.txt", Data: big, Method: format.MethodDeflate, Raw: deflated(big, 6), Flags: format.FlagDataDescriptor},
	}
	data := testkit.Zip(files...).Bytes()
	for _, source := range []io.ReaderAt{bytes.NewReader(data), oneByteReader{data}} {
		r, err := New(source, int64(len(data)), Options{})
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range r.Entries() {
			for _, chunk := range []int{1, 7, 4096, 1 << 20} {
				if _, slow := source.(oneByteReader); slow && chunk != 4096 {
					continue
				}
				got, err := readChunks(r, e, chunk)
				if err != nil || !bytes.Equal(got, files[i].Data) {
					t.Errorf("%s, chunk %d: %d bytes, %v; want %d bytes", e.Name, chunk, len(got), err, len(files[i].Data))
				}
			}
			raw, err := r.OpenRaw(e)
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(raw)
			want := files[i].Raw
			if want == nil {
				want = files[i].Data
				if files[i].Method == format.MethodDeflate {
					want = testkit.StoredDeflate(files[i].Data)
				}
			}
			if err != nil || !bytes.Equal(b, want) || uint64(len(b)) != e.CompressedSize {
				t.Errorf("%s: OpenRaw gives %d bytes, %v", e.Name, len(b), err)
			}
		}
	}
}

// TestContentErrors: treść sprzeczna z katalogiem albo wadliwy strumień DEFLATE dają stały kod, a bajty wydane
// przed błędem nie zależą od rozmiaru bufora.
// REQ: ENT-007 ENT-008 ENT-009 INF-002 INF-003 INF-004 INF-005 INF-006 DET-004
func TestContentErrors(t *testing.T) {
	hello := []byte("hello, hello, hello, hello")
	stream := deflated(hello, 6)
	raw := func(data, area []byte) []byte {
		return testkit.Zip(testkit.File{Name: "a.txt", Data: data, Method: format.MethodDeflate, Raw: area}).Bytes()
	}
	crc := func(method uint16) []byte {
		a := testkit.Zip(testkit.File{Name: "a.txt", Data: hello, Method: method})
		a.Central[0].CRC32 ^= 1
		a.Members[0].Header.CRC32 ^= 1
		return a.Bytes()
	}
	cases := []struct {
		label   string
		archive []byte
		code    string
		before  string // bajty wydane przed błędem
	}{
		{"bytes after the stream", raw(hello, append(append([]byte(nil), stream...), 0)), CodeInflateTrailingData, string(hello)},
		{"stream cut", raw(hello, stream[:len(stream)-3]), CodeInflateTruncated, ""},
		{"no data at all", raw(hello, []byte{}), CodeInflateTruncated, ""},
		{"content longer than declared", raw(hello[:5], stream), CodeSizeMismatch, "hello"},
		{"content shorter than declared", raw(append(append([]byte(nil), hello...), "!!"...), stream), CodeSizeMismatch, string(hello)},
		{"empty stream, content declared", raw(hello, deflated(nil, 6)), CodeSizeMismatch, ""},
		{"content where none is declared", raw(nil, stream), CodeSizeMismatch, ""},
		{"CRC-32, Deflate", crc(format.MethodDeflate), CodeChecksum, string(hello)},
		{"CRC-32, Stored", crc(format.MethodStored), CodeChecksum, string(hello)},
		// BFINAL 1, BTYPE 11.
		{"reserved block type", raw(hello, []byte{0x07}), CodeInflateBlockType, ""},
		// Blok niezakodowany: LEN 3, NLEN 0.
		{"stored block length", raw(hello, []byte{0x01, 3, 0, 0, 0, 'a', 'b', 'c'}), CodeInflateStoredLength, ""},
		// Blok dynamiczny z HLIT 287.
		{"code lengths", raw(hello, []byte{0xF5, 0, 0, 0, 0, 0, 0, 0}), CodeInflateCodeLengths, ""},
		// Blok ze stałymi kodami: odwołanie o odległości 1 na samym początku.
		{"distance before the start", raw(hello, []byte{0x03, 0x02}), CodeInflateDistance, ""},
	}
	for _, c := range cases {
		r, _ := wantFull(t, c.label, c.archive, Options{}, "")
		if r == nil {
			continue
		}
		for _, chunk := range []int{1, 3, 4096} {
			got, err := readChunks(r, r.Entries()[0], chunk)
			e, _ := err.(*Error)
			if e == nil || e.Code != c.code || e.Entry != 0 || e.Name != "a.txt" {
				t.Errorf("%s, chunk %d: error %v, want %s", c.label, chunk, err, c.code)
			}
			if c.code != CodeInflateTruncated && string(got) != c.before {
				t.Errorf("%s, chunk %d: %q before the error, want %q", c.label, chunk, got, c.before)
			}
			if c.code == CodeInflateTruncated && !bytes.HasPrefix(hello, got) {
				t.Errorf("%s, chunk %d: %q is not the start of the content", c.label, chunk, got)
			}
		}
	}
	// Ucięty strumień: liczba bajtów przed błędem jest ta sama dla każdego rozmiaru bufora.
	big := pattern(200000)
	cut := deflated(big, 6)
	cut = cut[:len(cut)/2]
	r, _ := wantFull(t, "big stream cut", testkit.Zip(testkit.File{Name: "a.txt", Data: big, Method: format.MethodDeflate, Raw: cut}).Bytes(), Options{}, "")
	first, _ := readChunks(r, r.Entries()[0], 1)
	for _, chunk := range []int{5, 4096, 1 << 20} {
		got, err := readChunks(r, r.Entries()[0], chunk)
		if codeOf(err) != CodeInflateTruncated || !bytes.Equal(got, first) || !bytes.HasPrefix(big, got) {
			t.Errorf("cut stream, chunk %d: %d bytes before %v, want %d", chunk, len(got), err, len(first))
		}
	}
}

// rangeRecorder zapisuje zakres bajtów, o które proszono.
type rangeRecorder struct {
	data   []byte
	lo, hi int64
}

func (r *rangeRecorder) ReadAt(p []byte, off int64) (int, error) {
	if r.lo < 0 || off < r.lo {
		r.lo = off
	}
	r.hi = max(r.hi, off+int64(len(p)))
	return bytes.NewReader(r.data).ReadAt(p, off)
}

// TestOpenReadsOnlyTheEntry: Open i OpenRaw nie czytają bajtów spoza obszaru danych wpisu.
// REQ: ENT-010
func TestOpenReadsOnlyTheEntry(t *testing.T) {
	big := pattern(50000)
	data := testkit.Zip(testkit.File{Name: "a", Data: big}, deflateFile("b", big, 6), testkit.File{Name: "c", Data: big},
		testkit.File{Name: "empty"}).Bytes()
	src := &rangeRecorder{data: data}
	r, err := New(src, int64(len(data)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Entries() {
		for _, raw := range []bool{false, true} {
			src.lo, src.hi = -1, 0
			if raw {
				rd, _ := r.OpenRaw(e)
				io.Copy(io.Discard, rd)
			} else if b, err := readEntry(r, e); err != nil || !bytes.Equal(b, big) && e.Name != "empty" {
				t.Errorf("%s: %v", e.Name, err)
			}
			lo, hi := int64(e.DataOffset), int64(e.DataOffset+e.CompressedSize)
			if src.lo >= 0 && (src.lo < lo || src.hi > hi) {
				t.Errorf("%s (raw %v): read [%d, %d), the data are [%d, %d)", e.Name, raw, src.lo, src.hi, lo, hi)
			}
		}
	}
}

// TestOpenUnsupported: wpis z cechą spoza zakresu jest na liście, Open daje jego kod, a OpenRaw działa.
// REQ: ENT-011
func TestOpenUnsupported(t *testing.T) {
	data := testkit.Zip(
		testkit.File{Name: "enc", Flags: format.FlagEncrypted, Data: []byte("hello"), Raw: []byte("0123456789ABhello")},
		testkit.File{Name: "bzip2", Method: 12, Data: []byte("hello"), Raw: []byte("BZh")},
		testkit.File{Name: "ok", Data: []byte("hello")},
	).Bytes()
	r, _ := wantFull(t, "unsupported entries", data, Options{}, "")
	if r == nil {
		return
	}
	for i, want := range []struct{ code, raw string }{{CodeUnsupportedEncryption, "0123456789ABhello"}, {CodeUnsupportedMethod, "BZh"}, {"", "hello"}} {
		e := r.Entries()[i]
		_, err := r.Open(e)
		if x, _ := err.(*Error); codeOf(err) != want.code || x != nil && (x.Entry != i || x.Name != e.Name) {
			t.Errorf("%s: Open gives %v, want %q", e.Name, err, want.code)
		}
		rd, err := r.OpenRaw(e)
		if err != nil {
			t.Fatal(err)
		}
		if b, err := io.ReadAll(rd); err != nil || string(b) != want.raw {
			t.Errorf("%s: OpenRaw gives %q, %v", e.Name, b, err)
		}
	}
}

// TestOpenContract: obcy wpis, odczyt po Close, pusty bufor, wielokrotne i równoległe Open.
// REQ: ENT-012 DET-006
func TestOpenContract(t *testing.T) {
	big := pattern(100000)
	data := testkit.Zip(testkit.File{Name: "a", Data: big}, deflateFile("b", big, 6)).Bytes()
	r, err := open(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := open(testkit.Zip(testkit.File{Name: "zzz", Data: []byte("x")}).Bytes(), Options{})
	renamed, moved := r.Entries()[0], r.Entries()[0]
	renamed.Name, moved.LocalHeaderOffset = "b", 7
	for label, e := range map[string]Entry{"zero value": {}, "entry of another reader": other.Entries()[0], "index out of range": {Index: 2},
		"negative index": {Index: -1}, "changed name": renamed, "changed offset": moved} {
		if _, err := r.Open(e); err != ErrNoEntry {
			t.Errorf("%s: Open gives %v", label, err)
		}
		if _, err := r.OpenRaw(e); err != ErrNoEntry {
			t.Errorf("%s: OpenRaw gives %v", label, err)
		}
	}
	rc, _ := r.Open(r.Entries()[1])
	if n, err := rc.Read(nil); n != 0 || err != nil {
		t.Errorf("empty read: %d, %v", n, err)
	}
	buf := make([]byte, 10)
	rc.Read(buf)
	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if _, err := rc.Read(buf); err != ErrClosed {
		t.Errorf("read after Close: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// Po Close archiwum nadal jest dostępne; równoległe odczyty dają to samo co kolejne.
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			e := r.Entries()[g%2]
			if b, err := readChunks(r, e, 1+g*1000); err != nil || !bytes.Equal(b, big) {
				t.Errorf("goroutine %d: %d bytes, %v", g, len(b), err)
			}
		}(g)
	}
	wg.Wait()
}

// TestContentLimits: rozmiar wpisu, stosunek kompresji i rozmiar łączny; limity działają niezależnie od tego,
// co mówią nagłówki, bo odczyt wymusza zadeklarowany rozmiar.
// REQ: SAFE-004 SAFE-002
func TestContentLimits(t *testing.T) {
	ten := []byte("0123456789")
	data := testkit.Zip(testkit.File{Name: "a", Data: ten}, deflateFile("b", ten, 6)).Bytes()
	openFirst := func(opts Options) error {
		r, err := open(data, opts)
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.Open(r.Entries()[0])
		return err
	}
	if err := openFirst(Options{MaxEntrySize: 9}); codeOf(err) != CodeLimitEntrySize {
		t.Errorf("MaxEntrySize 9: %v", err)
	}
	for _, limit := range []int64{10, 0, -1} {
		if err := openFirst(Options{MaxEntrySize: limit}); err != nil {
			t.Errorf("MaxEntrySize %d: %v", limit, err)
		}
	}
	// Limit łączny: 15 bajtów na dwa wpisy po 10; drugi odczyt wydaje 5 bajtów i błąd, dla każdego bufora.
	for _, chunk := range []int{1, 4, 100} {
		r, _ := open(data, Options{MaxTotalSize: 15})
		if b, err := readChunks(r, r.Entries()[0], chunk); err != nil || string(b) != "0123456789" {
			t.Errorf("chunk %d: first entry %q, %v", chunk, b, err)
		}
		b, err := readChunks(r, r.Entries()[1], chunk)
		if codeOf(err) != CodeLimitTotalSize || string(b) != "01234" {
			t.Errorf("chunk %d: second entry %q, %v", chunk, b, err)
		}
		// Limit jest wyczerpany: kolejne otwarcie nie wydaje nic.
		if b, err := readChunks(r, r.Entries()[0], chunk); codeOf(err) != CodeLimitTotalSize || len(b) != 0 {
			t.Errorf("chunk %d: after the limit %q, %v", chunk, b, err)
		}
	}
	// Ten sam wpis otwarty dwa razy liczy się dwa razy.
	r, _ := open(data, Options{MaxTotalSize: 19})
	readChunks(r, r.Entries()[0], 100)
	if b, err := readChunks(r, r.Entries()[0], 100); codeOf(err) != CodeLimitTotalSize || len(b) != 9 {
		t.Errorf("reopened entry: %d bytes, %v", len(b), err)
	}
	// Stosunek kompresji: 2 MiB zer to około 1000:1; poniżej 1 MiB limit nie działa.
	zeros := make([]byte, 2<<20)
	bomb := testkit.Zip(deflateFile("zeros", zeros, 9), deflateFile("small", zeros[:1<<20], 9)).Bytes()
	for _, c := range []struct {
		ratio int64
		code  string
	}{{0, CodeLimitRatio}, {100, CodeLimitRatio}, {2000, ""}, {-1, ""}} {
		r, err := open(bomb, Options{MaxRatio: c.ratio})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Open(r.Entries()[0]); codeOf(err) != c.code {
			t.Errorf("MaxRatio %d: %v, want %q", c.ratio, err, c.code)
		}
		if b, err := readEntry(r, r.Entries()[1]); err != nil || len(b) != 1<<20 {
			t.Errorf("MaxRatio %d: entry of 1 MiB: %v", c.ratio, err)
		}
	}
	// Wpis o zadeklarowanym rozmiarze mniejszym niż rzeczywisty nie omija limitów: odczyt kończy się na
	// zadeklarowanym rozmiarze.
	lie := testkit.Zip(testkit.File{Name: "lie", Data: zeros[:1000], Method: format.MethodDeflate, Raw: deflated(zeros, 9)}).Bytes()
	r, _ = open(lie, Options{})
	if b, err := readEntry(r, r.Entries()[0]); codeOf(err) != CodeSizeMismatch || len(b) > 1000 {
		t.Errorf("lying header: %d bytes, %v", len(b), err)
	}
}

// flakyReader zawodzi od chwili ustawienia fail.
type flakyReader struct {
	data  []byte
	fail  error
	calls int
	after int
}

func (f *flakyReader) ReadAt(p []byte, off int64) (int, error) {
	if f.fail != nil {
		f.calls++
		if f.calls > f.after {
			return 0, f.fail
		}
	}
	return bytes.NewReader(f.data).ReadAt(p, off)
}

// TestContentSourceErrors: błąd źródła w czasie odczytu treści wraca bez zmiany; źródło krótsze niż rozmiar
// daje io.ErrUnexpectedEOF; nic nie panikuje.
// REQ: SAFE-007
func TestContentSourceErrors(t *testing.T) {
	big := pattern(100000)
	data := testkit.Zip(testkit.File{Name: "a", Data: big}, deflateFile("b", big, 6)).Bytes()
	boom := errors.New("boom")
	for i := 0; i < 2; i++ {
		for after := 0; after < 4; after++ {
			src := &flakyReader{data: data}
			r, err := New(src, int64(len(data)), Options{})
			if err != nil {
				t.Fatal(err)
			}
			src.fail, src.after = boom, after
			b, err := readChunks(r, r.Entries()[i], 4096)
			if err != boom || !bytes.HasPrefix(big, b) {
				t.Errorf("entry %d, failure after %d reads: %d bytes, %v", i, after, len(b), err)
			}
			raw, _ := r.OpenRaw(r.Entries()[i])
			if _, err := io.ReadAll(raw); err != boom {
				t.Errorf("entry %d: OpenRaw gives %v", i, err)
			}
		}
		// Źródło, które po New się skróciło.
		src := &shrinkingReader{data: data}
		r, err := New(src, int64(len(data)), Options{})
		if err != nil {
			t.Fatal(err)
		}
		src.data = data[:r.Entries()[i].DataOffset+10]
		if _, err := readChunks(r, r.Entries()[i], 4096); err != io.ErrUnexpectedEOF {
			t.Errorf("entry %d, shrunk source: %v", i, err)
		}
		src.stuck = true
		if _, err := readChunks(r, r.Entries()[i], 4096); err != io.ErrNoProgress {
			t.Errorf("entry %d, stuck source: %v", i, err)
		}
	}
}

type shrinkingReader struct {
	data  []byte
	stuck bool
}

func (s *shrinkingReader) ReadAt(p []byte, off int64) (int, error) {
	if s.stuck {
		return 0, nil
	}
	return bytes.NewReader(s.data).ReadAt(p, off)
}

// BenchmarkContent porównuje odczyt treści z archive/zip na tych samych danych (wstępny pomiar K7; pomiar do
// publikacji robi tools/bench w etapie 3).
func BenchmarkContent(b *testing.B) {
	text := pattern(8 << 20)
	for _, c := range []struct {
		name string
		file testkit.File
	}{{"deflate", deflateFile("a", text, 6)}, {"stored", testkit.File{Name: "a", Data: text}}} {
		data := testkit.Zip(c.file).Bytes()
		buf := make([]byte, 32<<10)
		b.Run(c.name+"/gozipgo", func(b *testing.B) {
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r, err := New(bytes.NewReader(data), int64(len(data)), Options{})
				if err != nil {
					b.Fatal(err)
				}
				rc, _ := r.Open(r.Entries()[0])
				if _, err := io.CopyBuffer(io.Discard, struct{ io.Reader }{rc}, buf); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(c.name+"/archive-zip", func(b *testing.B) {
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				if err != nil {
					b.Fatal(err)
				}
				rc, _ := r.File[0].Open()
				if _, err := io.CopyBuffer(io.Discard, struct{ io.Reader }{rc}, buf); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
