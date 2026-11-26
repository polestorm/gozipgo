package zipreader

import (
	"bytes"
	"compress/flate"
	"io"
	"reflect"
	"testing"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

// deflated kompresuje dane wyrocznią (compress/flate): tak powstają strumienie DEFLATE do testów.
func deflated(data []byte, level int) []byte {
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, level)
	if err != nil {
		panic(err)
	}
	w.Write(data)
	w.Close()
	return b.Bytes()
}

// wantFull sprawdza kod pełnego New (z nagłówkami lokalnymi i układem pliku).
func wantFull(t *testing.T, label string, data []byte, opts Options, code string) (*Reader, *Error) {
	t.Helper()
	r, err := open(data, opts)
	if got := codeOf(err); got != code {
		t.Errorf("%s: got %q (%v), want %q", label, got, err, code)
	}
	e, _ := err.(*Error)
	return r, e
}

// content czyta treść wpisu o podanym indeksie.
func content(t *testing.T, r *Reader, i int) string {
	t.Helper()
	b, err := readEntry(r, r.Entries()[i])
	if err != nil {
		t.Errorf("entry %d: %v", i, err)
	}
	return string(b)
}

// pair to archiwum z dwoma wpisami: Stored i Deflate.
func pair(mut func(a *testkit.Archive)) []byte {
	a := testkit.Zip(testkit.File{Name: "a.txt", Data: []byte("hello")},
		testkit.File{Name: "b.txt", Data: []byte("world!"), Method: format.MethodDeflate})
	if mut != nil {
		mut(a)
	}
	return a.Bytes()
}

// member zmienia obszar jedynego wpisu archiwum podstawowego i poprawia położenia.
func member(file testkit.File, mut func(a *testkit.Archive, m *testkit.Member)) []byte {
	a := testkit.Zip(file)
	if mut != nil {
		mut(a, &a.Members[0])
		a.Relayout()
	}
	return a.Bytes()
}

var plain = testkit.File{Name: "a.txt", Data: []byte("hello")}
var streamed = testkit.File{Name: "a.txt", Data: []byte("hello"), Flags: format.FlagDataDescriptor}

