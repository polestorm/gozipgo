package zipwriter

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/polestorm/gozipgo/zipreader"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/golden.tsv")

// sample to dane deterministyczne: znaczniki o małym słowniku (kind 0), szum (1), zera (2).
func sample(kind, n int) []byte {
	b := make([]byte, n)
	x := uint32(kind)*2654435761 + 99
	next := func() uint32 { x = x*1664525 + 1013904223; return x >> 8 }
	switch kind {
	case 0:
		words := []string{"<w:p>", "</w:p>", "<w:r>", "<w:t>", "</w:t>", "</w:r>", "zażółć", "gęślą", "jaźń", " ", "\n", "2024"}
		for i := 0; i < n; {
			i += copy(b[i:], words[next()%uint32(len(words))])
		}
	case 1:
		for i := range b {
			b[i] = byte(next())
		}
	}
	return b
}

// item to wpis oczekiwany przy odczycie.
type item struct {
	name    string
	content []byte
}

// build wykonuje f na nowym pisarzu, zamyka go i zwraca archiwum.
func build(t testing.TB, f func(w *Writer)) []byte {
	t.Helper()
	var out bytes.Buffer
	w := New(&out)
	f(w)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.Bytes()
}

func add(t testing.TB, w *Writer, name string, method uint16, content []byte) {
	t.Helper()
	if err := w.Add(Header{Name: name, Method: method}, content); err != nil {
		t.Fatalf("Add %q: %v", name, err)
	}
}

// stream zapisuje wpis przez Create, dzieląc treść na kawałki o długości chunk (0: jeden zapis).
func stream(t testing.TB, w *Writer, h Header, content []byte, chunk int) {
	t.Helper()
	ew, err := w.Create(h)
	if err != nil {
		t.Fatalf("Create %q: %v", h.Name, err)
	}
	for len(content) > 0 {
		n := len(content)
		if chunk > 0 {
			n = min(n, chunk)
		}
		if k, err := ew.Write(content[:n]); err != nil || k != n {
			t.Fatalf("Write %q: %d, %v", h.Name, k, err)
		}
		content = content[n:]
	}
	if err := ew.Close(); err != nil {
		t.Fatalf("close %q: %v", h.Name, err)
	}
}

// strict otwiera archiwum czytnikiem w trybie ścisłym (opcje domyślne) i sprawdza, że nic nie poluzowano.
func strict(t testing.TB, data []byte, opts zipreader.Options) *zipreader.Reader {
	t.Helper()
	r, err := zipreader.New(bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		t.Fatalf("zipreader rejects the archive: %v", err)
	}
	if len(r.Relaxed()) != 0 {
		t.Fatalf("zipreader relaxed %v", r.Relaxed())
	}
	return r
}