// layoutCases to archiwa wierszy inwentarza formatu z etapu 2: nagłówek lokalny, dane i deskryptor.
func layoutCases() map[string]inventoryCase {
	c := map[string]inventoryCase{}
	reads := func(want string) func(r *Reader) string {
		return func(r *Reader) string {
			b, err := readEntry(r, r.Entries()[0])
			if err != nil || string(b) != want {
				return "the content is not readable: " + codeOf(err)
			}
			return ""
		}
	}
	local := func(mut func(h *testkit.LocalHeader)) []byte {
		return member(plain, func(a *testkit.Archive, m *testkit.Member) { mut(&m.Header) })
	}
	c["record.local-header"] = inventoryCase{archives: [][]byte{member(plain, nil)}, check: reads("hello")}
	c["record.file-data"] = inventoryCase{archives: [][]byte{
		member(plain, nil), member(testkit.File{Name: "a.txt", Data: []byte("hello"), Method: format.MethodDeflate}, nil),
		member(testkit.File{Name: "a.txt", Data: []byte("hello"), Method: format.MethodDeflate, Raw: deflated([]byte("hello"), 9)}, nil),
	}, check: reads("hello")}
	c["record.data-descriptor"] = inventoryCase{archives: [][]byte{member(streamed, nil)}, check: reads("hello")}
	c["record.spanning-marker"] = inventoryCase{archives: [][]byte{
		member(plain, func(a *testkit.Archive, m *testkit.Member) { a.Prefix = testkit.U32(format.SigSpanningMarker) }),
		member(plain, func(a *testkit.Archive, m *testkit.Member) { a.Prefix = testkit.U32(format.SigDataDescriptor) }),
	}}
	c["lfh.signature"] = one(local(func(h *testkit.LocalHeader) { h.Signature = format.SigCentralHeader }))
	c["lfh.version-needed"] = inventoryCase{archives: [][]byte{local(func(h *testkit.LocalHeader) { h.VersionNeeded = 45 })}, check: reads("hello")}
	c["lfh.flags"] = inventoryCase{archives: [][]byte{
		local(func(h *testkit.LocalHeader) { h.Flags |= format.FlagUTF8 }),
		local(func(h *testkit.LocalHeader) { h.Flags |= format.FlagEncrypted }),
		local(func(h *testkit.LocalHeader) { h.Flags |= 1 << 1 }),
	}}
	c["lfh.method"] = one(local(func(h *testkit.LocalHeader) { h.Method = format.MethodDeflate }))
	c["lfh.mod-time"] = inventoryCase{archives: [][]byte{local(func(h *testkit.LocalHeader) { h.ModTime = testkit.DOSTime(1, 2, 4) })}, check: reads("hello")}
	c["lfh.mod-date"] = inventoryCase{archives: [][]byte{local(func(h *testkit.LocalHeader) { h.ModDate = testkit.DOSDate(2001, 2, 3) })}, check: reads("hello")}
	c["lfh.crc32"] = inventoryCase{archives: [][]byte{
		local(func(h *testkit.LocalHeader) { h.CRC32 ^= 1 }),
		local(func(h *testkit.LocalHeader) { h.CRC32 = 0 }),
		member(streamed, func(a *testkit.Archive, m *testkit.Member) { m.Header.CRC32 = 1 }),
	}}
	c["lfh.compressed-size"] = inventoryCase{archives: [][]byte{
		local(func(h *testkit.LocalHeader) { h.CompressedSize = 4 }),
		local(func(h *testkit.LocalHeader) { h.CompressedSize = 0 }),
		member(streamed, func(a *testkit.Archive, m *testkit.Member) { m.Header.CompressedSize = 4 }),
	}}
	c["lfh.uncompressed-size"] = inventoryCase{archives: [][]byte{
		local(func(h *testkit.LocalHeader) { h.UncompressedSize = 6 }),
		member(streamed, func(a *testkit.Archive, m *testkit.Member) { m.Header.UncompressedSize = 6 }),
	}}
	c["lfh.name-length"] = inventoryCase{archives: [][]byte{
		local(func(h *testkit.LocalHeader) { h.Name, h.NameLength = []byte("a.tx"), 4 }),
		local(func(h *testkit.LocalHeader) { h.Name, h.NameLength = []byte("a.txt2"), 6 }),
		// Długość większa niż to, co zostało do katalogu.
		local(func(h *testkit.LocalHeader) { h.NameLength = 5; h.ExtraLength = 60000 }),
	}}
	c["lfh.extra-length"] = one(local(func(h *testkit.LocalHeader) { h.ExtraLength = 60000 }))
	c["lfh.name"] = inventoryCase{archives: [][]byte{
		local(func(h *testkit.LocalHeader) { h.Name = []byte("b.txt") }),
		local(func(h *testkit.LocalHeader) { h.Name = []byte("A.txt") }),
	}}
	extra := func(x []byte) []byte {
		return local(func(h *testkit.LocalHeader) { h.Extra, h.ExtraLength = x, uint16(len(x)) })
	}
	c["lfh.extra"] = inventoryCase{archives: [][]byte{
		extra([]byte{1}), extra([]byte{0x55, 0x54, 9, 0, 1}), extra(append(testkit.Extra(0x5455, []byte{1}), 0, 0, 0)),
	}}
	big := testkit.File{Name: "a.txt", Data: []byte("hello"), Zip64: true}
	z64 := func(mut func(h *testkit.LocalHeader)) []byte {
		return member(big, func(a *testkit.Archive, m *testkit.Member) { mut(&m.Header) })
	}
	setExtra := func(h *testkit.LocalHeader, x []byte) { h.Extra, h.ExtraLength = x, uint16(len(x)) }
	c["lfh.extra.0x0001"] = inventoryCase{archives: [][]byte{
		z64(func(h *testkit.LocalHeader) { setExtra(h, nil) }),
		z64(func(h *testkit.LocalHeader) { setExtra(h, testkit.Extra(format.ExtraZip64, testkit.U64(5))) }),
		z64(func(h *testkit.LocalHeader) {
			setExtra(h, testkit.Extra(format.ExtraZip64, testkit.U64(5), testkit.U64(5), testkit.U64(0)))
		}),
		z64(func(h *testkit.LocalHeader) { setExtra(h, append(append([]byte(nil), h.Extra...), h.Extra...)) }),
		// Pole rozmiaru bez wartości granicznej, a pole ZIP64 mówi co innego.
		z64(func(h *testkit.LocalHeader) { h.UncompressedSize = 4 }),
		// Tylko jedno pole graniczne i brak pola ZIP64.
		local(func(h *testkit.LocalHeader) { h.CompressedSize = format.Sentinel32 }),
	}}
	c["lfh.extra.0x7075"] = inventoryCase{archives: [][]byte{
		extra(testkit.UnicodePath("b.txt", []byte("a.txt"))),
		extra(testkit.UnicodePath("a.txt", []byte("b.txt"))), // CRC innej nazwy
		extra(append(testkit.UnicodePath("a.txt", []byte("a.txt")), testkit.UnicodePath("a.txt", []byte("a.txt"))...)),
	}}
	c["lfh.extra.encryption"] = inventoryCase{archives: [][]byte{
		extra(testkit.Extra(format.ExtraAEx, make([]byte, 7))),
		extra(testkit.Extra(format.ExtraStrongEncryption, make([]byte, 8))),
		extra(testkit.Extra(format.ExtraPatchDescriptor, make([]byte, 8))),
	}}
	desc := func(mut func(d *testkit.Descriptor)) []byte {
		return member(streamed, func(a *testkit.Archive, m *testkit.Member) { mut(m.Descriptor) })
	}
	c["dd.signature"] = inventoryCase{archives: [][]byte{
		desc(func(d *testkit.Descriptor) {}),
		desc(func(d *testkit.Descriptor) { d.HasSignature = false }),
		desc(func(d *testkit.Descriptor) { d.Zip64 = true }),
		desc(func(d *testkit.Descriptor) { d.Zip64, d.HasSignature = true, false }),
	}, check: reads("hello")}
	c["dd.crc32"] = inventoryCase{archives: [][]byte{
		desc(func(d *testkit.Descriptor) { d.CRC32 ^= 1 }),
		desc(func(d *testkit.Descriptor) { d.CRC32 ^= 1; d.HasSignature = false }),
	}}
	c["dd.sizes-32"] = inventoryCase{archives: [][]byte{
		desc(func(d *testkit.Descriptor) { d.CompressedSize = 4 }),
		desc(func(d *testkit.Descriptor) { d.UncompressedSize = 6 }),
		desc(func(d *testkit.Descriptor) { d.UncompressedSize = 6; d.HasSignature = false }),
	}}
	c["dd.sizes-64"] = inventoryCase{archives: [][]byte{
		desc(func(d *testkit.Descriptor) { d.Zip64 = true; d.CompressedSize = 4 }),
		desc(func(d *testkit.Descriptor) { d.Zip64 = true; d.UncompressedSize = 1<<32 + 5 }),
		desc(func(d *testkit.Descriptor) { d.Zip64, d.HasSignature = true, false; d.UncompressedSize = 6 }),
	}}
	return c
}

// TestLocalHeaderPlace: przesunięcie z katalogu musi wskazywać nagłówek lokalny przed katalogiem.
// REQ: ENT-001
func TestLocalHeaderPlace(t *testing.T) {
	wantFull(t, "valid", pair(nil), Options{}, "")
	for label, mut := range map[string]func(a *testkit.Archive){
		"offset in the directory":  func(a *testkit.Archive) { a.Central[1].LocalOffset = uint32(a.DirOffset()) },
		"offset just before it":    func(a *testkit.Archive) { a.Central[1].LocalOffset = uint32(a.DirOffset()) - 29 },
		"offset past the file":     func(a *testkit.Archive) { a.Central[1].LocalOffset = 1 << 30 },
		"no signature":             func(a *testkit.Archive) { a.Members[1].Header.Signature = 0 },
		"central header signature": func(a *testkit.Archive) { a.Members[0].Header.Signature = format.SigCentralHeader },
	} {
		_, e := wantFull(t, label, pair(mut), Options{}, CodeLocalHeaderMismatch)
		if e != nil && e.Entry < 0 {
			t.Errorf("%s: no entry in %+v", label, e)
		}
	}
	// Przesunięcie ZIP64 poza plikiem, także takie, które przepełnia 64 bity po dodaniu danych wstępnych.
	a := testkit.Zip(testkit.File{Name: "a.txt", Data: []byte("hello"), Zip64: true})
	a.Central[0].SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(5), testkit.U64(5), testkit.U64(1<<63)))
	wantFull(t, "ZIP64 offset past the file", a.Bytes(), Options{}, CodeLocalHeaderMismatch)
	a.Central[0].SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(5), testkit.U64(5), testkit.U64(^uint64(0)-3)))
	wantFull(t, "ZIP64 offset that overflows", append([]byte("prefix"), a.Bytes()...), Options{AllowPrefix: true}, CodeLocalHeaderMismatch)
}