// check czyta archiwum czytnikiem w trybie ścisłym i przez archive/zip i porównuje z oczekiwaniem: nazwy
// w kolejności, treść, brak pól dodatkowych poza ZIP64. Limit długości nazwy czytnika jest podniesiony do
// granicy formatu, bo pisarz przyjmuje każdą nazwę, którą format zapisze (ZW-007); pozostałe są domyślne.
func check(t testing.TB, data []byte, want []item) {
	t.Helper()
	r := strict(t, data, zipreader.Options{MaxNameLength: 65535})
	entries := r.Entries()
	if len(entries) != len(want) {
		t.Fatalf("zipreader lists %d entries, want %d", len(entries), len(want))
	}
	for i, e := range entries {
		if e.Name != want[i].name {
			t.Errorf("entry %d: name %q, want %q", i, e.Name, want[i].name)
		}
		rc, err := r.Open(e)
		if err != nil {
			t.Fatalf("entry %d %q: Open: %v", i, e.Name, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("entry %d %q: read: %v", i, e.Name, err)
		}
		if !bytes.Equal(got, want[i].content) {
			t.Errorf("entry %d %q: zipreader gives different content (%d bytes, want %d)", i, e.Name, len(got), len(want[i].content))
		}
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("archive/zip rejects the archive: %v", err)
	}
	if len(zr.File) != len(want) {
		t.Fatalf("archive/zip lists %d entries, want %d", len(zr.File), len(want))
	}
	for i, f := range zr.File {
		if f.Name != want[i].name {
			t.Errorf("entry %d: archive/zip gives the name %q, want %q", i, f.Name, want[i].name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("entry %d %q: archive/zip Open: %v", i, f.Name, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || !bytes.Equal(got, want[i].content) {
			t.Errorf("entry %d %q: archive/zip gives different content (%v)", i, f.Name, err)
		}
	}
}

func wantCode(t testing.TB, err error, code string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
	return e
}

// TestRoundTrip: wpisy zapisane trzema drogami, obiema metodami, czyta czytnik w trybie ścisłym i archive/zip;
// kolejność w archiwum i w katalogu jest kolejnością wywołań.
// REQ: ZW-001 ZW-007
func TestRoundTrip(t *testing.T) {
	xml, noise, zeros := sample(0, 200000), sample(1, 70000), sample(2, 8<<20)
	want := []item{
		{"mimetype", []byte("application/zip")},
		{"word/", nil},
		{"word/document.xml", xml},
		{"word/media/image1.bin", noise},
		{"zażółć gęślą jaźń.txt", []byte("pchnąć w tę łódź jeża")},
		{"empty-stored", nil},
		{"empty-deflate", nil},
		{"streamed/stored.bin", noise},
		{"streamed/deflate.xml", xml},
		{"streamed/empty", nil},
		{"streamed/dir/", nil},
		{"zeros.bin", zeros}, // ponad 1 MiB przy stosunku poniżej 1000:1: mieści się w domyślnym MaxRatio czytnika
	}
	data := build(t, func(w *Writer) {
		add(t, w, want[0].name, Stored, want[0].content)
		add(t, w, want[1].name, Stored, nil)
		add(t, w, want[2].name, Deflate, xml)
		add(t, w, want[3].name, Deflate, noise)
		add(t, w, want[4].name, Deflate, want[4].content)
		add(t, w, want[5].name, Stored, nil)
		add(t, w, want[6].name, Deflate, nil)
		stream(t, w, Header{Name: want[7].name, Method: Stored}, noise, 4096)
		stream(t, w, Header{Name: want[8].name, Method: Deflate}, xml, 1000)
		stream(t, w, Header{Name: want[9].name, Method: Deflate}, nil, 0)
		stream(t, w, Header{Name: want[10].name, Method: Deflate}, nil, 0)
		stream(t, w, Header{Name: want[11].name, Method: Deflate}, zeros, 1<<20)
	})
	check(t, data, want)
	// Położenie wpisów rośnie z indeksem: kolejność w pliku jest kolejnością katalogu.
	entries := strict(t, data, zipreader.Options{}).Entries()
	for i := 1; i < len(entries); i++ {
		if entries[i].LocalHeaderOffset <= entries[i-1].LocalHeaderOffset {
			t.Errorf("entry %d is not after entry %d in the file", i, i-1)
		}
	}
}

// TestSmallestArchive: bajty archiwum z jednym wpisem i archiwum pustego, pole po polu, według APPNOTE 4.3.7,
// 4.3.12 i 4.3.16. Pola stałe: wersja 4.5 i system 0 w „version made by”, wersja wymagana 1.0 dla Stored, data
// 1980-01-01, brak pól dodatkowych, komentarzy i atrybutów.
// REQ: ZW-003 ZW-004
func TestSmallestArchive(t *testing.T) {
	got := build(t, func(w *Writer) { add(t, w, "a", Stored, []byte("x")) })
	want := "504b0304" + "0a00" + "0000" + "0000" + "0000" + "2100" + "8316dc8c" + "01000000" + "01000000" + "0100" + "0000" + "61" + "78" +
		"504b0102" + "2d00" + "0a00" + "0000" + "0000" + "0000" + "2100" + "8316dc8c" + "01000000" + "01000000" + "0100" + "0000" + "0000" +
		"0000" + "0000" + "00000000" + "00000000" + "61" +
		"504b0506" + "0000" + "0000" + "0100" + "0100" + "2f000000" + "20000000" + "0000"
	if hex.EncodeToString(got) != want {
		t.Errorf("one Stored entry:\n got %x\nwant %s", got, want)
	}
	if got := build(t, func(w *Writer) {}); hex.EncodeToString(got) != "504b0506"+strings.Repeat("00", 18) {
		t.Errorf("empty archive: %x", got)
	}
	check(t, got, []item{{"a", []byte("x")}})
}

// TestFixedFields: to, co pisarz zapisuje poza nazwą i treścią, jest stałe i zgodne z dokumentacją: bit 11 tylko
// przy nazwie spoza ASCII, bit 3 tylko przy Create, wersja wymagana 2.0 dla Deflate i katalogów, atrybut katalogu.
// REQ: ZW-002 ZW-003 ZW-004
func TestFixedFields(t *testing.T) {
	data := build(t, func(w *Writer) {
		add(t, w, "stored", Stored, []byte("abc"))
		add(t, w, "deflate", Deflate, []byte("abc"))
		add(t, w, "dir/", Deflate, nil)
		add(t, w, "żółw", Stored, nil)
		stream(t, w, Header{Name: "streamed", Method: Deflate}, []byte("abc"), 0)
		stream(t, w, Header{Name: "dir2/", Method: Stored}, nil, 0)
	})
	want := []struct {
		flags, method, needed uint16
		attrs                 uint32
	}{{0, 0, 10, 0}, {0, 8, 20, 0}, {0, 0, 20, 0x10}, {1 << 11, 0, 10, 0}, {1 << 3, 8, 20, 0}, {0, 0, 20, 0x10}}
	for i, e := range strict(t, data, zipreader.Options{}).Entries() {
		w := want[i]
		if e.Flags != w.flags || e.Method != w.method || e.VersionNeeded != w.needed || e.ExternalAttrs != w.attrs ||
			e.VersionMadeBy != 45 || e.InternalAttrs != 0 || len(e.Extra) != 0 || e.Comment != "" {
			t.Errorf("entry %d %q: flags %#x method %d needed %d made by %d attrs %#x/%#x extra %x comment %q",
				i, e.Name, e.Flags, e.Method, e.VersionNeeded, e.VersionMadeBy, e.InternalAttrs, e.ExternalAttrs, e.Extra, e.Comment)
		}
		if m := e.Modified; m != (zipreader.Timestamp{Year: 1980, Month: 1, Day: 1, Valid: true}) {
			t.Errorf("entry %d: date %+v, want 1980-01-01 00:00:00", i, m)
		}
	}
}

// TestNames: nazwa, którą czytnik odrzuca, jest błędem z tym samym kodem; nazwa spoza ASCII idzie w UTF-8
// z bitem 11. Po błędzie pisarz jest w tym samym stanie: archiwum jest takie, jakby wywołania nie było.
// REQ: ZW-002 ZW-007
func TestNames(t *testing.T) {
	bad := []struct{ name, code string }{
		{"", CodeNameEmpty},
		{"a\x00b", CodeNameNUL},
		{"a\xffb", CodeNameEncoding},
		{"a\tb", CodeNameControl},
		{"a\x7fb", CodeNameControl},
		{`a\b`, CodeNameBackslash},
		{"/a", CodeNameAbsolute},
		{"C:/a", CodeNameAbsolute},
		{"c:a", CodeNameAbsolute},
		{"../a", CodeNameTraversal},
		{"a/../b", CodeNameTraversal},
		{"a/./b", CodeNameTraversal},
		{"a//b", CodeNameTraversal},
		{"a/..", CodeNameTraversal},
		{"/", CodeNameAbsolute},
		{strings.Repeat("n", 65536), CodeNameLength},
	}
	conflicts := []struct {
		first, second string
	}{{"a", "a"}, {"a/", "a/"}, {"a", "a/"}, {"a/", "a"}, {"a", "a/b"}, {"a/b", "a"}, {"a/b/c", "a/b"}, {"a/b", "a/b/c/d"}}
	clean := build(t, func(w *Writer) { add(t, w, "ok", Stored, []byte("1")) })
	got := build(t, func(w *Writer) {
		for i, tc := range bad {
			e := wantCode(t, w.Add(Header{Name: tc.name}, nil), tc.code)
			if e.Entry != 0 || e.Name != tc.name {
				t.Errorf("case %d: entry %d name %q", i, e.Entry, e.Name)
			}
			_, err := w.Create(Header{Name: tc.name})
			wantCode(t, err, tc.code)
			wantCode(t, w.AddRaw(Header{Name: tc.name}, strings.NewReader(""), RawInfo{}), tc.code)
		}
		add(t, w, "ok", Stored, []byte("1"))
	})
	if !bytes.Equal(got, clean) {
		t.Error("rejected names left a trace in the archive")
	}
	for _, tc := range conflicts {
		w := New(io.Discard)
		add(t, w, tc.first, Stored, nil)
		if e := wantCode(t, w.Add(Header{Name: tc.second}, nil), CodeNameDuplicate); e.Entry != 1 {
			t.Errorf("%q then %q: entry %d", tc.first, tc.second, e.Entry)
		}
	}
	// Nazwy dozwolone, także graniczne.
	long := strings.Repeat("n", 65535)
	okNames := []string{"a/", "a/b", "b", "a.b/c..d", "...", "a b", "ż/ó/ł/w", "ab:c", "1:", long}
	data := build(t, func(w *Writer) {
		for _, n := range okNames {
			add(t, w, n, Stored, nil)
		}
	})
	r := strict(t, data, zipreader.Options{MaxNameLength: 65535})
	for i, e := range r.Entries() {
		if e.Name != okNames[i] {
			t.Errorf("entry %d: %q, want %q", i, e.Name, okNames[i])
		}
	}
	// Limity czytnika nie są limitami pisarza: to samo archiwum czytnik z domyślnymi opcjami odrzuca kodem
	// limitu, a nie błędem formatu (ZW-007).
	_, err := zipreader.New(bytes.NewReader(data), int64(len(data)), zipreader.Options{})
	var re *zipreader.Error
	if !errors.As(err, &re) || re.Code != zipreader.CodeLimitNameLength {
		t.Errorf("reader with default limits, name of 65,535 bytes: %v, want %s", err, zipreader.CodeLimitNameLength)
	}
}

// TestTimestamp: data i czas pochodzą z nagłówka; wartości, których format nie zapisze, są błędem.
// REQ: ZW-003
func TestTimestamp(t *testing.T) {
	good := []Timestamp{
		{1980, 1, 1, 0, 0, 0}, {2026, 11, 27, 13, 37, 58}, {2107, 12, 31, 23, 59, 58}, {2000, 2, 29, 0, 0, 0}, {2024, 2, 29, 12, 0, 2},
	}
	data := build(t, func(w *Writer) {
		for i, ts := range good {
			if err := w.Add(Header{Name: fmt.Sprint("f", i), Modified: ts}, nil); err != nil {
				t.Fatalf("%+v: %v", ts, err)
			}
		}
	})
	for i, e := range strict(t, data, zipreader.Options{}).Entries() {
		g := good[i]
		if e.Modified != (zipreader.Timestamp{Year: g.Year, Month: g.Month, Day: g.Day, Hour: g.Hour, Minute: g.Minute, Second: g.Second, Valid: true}) {
			t.Errorf("entry %d: %+v, want %+v", i, e.Modified, g)
		}
	}
	bad := []Timestamp{
		{1979, 12, 31, 0, 0, 0}, {2108, 1, 1, 0, 0, 0}, {2026, 0, 1, 0, 0, 0}, {2026, 13, 1, 0, 0, 0}, {2026, 1, 0, 0, 0, 0},
		{2026, 4, 31, 0, 0, 0}, {2026, 2, 29, 0, 0, 0}, {2100, 2, 29, 0, 0, 0}, {2026, 1, 1, 24, 0, 0}, {2026, 1, 1, 0, 60, 0},
		{2026, 1, 1, 0, 0, 60}, {2026, 1, 1, 0, 0, 1}, {2026, 1, 1, -1, 0, 0}, {0, 0, 0, 0, 0, 2}, {1980, 1, 1, 0, -2, 0},
	}
	w := New(io.Discard)
	for _, ts := range bad {
		wantCode(t, w.Add(Header{Name: "f", Modified: ts}, nil), CodeTimestamp)
	}
}

// TestMethodAndDirectories: metoda spoza zakresu jest błędem; katalog nie ma treści i jest zapisywany jako Stored.
// REQ: ZW-005
func TestMethodAndDirectories(t *testing.T) {
	w := New(io.Discard)
	for _, m := range []uint16{1, 9, 12, 14, 93, 99, 65535} {
		wantCode(t, w.Add(Header{Name: "f", Method: m}, nil), CodeMethod)
	}
	wantCode(t, w.Add(Header{Name: "d/", Method: Stored}, []byte("x")), CodeDirectoryContent)
	wantCode(t, w.AddRaw(Header{Name: "d/"}, strings.NewReader("x"), RawInfo{CompressedSize: 1, UncompressedSize: 1}), CodeDirectoryContent)
	ew, err := w.Create(Header{Name: "d/"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ew.Write(nil); n != 0 || err != nil {
		t.Errorf("empty write to a directory: %d, %v", n, err)
	}
	_, err = ew.Write([]byte("x"))
	wantCode(t, err, CodeDirectoryContent)
	if err := ew.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// script buduje archiwum ustaloną sekwencją wywołań; chunk dzieli treść wpisów strumieniowych.
func script(t testing.TB, w *Writer, chunk int) {
	xml, noise := sample(0, 300000), sample(1, 100000)
	add(t, w, "mimetype", Stored, []byte("application/vnd.openxmlformats-officedocument.wordprocessingml.document"))
	add(t, w, "[Content_Types].xml", Deflate, xml[:5000])
	add(t, w, "word/", Stored, nil)
	stream(t, w, Header{Name: "word/document.xml", Method: Deflate, Modified: Timestamp{2026, 11, 27, 12, 0, 0}}, xml, chunk)
	stream(t, w, Header{Name: "word/media/obraz ż.bin", Method: Stored}, noise, chunk)
	stream(t, w, Header{Name: "word/media/szum.bin", Method: Deflate, Zip64: true}, noise, chunk)
	stream(t, w, Header{Name: "zera", Method: Deflate}, sample(2, 1<<20), chunk)
	raw := build(t, func(w *Writer) { add(t, w, "x", Deflate, xml[:70000]) })
	r := strict(t, raw, zipreader.Options{})
	e := r.Entries()[0]
	rr, _ := r.OpenRaw(e)
	if err := w.AddRaw(Header{Name: "copied.xml", Method: Deflate}, rr, RawInfo{e.CompressedSize, e.UncompressedSize, e.CRC32}); err != nil {
		t.Fatal(err)
	}
}

// TestSplitIndependence: ten sam ciąg wywołań daje te same bajty, niezależnie od podziału treści na zapisy.
// REQ: ZW-008
func TestSplitIndependence(t *testing.T) {
	want := build(t, func(w *Writer) { script(t, w, 0) })
	for _, chunk := range []int{1, 13, 4096, 65536, 99999} {
		if chunk == 1 && testing.Short() {
			continue
		}
		if got := build(t, func(w *Writer) { script(t, w, chunk) }); !bytes.Equal(got, want) {
			t.Errorf("chunks of %d bytes: the archive differs", chunk)
		}
	}
}

// TestGolden: skrót archiwów zbudowanych ustalonymi wywołaniami jest równy zapisanemu wzorcowi, wspólnemu dla
// wszystkich systemów i wersji Go z CI (K8): pisarz daje te same bajty niezależnie od kompilatora i systemu.
// REQ: ZW-005 ZW-008
func TestGolden(t *testing.T) {
	archives := []struct {
		name string
		f    func(w *Writer)
	}{
		{"empty", func(w *Writer) {}},
		{"one stored entry", func(w *Writer) { add(t, w, "a", Stored, []byte("x")) }},
		{"script", func(w *Writer) { script(t, w, 0) }},
		{"many", func(w *Writer) {
			part := sample(0, 4096)
			for i := 0; i < 3000; i++ {
				add(t, w, fmt.Sprintf("parts/%03d/item%06d.xml", i%50, i), Deflate, part[:1024+i%3072])
			}
		}},
	}
	var b strings.Builder
	b.WriteString("archive\tbytes\tsha256\n")
	for _, a := range archives {
		data := build(t, a.f)
		h := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s\t%d\t%s\n", a.name, len(data), hex.EncodeToString(h[:]))
	}
	const path = "testdata/golden.tsv"
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("the bytes of the archives changed; if intended, record the decision and run with -update-golden\n%s", got)
	}
}

// TestState: wpis niezamknięty, użycie po Close i zapis do zamkniętego wpisu są błędami ze stałym kodem; błędy
// sprzed pierwszego zapisanego bajtu nie psują pisarza.
// REQ: ZW-009
func TestState(t *testing.T) {
	var out bytes.Buffer
	w := New(&out)
	ew, err := w.Create(Header{Name: "a", Method: Deflate})
	if err != nil {
		t.Fatal(err)
	}
	ew.Write([]byte("abc"))
	if e := wantCode(t, w.Add(Header{Name: "b"}, nil), CodeEntryOpen); e.Entry != 1 || e.Name != "b" {
		t.Errorf("Add with an open entry: %+v", e)
	}
	_, err = w.Create(Header{Name: "b"})
	wantCode(t, err, CodeEntryOpen)
	wantCode(t, w.AddRaw(Header{Name: "b"}, strings.NewReader(""), RawInfo{}), CodeEntryOpen)
	if e := wantCode(t, w.Close(), CodeEntryOpen); e.Entry != 0 || e.Name != "a" {
		t.Errorf("Close with an open entry: %+v", e)
	}
	if err := ew.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ew.Close(); err != nil {
		t.Errorf("second Close of an entry: %v", err)
	}
	_, err = ew.Write([]byte("x"))
	if e := wantCode(t, err, CodeAfterClose); e.Entry != 0 {
		t.Errorf("Write after Close of the entry: %+v", e)
	}
	add(t, w, "b", Stored, []byte("x"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if e := wantCode(t, w.Add(Header{Name: "c"}, nil), CodeAfterClose); e.Entry != -1 {
		t.Errorf("Add after Close: %+v", e)
	}
	_, err = w.Create(Header{Name: "c"})
	wantCode(t, err, CodeAfterClose)
	wantCode(t, w.AddRaw(Header{Name: "c"}, strings.NewReader(""), RawInfo{}), CodeAfterClose)
	check(t, out.Bytes(), []item{{"a", []byte("abc")}, {"b", []byte("x")}})
}

type failingWriter struct {
	left  int
	err   error
	short bool
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.left >= len(p) {
		f.left -= len(p)
		return len(p), nil
	}
	n := f.left
	f.left = 0
	if f.short {
		return n, nil
	}
	return n, f.err
}

// TestSinkErrors: błąd docelowego io.Writer wraca niezmieniony z wywołania, które na niego trafiło, i z każdego
// następnego; zapis krótszy bez błędu to io.ErrShortWrite. Sprawdzane dla każdego miejsca przerwania.
// REQ: ZW-009
func TestSinkErrors(t *testing.T) {
	boom := errors.New("boom")
	steps := func(w *Writer) []error {
		var errs []error
		errs = append(errs, w.Add(Header{Name: "a", Method: Deflate}, []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")))
		ew, err := w.Create(Header{Name: "b", Method: Stored})
		errs = append(errs, err)
		if err == nil {
			_, err = ew.Write([]byte("content"))
			errs = append(errs, err, ew.Close())
		}
		ew, err = w.Create(Header{Name: "c", Method: Deflate})
		errs = append(errs, err)
		if err == nil {
			_, err = ew.Write([]byte("content"))
			errs = append(errs, err, ew.Close())
		}
		errs = append(errs, w.AddRaw(Header{Name: "d", Method: Stored}, strings.NewReader("raw"), RawInfo{3, 3, crc32.ChecksumIEEE([]byte("raw"))}))
		errs = append(errs, w.Close())
		return errs
	}
	var full bytes.Buffer
	for i, err := range steps(New(&full)) {
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	check(t, full.Bytes(), []item{{"a", []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}, {"b", []byte("content")}, {"c", []byte("content")}, {"d", []byte("raw")}})
	for cut := 0; cut < full.Len(); cut++ {
		for _, short := range []bool{false, true} {
			want := boom
			if short {
				want = io.ErrShortWrite
			}
			w := New(&failingWriter{left: cut, err: boom, short: short})
			failed := false
			for i, err := range steps(w) {
				switch {
				case err == nil && failed:
					t.Fatalf("cut %d: step %d succeeds after the error", cut, i)
				case err != nil && err != want:
					t.Fatalf("cut %d: step %d gives %v, want %v", cut, i, err, want)
				case err != nil:
					failed = true
				}
			}
			if !failed {
				t.Fatalf("cut %d: no step failed", cut)
			}
			if err := w.Add(Header{Name: "later"}, nil); err != want {
				t.Fatalf("cut %d: Add after the error gives %v", cut, err)
			}
			if err := w.Close(); err != want {
				t.Fatalf("cut %d: Close after the error gives %v", cut, err)
			}
		}
	}
}

// rawOf zwraca dane surowe i opis wpisu archiwum.
func rawOf(t testing.TB, r *zipreader.Reader, e zipreader.Entry) ([]byte, RawInfo) {
	t.Helper()
	rr, err := r.OpenRaw(e)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rr)
	if err != nil {
		t.Fatal(err)
	}
	return b, RawInfo{e.CompressedSize, e.UncompressedSize, e.CRC32}
}

// TestAddRaw: wpis skopiowany z czytnika bez ponownej kompresji ma te same dane, tę samą treść i ten sam CRC,
// także gdy strumień DEFLATE pochodzi z innego programu.
// REQ: ZW-010
func TestAddRaw(t *testing.T) {
	var src bytes.Buffer
	zw := zip.NewWriter(&src)
	contents := map[string][]byte{"a.xml": sample(0, 150000), "b.bin": sample(1, 40000), "c/zeros": sample(2, 500000), "d/empty": nil}
	order := []string{"a.xml", "b.bin", "c/zeros", "d/empty", "stored.bin", "dir/"}
	for _, n := range order[:4] {
		f, _ := zw.Create(n)
		f.Write(contents[n])
	}
	f, _ := zw.CreateHeader(&zip.FileHeader{Name: "stored.bin", Method: zip.Store})
	contents["stored.bin"] = sample(1, 9000)
	f.Write(contents["stored.bin"])
	zw.Create("dir/")
	zw.Close()
	explorer, err := os.ReadFile("../testdata/interop/windows11-explorer.zip")
	if err != nil {
		t.Fatal(err)
	}
	for name, archive := range map[string][]byte{"archive/zip": src.Bytes(), "Windows 11 Explorer": explorer} {
		r := strict(t, archive, zipreader.Options{})
		var want []item
		copied := build(t, func(w *Writer) {
			for _, e := range r.Entries() {
				raw, info := rawOf(t, r, e)
				if err := w.AddRaw(Header{Name: e.Name, Method: e.Method}, bytes.NewReader(raw), info); err != nil {
					t.Fatalf("%s: AddRaw %q: %v", name, e.Name, err)
				}
				rc, _ := r.Open(e)
				content, err := io.ReadAll(rc)
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, item{e.Name, content})
			}
		})
		check(t, copied, want)
		r2 := strict(t, copied, zipreader.Options{})
		for i, e := range r2.Entries() {
			src := r.Entries()[i]
			raw, _ := rawOf(t, r, src)
			raw2, _ := rawOf(t, r2, e)
			if !bytes.Equal(raw, raw2) || e.CRC32 != src.CRC32 || e.Flags&(1<<3) != 0 {
				t.Errorf("%s: entry %q: the copy has other data, another CRC-32 or a data descriptor", name, e.Name)
			}
		}
	}
}

// TestAddRawRejects: dane surowe, które nie są tym, co mówi opis, dają write-raw-content; czytnik nie dostanie
// archiwum, którego by nie przyjął. Błąd źródła danych wraca niezmieniony. Po błędzie w trakcie kopiowania
// archiwum nie da się dokończyć.
// REQ: ZW-010 ZW-007
func TestAddRawRejects(t *testing.T) {
	content := sample(0, 100000)
	good := build(t, func(w *Writer) { add(t, w, "x", Deflate, content) })
	r := strict(t, good, zipreader.Options{})
	raw, info := rawOf(t, r, r.Entries()[0])
	with := func(f func(i *RawInfo)) RawInfo { i := info; f(&i); return i }
	boom := errors.New("boom")
	cases := []struct {
		name   string
		method uint16
		raw    io.Reader
		info   RawInfo
		want   error // nil: write-raw-content
	}{
		{"wrong CRC-32", Deflate, bytes.NewReader(raw), with(func(i *RawInfo) { i.CRC32++ }), nil},
		{"content longer than declared", Deflate, bytes.NewReader(raw), with(func(i *RawInfo) { i.UncompressedSize-- }), nil},
		{"content shorter than declared", Deflate, bytes.NewReader(raw), with(func(i *RawInfo) { i.UncompressedSize++ }), nil},
		{"data end early", Deflate, bytes.NewReader(raw[:len(raw)-1]), info, nil},
		{"stream cut short", Deflate, bytes.NewReader(raw), with(func(i *RawInfo) { i.CompressedSize-- }), nil},
		{"bytes after the stream", Deflate, bytes.NewReader(append(append([]byte(nil), raw...), 0)), with(func(i *RawInfo) { i.CompressedSize++ }), nil},
		{"not a DEFLATE stream", Deflate, bytes.NewReader(bytes.Repeat([]byte{0xff}, 100)), RawInfo{100, 100, 0}, nil},
		{"Stored with another CRC-32", Stored, strings.NewReader("abc"), RawInfo{3, 3, 1}, nil},
		{"Stored data end early", Stored, strings.NewReader("ab"), RawInfo{3, 3, 0x352441c2}, nil},
		{"source fails", Deflate, io.MultiReader(bytes.NewReader(raw[:100]), errReader{boom}), info, boom},
		{"source makes no progress", Stored, stuck{}, RawInfo{3, 3, 0x352441c2}, io.ErrNoProgress},
	}
	for _, tc := range cases {
		w := New(io.Discard)
		add(t, w, "before", Stored, nil)
		err := w.AddRaw(Header{Name: "x", Method: tc.method}, tc.raw, tc.info)
		if tc.want != nil {
			if err != tc.want {
				t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
			}
		} else if e := wantCode(t, err, CodeRawContent); e.Entry != 1 || e.Name != "x" {
			t.Errorf("%s: %+v", tc.name, e)
		}
		if err2 := w.Add(Header{Name: "after"}, nil); err2 != err {
			t.Errorf("%s: Add after the error gives %v", tc.name, err2)
		}
		if err2 := w.Close(); err2 != err {
			t.Errorf("%s: Close after the error gives %v", tc.name, err2)
		}
	}
	// Wykryte przed pierwszym bajtem: pisarz działa dalej.
	w := New(io.Discard)
	wantCode(t, w.AddRaw(Header{Name: "x", Method: Stored}, strings.NewReader("abc"), RawInfo{3, 4, 0}), CodeRawContent)
	add(t, w, "x", Stored, nil)
	if err := w.Close(); err != nil {
		t.Error(err)
	}
	// Źródło dłuższe niż zadeklarowano: pisarz bierze tylko zadeklarowane bajty.
	long := append(append([]byte(nil), raw...), "trailing"...)
	data := build(t, func(w *Writer) {
		if err := w.AddRaw(Header{Name: "x", Method: Deflate}, bytes.NewReader(long), info); err != nil {
			t.Fatal(err)
		}
	})
	check(t, data, []item{{"x", content}})
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

type stuck struct{}

func (stuck) Read([]byte) (int, error) { return 0, nil }

// hasZip64End mówi, czy przed końcem katalogu stoi lokalizator ZIP64.
func hasZip64End(data []byte) bool {
	i := len(data) - 22 - 20
	return i >= 0 && string(data[i:i+4]) == "PK\x06\x07"
}

// TestZip64Forms: Header.Zip64 daje nagłówek lokalny w postaci ZIP64 (i deskryptor 64-bitowy), który czytają
// czytnik i archive/zip; katalog nie dostaje pola ZIP64, dopóki wartości mieszczą się w 32 bitach. Rekordy ZIP64
// końca katalogu pojawiają się dokładnie od 65 535 wpisów.
// REQ: ZW-004
func TestZip64Forms(t *testing.T) {
	xml := sample(0, 50000)
	data := build(t, func(w *Writer) {
		if err := w.Add(Header{Name: "added", Method: Deflate, Zip64: true}, xml); err != nil {
			t.Fatal(err)
		}
		stream(t, w, Header{Name: "streamed", Method: Deflate, Zip64: true}, xml, 0)
		stream(t, w, Header{Name: "stored", Method: Stored, Zip64: true}, xml, 0)
		if err := w.AddRaw(Header{Name: "raw", Method: Stored, Zip64: true}, strings.NewReader("abc"), RawInfo{3, 3, 0x352441c2}); err != nil {
			t.Fatal(err)
		}
		add(t, w, "plain", Stored, []byte("abc"))
	})
	check(t, data, []item{{"added", xml}, {"streamed", xml}, {"stored", xml}, {"raw", []byte("abc")}, {"plain", []byte("abc")}})
	entries := strict(t, data, zipreader.Options{}).Entries()
	for i, e := range entries {
		wantNeeded := uint16(45)
		if i == 4 {
			wantNeeded = 10
		}
		if len(e.Extra) != 0 || e.VersionNeeded != wantNeeded {
			t.Errorf("entry %q: directory extra %x, version needed %d", e.Name, e.Extra, e.VersionNeeded)
		}
	}
	// Nagłówek lokalny ZIP64 to 30 bajtów, nazwa i 20 bajtów pola; deskryptor 64-bitowy z sygnaturą to 24 bajty.
	if got, want := entries[1].DataOffset-entries[1].LocalHeaderOffset, uint64(30+len("streamed")+20); got != want {
		t.Errorf("streamed: local header of %d bytes, want %d", got, want)
	}
	if got := entries[2].LocalHeaderOffset - entries[1].DataOffset - entries[1].CompressedSize; got != 24 {
		t.Errorf("streamed: data descriptor of %d bytes, want 24", got)
	}
	if hasZip64End(data) {
		t.Error("ZIP64 end records in an archive that does not need them")
	}
	for _, n := range []int{65534, 65535, 70000} {
		data := build(t, func(w *Writer) {
			for i := 0; i < n; i++ {
				add(t, w, fmt.Sprintf("%05d", i), Stored, nil)
			}
		})
		if got, want := hasZip64End(data), n >= 65535; got != want {
			t.Errorf("%d entries: ZIP64 end records present: %v, want %v", n, got, want)
		}
		r := strict(t, data, zipreader.Options{MaxEntries: -1})
		if len(r.Entries()) != n || r.Entries()[n-1].Name != fmt.Sprintf("%05d", n-1) {
			t.Errorf("%d entries: zipreader lists %d", n, len(r.Entries()))
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(zr.File) != n {
			t.Errorf("%d entries: archive/zip: %v", n, err)
		}
	}
}

// sparse to cel zapisu i źródło odczytu, które trzyma tylko bloki inne niż zerowe: archiwum ponad 4 GiB
// z treścią z samych zer mieści się w pamięci.
type sparse struct {
	blocks map[int64][]byte
	size   int64
}

const sparseBlock = 1 << 16

var zeroBlock [sparseBlock]byte

func (s *sparse) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		idx, off := s.size/sparseBlock, int(s.size%sparseBlock)
		k := min(len(p), sparseBlock-off)
		b := s.blocks[idx]
		if b == nil && !bytes.Equal(p[:k], zeroBlock[:k]) {
			b = make([]byte, sparseBlock)
			s.blocks[idx] = b
		}
		if b != nil {
			copy(b[off:], p[:k])
		}
		s.size += int64(k)
		p = p[k:]
	}
	return n, nil
}

func (s *sparse) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) && off < s.size {
		idx, o := off/sparseBlock, int(off%sparseBlock)
		k := int(min(int64(min(len(p)-n, sparseBlock-o)), s.size-off))
		if b := s.blocks[idx]; b != nil {
			copy(p[n:n+k], b[o:])
		} else {
			clear(p[n : n+k])
		}
		n += k
		off += int64(k)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// TestLarge: wpis strumieniowy ponad 4 GiB z Header.Zip64 i wpis położony za 4 GiB dają archiwum, które czytnik
// przyjmuje w trybie ścisłym: rozmiary i przesunięcie w polu ZIP64 katalogu, rekordy ZIP64 końca katalogu. Bez
// Header.Zip64 wpis kończy się błędem write-entry-size dokładnie na granicy. Kopia przez AddRaw, gdzie rozmiary
// są znane z góry, ma je w nagłówku lokalnym.
// REQ: ZW-004 ZW-009
func TestLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("writes and reads more than 4 GiB")
	}
	const big = 4<<30 + 12345
	zeros := make([]byte, 1<<20)
	fill := func(w io.Writer, n int64) error {
		for n > 0 {
			k := min(n, int64(len(zeros)))
			if _, err := w.Write(zeros[:k]); err != nil {
				return err
			}
			n -= k
		}
		return nil
	}
	s := &sparse{blocks: map[int64][]byte{}}
	w := New(s)
	ew, err := w.Create(Header{Name: "big", Method: Stored, Zip64: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := fill(ew, big); err != nil {
		t.Fatal(err)
	}
	if err := ew.Close(); err != nil {
		t.Fatal(err)
	}
	add(t, w, "after", Deflate, []byte("after the big entry"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	opts := zipreader.Options{MaxEntrySize: -1, MaxTotalSize: -1}
	r, err := zipreader.New(s, s.size, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Relaxed()) != 0 {
		t.Fatalf("relaxed: %v", r.Relaxed())
	}
	e := r.Entries()
	if len(e) != 2 || e[0].UncompressedSize != big || e[0].CompressedSize != big || e[1].LocalHeaderOffset < 4<<30 {
		t.Fatalf("entries: %+v", e)
	}
	if len(e[0].Extra) != 4+16 || len(e[1].Extra) != 4+8 {
		t.Errorf("directory ZIP64 fields of %d and %d bytes, want 20 and 12", len(e[0].Extra), len(e[1].Extra))
	}
	drain := func(r *zipreader.Reader, e zipreader.Entry) int64 {
		rc, err := r.Open(e)
		if err != nil {
			t.Fatal(err)
		}
		n, err := io.Copy(io.Discard, rc)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := drain(r, e[0]); n != big {
		t.Errorf("big: %d bytes", n)
	}
	rc, _ := r.Open(e[1])
	if b, err := io.ReadAll(rc); err != nil || string(b) != "after the big entry" {
		t.Errorf("after: %q, %v", b, err)
	}

	// Kopia przez AddRaw: rozmiary w nagłówku lokalnym w postaci ZIP64, bez deskryptora.
	s2 := &sparse{blocks: map[int64][]byte{}}
	w2 := New(s2)
	raw, _ := r.OpenRaw(e[0])
	if err := w2.AddRaw(Header{Name: "copy", Method: Stored}, raw, RawInfo{e[0].CompressedSize, e[0].UncompressedSize, e[0].CRC32}); err != nil {
		t.Fatal(err)
	}
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	r2, err := zipreader.New(s2, s2.size, opts)
	if err != nil {
		t.Fatal(err)
	}
	if c := r2.Entries()[0]; c.UncompressedSize != big || c.CRC32 != e[0].CRC32 || c.Flags != 0 || c.DataOffset != 30+4+20 {
		t.Errorf("copy: %+v", c)
	}

	// Bez Header.Zip64: ostatni bajt, który się mieści, to 0xFFFFFFFE.
	w3 := New(io.Discard)
	ew, err = w3.Create(Header{Name: "big", Method: Stored})
	if err != nil {
		t.Fatal(err)
	}
	if err := fill(ew, 0xFFFFFFFE); err != nil {
		t.Fatal(err)
	}
	_, err = ew.Write([]byte{0})
	if e := wantCode(t, err, CodeEntrySize); e.Entry != 0 || e.Name != "big" {
		t.Errorf("%+v", e)
	}
	if err2 := w3.Close(); err2 != err {
		t.Errorf("Close after the error: %v", err2)
	}
}

// FuzzRoundTrip mierzy część kryterium K5: z dowolnych danych powstają nazwy i treść wpisów; to, co pisarz
// zapisał bez błędu, czytnik przyjmuje w trybie ścisłym z tymi samymi nazwami i treścią, a archive/zip czyta
// tak samo; te same wywołania z treścią podzieloną inaczej i bez samokontroli dają te same bajty.
// REQ: ZW-007 ZW-008 ZW-011
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte("a\x00hello\x00dir/\x00\x00b/c\x00some content some content some content"), uint8(0))
	f.Add([]byte("../x\x00evil\x00a\x00a\x00a/\x00"), uint8(3))
	f.Add(sample(0, 3000), uint8(7))
	f.Fuzz(func(t *testing.T, data []byte, mode uint8) {
		parts := bytes.Split(data, []byte{0})
		// run zapisuje archiwum; pieces to liczba zapisów, na które dzielona jest treść wpisu strumieniowego.
		run := func(pieces int, opts Options) ([]byte, []item) {
			var want []item
			var out bytes.Buffer
			w := NewWithOptions(&out, opts)
			for i := 0; i+1 < len(parts) && i < 40; i += 2 {
				name, content := string(parts[i]), bytes.Repeat(parts[i+1], 1+int(mode)%5*7)
				h := Header{Name: name, Method: []uint16{Stored, Deflate}[(int(mode)+i/2)%2], Zip64: mode&64 != 0}
				streamed := (int(mode)>>1+i/2)%2 != 0
				var err error
				if !streamed {
					err = w.Add(h, content)
				} else {
					var ew io.WriteCloser
					if ew, err = w.Create(h); err == nil {
						for rest, k := content, pieces; err == nil && k > 0; k-- {
							n := len(rest) / k
							_, err = ew.Write(rest[:n])
							rest = rest[n:]
						}
						if cerr := ew.Close(); err == nil {
							err = cerr
						}
					}
				}
				var e *Error
				switch {
				case err == nil:
					want = append(want, item{name, content})
				case !errors.As(err, &e):
					t.Fatalf("unexpected error %v", err)
				case e.Code == CodeDirectoryContent && streamed:
					want = append(want, item{name, nil}) // Create zapisał wpis katalogu; treść została odrzucona
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			return out.Bytes(), want
		}
		archive, want := run(2, Options{})
		check(t, archive, want)
		if again, _ := run(5, Options{SkipSelfCheck: true}); !bytes.Equal(again, archive) {
			t.Fatal("the same calls with content split differently and without the self-check give other bytes")
		}
	})
}