// TestLocalHeaderFields: nagłówek lokalny zgadza się z katalogiem; z bitem 3 pola są zerowe albo równe.
// REQ: ENT-002 NAME-006
func TestLocalHeaderFields(t *testing.T) {
	for key, code := range map[string]string{
		"lfh.flags": CodeLocalHeaderMismatch, "lfh.method": CodeLocalHeaderMismatch, "lfh.crc32": CodeLocalHeaderMismatch,
		"lfh.compressed-size": CodeLocalHeaderMismatch, "lfh.uncompressed-size": CodeLocalHeaderMismatch,
		"lfh.name-length": CodeLocalHeaderMismatch, "lfh.name": CodeLocalHeaderMismatch, "lfh.extra-length": CodeLocalHeaderMismatch,
		"lfh.extra": CodeExtraField, "lfh.extra.0x0001": CodeZip64Inconsistent, "lfh.extra.0x7075": CodeNameUnicodePath,
		"lfh.extra.encryption": CodeLocalHeaderMismatch, "dd.crc32": CodeDescriptorMismatch,
		"dd.sizes-32": CodeDescriptorMismatch, "dd.sizes-64": CodeDescriptorMismatch,
	} {
		for i, data := range layoutCases()[key].archives {
			_, e := wantFull(t, key, data, Options{}, code)
			if e != nil && (e.Entry != 0 || e.Name != "a.txt") {
				t.Errorf("%s[%d]: error without the entry: %+v", key, i, e)
			}
		}
	}
	// Z bitem 3 nagłówek lokalny może też powtarzać wartości z katalogu.
	r, _ := wantFull(t, "bit 3 with real values", member(streamed, func(a *testkit.Archive, m *testkit.Member) {
		m.Header.CRC32, m.Header.CompressedSize, m.Header.UncompressedSize = m.Descriptor.CRC32, 5, 5
	}), Options{}, "")
	if r != nil && content(t, r, 0) != "hello" {
		t.Error("bit 3 with real values: content")
	}
	// Pole ZIP64 nagłówka lokalnego obok zwykłych pól o tej samej wartości (tak pisze zipfile Pythona).
	r, _ = wantFull(t, "ZIP64 extra beside real sizes", member(plain, func(a *testkit.Archive, m *testkit.Member) {
		m.Header.Extra = testkit.Extra(format.ExtraZip64, testkit.U64(5), testkit.U64(5))
		m.Header.ExtraLength = uint16(len(m.Header.Extra))
	}), Options{}, "")
	if r != nil && (content(t, r, 0) != "hello" || r.Entries()[0].DataOffset != 30+5+20) {
		t.Errorf("ZIP64 extra beside real sizes: %+v", r.Entries()[0])
	}
	// Unicode Path tylko w nagłówku lokalnym (7-Zip na Windows): nazwa w stronie kodowej, pole daje ją w UTF-8.
	a := testkit.Zip(testkit.File{Name: "x"})
	raw := []byte("za\xbe\xa2\x88\x86.txt")
	a.Central[0].SetName(raw)
	a.Central[0].Flags &^= format.FlagUTF8
	a.Members[0].Header.Name, a.Members[0].Header.NameLength = raw, uint16(len(raw))
	a.Members[0].Header.Extra = testkit.UnicodePath("zażółć.txt", raw)
	a.Members[0].Header.ExtraLength = uint16(len(a.Members[0].Header.Extra))
	a.Relayout()
	r, _ = wantFull(t, "local Unicode Path", a.Bytes(), Options{UnflaggedNames: CodePage, CodePage: cp852(t)}, "")
	if r != nil && r.Entries()[0].Name != "zażółć.txt" {
		t.Errorf("local Unicode Path: name %q", r.Entries()[0].Name)
	}
	// To samo pole przy innym odczytaniu nazwy (CP437) jest sprzecznością.
	wantFull(t, "local Unicode Path against CP437", a.Bytes(), Options{UnflaggedNames: CP437}, CodeNameUnicodePath)
}

// TestDataDescriptor: cztery postaci deskryptora rozpoznawane po długości obszaru; inne długości i brak sygnatury
// tam, gdzie długość jej wymaga, są błędem.
// REQ: ENT-003
func TestDataDescriptor(t *testing.T) {
	for i, data := range layoutCases()["dd.signature"].archives {
		r, _ := wantFull(t, "descriptor form", data, Options{}, "")
		if r != nil && content(t, r, 0) != "hello" {
			t.Errorf("form %d: content", i)
		}
	}
	bad := map[string][]byte{
		"no descriptor": member(streamed, func(a *testkit.Archive, m *testkit.Member) { m.Descriptor = nil }),
		"one byte more": member(streamed, func(a *testkit.Archive, m *testkit.Member) { a.BeforeDir = []byte{0} }),
		"eight bytes":   member(streamed, func(a *testkit.Archive, m *testkit.Member) { m.Descriptor = nil; a.BeforeDir = make([]byte, 8) }),
		"28 bytes":      member(streamed, func(a *testkit.Archive, m *testkit.Member) { a.BeforeDir = make([]byte, 12) }),
		"wrong signature": member(streamed, func(a *testkit.Archive, m *testkit.Member) {
			m.Descriptor.Signature = format.SigLocalHeader
		}),
		"64-bit form without its signature value": member(streamed, func(a *testkit.Archive, m *testkit.Member) {
			m.Descriptor.Zip64, m.Descriptor.Signature = true, 0
		}),
	}
	for label, data := range bad {
		_, e := wantFull(t, label, data, Options{}, CodeDescriptorMismatch)
		if e != nil && e.Offset != 30+5+5 {
			t.Errorf("%s: offset %d, want the end of the data", label, e.Offset)
		}
	}
	// Bit 3 w obu nagłówkach wpisu, po którym od razu zaczyna się następny wpis.
	two := testkit.Zip(plain, testkit.File{Name: "b.txt"})
	two.Central[0].Flags |= format.FlagDataDescriptor
	two.Members[0].Header.Flags |= format.FlagDataDescriptor
	wantFull(t, "next entry instead of the descriptor", two.Bytes(), Options{}, CodeDescriptorMismatch)
	// Deskryptor po wpisie bez bitu 3 to dane, na które nic nie wskazuje.
	wantFull(t, "descriptor without bit 3", member(plain, func(a *testkit.Archive, m *testkit.Member) {
		m.Descriptor = &testkit.Descriptor{HasSignature: true, Signature: format.SigDataDescriptor, CRC32: m.Header.CRC32,
			CompressedSize: 5, UncompressedSize: 5}
	}), Options{}, CodeUnreferencedData)
	// Deskryptor wpisu ZIP64 w postaci 32-bitowej nie może się równać rozmiarom ponad 4 GiB; tu rozmiary są małe,
	// więc każda postać o zgodnych wartościach jest przyjmowana.
	big := testkit.File{Name: "a.txt", Data: []byte("hello"), Zip64: true, Flags: format.FlagDataDescriptor}
	wantFull(t, "ZIP64 entry, 64-bit descriptor", member(big, nil), Options{}, "")
	wantFull(t, "ZIP64 entry, 32-bit descriptor", member(big, func(a *testkit.Archive, m *testkit.Member) { m.Descriptor.Zip64 = false }), Options{}, "")
}

// TestOverlap: dwa wpisy nie mogą dzielić bajtów pliku, a wpis nie może wchodzić na katalog.
// REQ: ENT-004
func TestOverlap(t *testing.T) {
	three := func(mut func(a *testkit.Archive)) []byte {
		a := testkit.Zip(testkit.File{Name: "a", Data: []byte("hello")}, testkit.File{Name: "b", Data: []byte("hello")},
			testkit.File{Name: "c", Data: []byte("hello")})
		mut(a)
		return a.Bytes()
	}
	check := func(label string, data []byte, entry, other int) {
		t.Helper()
		_, e := wantFull(t, label, data, Options{}, CodeEntriesOverlap)
		if e != nil && (e.Entry != entry || e.Other != other) {
			t.Errorf("%s: entries %d and %d, want %d and %d", label, e.Entry, e.Other, entry, other)
		}
	}
	// Dwa wpisy katalogu wskazują ten sam nagłówek lokalny (rdzeń bomby o nakładających się wpisach). Nazwy
	// w katalogu muszą być różne, więc drugi wpis nie zgadzałby się też z nagłówkiem; nakładanie jest zgłaszane
	// pierwsze, bo wynika z samych położeń.
	check("same local header", three(func(a *testkit.Archive) { a.Central[2].LocalOffset = a.Central[0].LocalOffset }), 2, 0)
	// Wpis, którego dane sięgają w następny wpis.
	check("data run into the next entry", three(func(a *testkit.Archive) {
		a.Central[0].CompressedSize, a.Central[0].UncompressedSize = 9, 9
		a.Members[0].Header.CompressedSize, a.Members[0].Header.UncompressedSize = 9, 9
	}), 1, 0)
	// Wpis późniejszy w katalogu, wcześniejszy w pliku.
	check("order of the pair", three(func(a *testkit.Archive) {
		a.Central[0], a.Central[2] = a.Central[2], a.Central[0]
		a.Central[2].CompressedSize, a.Central[2].UncompressedSize = 9, 9
		a.Members[0].Header.CompressedSize, a.Members[0].Header.UncompressedSize = 9, 9
	}), 2, 1)
	// Ostatni wpis sięga w katalog.
	check("data run into the directory", three(func(a *testkit.Archive) {
		a.Central[2].CompressedSize, a.Central[2].UncompressedSize = 6, 6
		a.Members[2].Header.CompressedSize, a.Members[2].Header.UncompressedSize = 6, 6
	}), 2, -1)
	// Nagłówek lokalny, którego nazwa i pola dodatkowe sięgają w następny wpis.
	check("header run into the next entry", three(func(a *testkit.Archive) {
		x := testkit.Extra(0x5455, make([]byte, 40))
		a.Members[0].Header.ExtraLength = uint16(len(x))
		copy(a.Members[1].Header.Name, "b")
	}), 1, 0)
}

// TestUnreferencedData: obszary wpisów pokrywają plik od pierwszego bajtu do katalogu.
// REQ: ENT-005
func TestUnreferencedData(t *testing.T) {
	at := func(label string, data []byte, offset int64) {
		t.Helper()
		_, e := wantFull(t, label, data, Options{}, CodeUnreferencedData)
		if e != nil && e.Offset != offset {
			t.Errorf("%s: offset %d, want %d", label, e.Offset, offset)
		}
	}
	at("data before the first entry", pair(func(a *testkit.Archive) { a.Prefix = []byte("MZ junk"); a.Relayout() }), 0)
	at("data before the directory", pair(func(a *testkit.Archive) { a.BeforeDir = []byte("junk"); a.Relayout() }), int64(len(pair(nil)))-22-2*51)
	// Wpis, którego nie ma w katalogu, między dwoma, które są.
	hidden := testkit.Zip(testkit.File{Name: "a", Data: []byte("hello")}, testkit.File{Name: "hidden", Data: []byte("secret")},
		testkit.File{Name: "c", Data: []byte("hello")})
	hidden.Central = append(hidden.Central[:1], hidden.Central[2:]...)
	hidden.FixDirectory()
	at("entry missing from the directory", hidden.Bytes(), 30+1+5)
	empty := testkit.Zip()
	wantFull(t, "empty archive", empty.Bytes(), Options{}, "")
	empty.Prefix = []byte("x")
	empty.FixDirectory()
	at("empty archive after data", empty.Bytes(), 0)
	// Luka nie jest danymi wstępnymi: AllowPrefix jej nie przepuszcza.
	wantFull(t, "gap with AllowPrefix", hidden.Bytes(), Options{AllowPrefix: true}, CodeUnreferencedData)
}

// TestAllowPrefix: dane przed pierwszym wpisem są przyjmowane tylko na mocy opcji, w obu sposobach liczenia
// przesunięć, a jej użycie jest widoczne w wyniku.
// REQ: ENT-005
func TestAllowPrefix(t *testing.T) {
	files := []testkit.File{{Name: "a.txt", Data: []byte("hello")}, {Name: "b.txt", Data: []byte("world!"), Method: format.MethodDeflate}}
	prefix := bytes.Repeat([]byte("MZ self-extractor "), 50)
	want := []Relaxation{{Option: "AllowPrefix", Entry: -1}}
	for _, mode := range []testkit.Zip64Mode{testkit.Zip64None, testkit.Zip64Records, testkit.Zip64Sentinels} {
		// Przesunięcia liczone od początku pliku.
		a := testkit.Build(mode, files...)
		a.Prefix = prefix
		a.Relayout()
		adjusted := a.Bytes()
		wantFull(t, "adjusted offsets, strict", adjusted, Options{}, CodeUnreferencedData)
		// Przesunięcia liczone od końca danych wstępnych: archiwum doklejone do programu.
		appended := append(append([]byte(nil), prefix...), testkit.Build(mode, files...).Bytes()...)
		strictCode := CodeDirectoryOutOfBounds
		if mode != testkit.Zip64None {
			strictCode = CodeZip64Inconsistent // lokalizator ZIP64 nie wskazuje rekordu
		}
		wantFull(t, "appended archive, strict", appended, Options{}, strictCode)
		for label, data := range map[string][]byte{"adjusted": adjusted, "appended": appended} {
			r, _ := wantFull(t, label, data, Options{AllowPrefix: true}, "")
			if r == nil {
				continue
			}
			if !reflect.DeepEqual(r.Relaxed(), want) || r.Entries()[0].LocalHeaderOffset != uint64(len(prefix)) ||
				content(t, r, 0) != "hello" || content(t, r, 1) != "world!" {
				t.Errorf("%s (ZIP64 mode %d): relaxed %v, first entry at %d", label, mode, r.Relaxed(), r.Entries()[0].LocalHeaderOffset)
			}
		}
	}
	// Bez danych wstępnych opcja nie zostawia śladu.
	r, _ := wantFull(t, "no prefix", pair(nil), Options{AllowPrefix: true}, "")
	if r != nil && len(r.Relaxed()) != 0 {
		t.Errorf("relaxed %v for an archive with no prefix", r.Relaxed())
	}
	// Puste archiwum po danych wstępnych.
	empty := testkit.Zip()
	empty.Prefix = prefix
	empty.FixDirectory()
	r, _ = wantFull(t, "empty archive after a prefix", empty.Bytes(), Options{AllowPrefix: true}, "")
	if r != nil && !reflect.DeepEqual(r.Relaxed(), want) {
		t.Errorf("empty archive: relaxed %v", r.Relaxed())
	}
	// Katalog za długi względem przesunięcia nie staje się „danymi wstępnymi”: przesunięcie musi być mniejsze.
	a := testkit.Zip(files...)
	a.End.DirOffset += 5
	wantFull(t, "directory offset too large", a.Bytes(), Options{AllowPrefix: true}, CodeDirectoryOutOfBounds)
}

// TestStoredSize: wpis Stored ma rozmiar skompresowany równy rozmiarowi treści.
// REQ: ENT-006
func TestStoredSize(t *testing.T) {
	data := member(plain, func(a *testkit.Archive, m *testkit.Member) {
		m.Header.UncompressedSize, a.Central[0].UncompressedSize = 4, 4
	})
	wantFull(t, "stored sizes differ", data, Options{}, CodeSizeMismatch)
	// Wpis zaszyfrowany ma 12 bajtów nagłówka szyfrowania w danych: jest poza zakresem i nie jest tu sprawdzany.
	enc := testkit.Zip(testkit.File{Name: "a.txt", Flags: format.FlagEncrypted, Data: []byte("hello"), Raw: make([]byte, 17)})
	r, _ := wantFull(t, "encrypted stored entry", enc.Bytes(), Options{}, "")
	if r != nil && r.Entries()[0].Unsupported != CodeUnsupportedEncryption {
		t.Errorf("encrypted entry: %+v", r.Entries()[0])
	}
}

// TestLayoutOneByteReads: wynik pełnego New nie zależy od podziału odczytów, także dla błędów układu.
// REQ: DET-004 SAFE-007
func TestLayoutOneByteReads(t *testing.T) {
	for key, c := range layoutCases() {
		for i, data := range c.archives {
			want := dump(open(data, Options{}))
			if got := dump(New(oneByteReader{data}, int64(len(data)), Options{})); got != want {
				t.Errorf("%s[%d]: one byte per read gives %s, want %s", key, i, got, want)
			}
			// Źródło krótsze niż podany rozmiar w każdym miejscu: błąd źródła albo kod, nigdy panika.
			for cut := 0; cut < len(data); cut++ {
				if _, err := New(bytes.NewReader(data[:cut]), int64(len(data)), Options{}); err == nil {
					t.Errorf("%s[%d]: accepted with %d of %d bytes", key, i, cut, len(data))
				} else if _, ok := err.(*Error); !ok && err != io.ErrUnexpectedEOF {
					t.Errorf("%s[%d]: cut at %d gives %v", key, i, cut, err)
				}
			}
		}
	}
}
