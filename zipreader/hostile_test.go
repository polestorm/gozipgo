package zipreader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

var writeHostile = flag.String("write-hostile", "", "write the hostile corpus into this directory")
var updateHostile = flag.Bool("update-hostile", false, "rewrite testdata/hostile.json")

// hostileArchive to jedno wrogie archiwum: rodzina z SAFE-003, opis po angielsku (trafia do manifestu i do
// docs/comparison.md) i kod, którym czytnik w trybie ścisłym je odrzuca. atRead mówi, że New je przyjmuje, a kod
// daje Open albo odczyt treści któregoś wpisu.
type hostileArchive struct {
	ID     string `json:"id"`
	Family string `json:"family"`
	What   string `json:"what"`
	Code   string `json:"code"`
	AtRead bool   `json:"at_read"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
	data   []byte
}

// hostileArchives buduje korpus wrogi (SAFE-003, rodziny a–n). Budowa jest deterministyczna: te same bajty
// w każdym przebiegu, na każdym systemie i w każdej wersji Go (strumienie DEFLATE zapisane dosłownie albo jako
// bloki niezakodowane; żaden nie pochodzi z compress/flate, którego wynik zależy od wersji).
func hostileArchives() []hostileArchive {
	var out []hostileArchive
	add := func(id, family, what, code string, atRead bool, data []byte) {
		sum := sha256.Sum256(data)
		out = append(out, hostileArchive{ID: id, Family: family, What: what, Code: code, AtRead: atRead,
			Size: len(data), SHA256: hex.EncodeToString(sum[:]), data: data})
	}
	hello := []byte("hello")
	plainFile := testkit.File{Name: "a.txt", Data: hello}
	zip := func(mut func(a *testkit.Archive), files ...testkit.File) []byte {
		if len(files) == 0 {
			files = []testkit.File{plainFile, {Name: "b.txt", Data: []byte("world!")}}
		}
		a := testkit.Zip(files...)
		if mut != nil {
			mut(a)
		}
		return a.Bytes()
	}

	// (a) Bomba klasyczna: 16 MiB zer w strumieniu o stosunku około 1030:1.
	bomb, bombSize := zeroStream(16 << 20)
	zeros := make([]byte, bombSize)
	add("bomb-ratio", "a", "one entry of 16 MiB of zeros compressed 1032:1, the most DEFLATE can do", CodeLimitRatio, true,
		zip(nil, testkit.File{Name: "zeros.bin", Data: zeros, Method: format.MethodDeflate, Raw: bomb}))
	// (b) Kłamiący nagłówek: zadeklarowane 1000 bajtów, strumień daje 16 MiB.
	add("bomb-lying-size", "b", "the headers declare 1000 bytes, the DEFLATE stream expands to 16 MiB", CodeSizeMismatch, true,
		zip(nil, testkit.File{Name: "small.bin", Data: zeros[:1000], Method: format.MethodDeflate, Raw: bomb}))
	// (c) Wiele wpisów katalogu, jeden strumień.
	kernelStream, kernelSize := zeroStream(1 << 20)
	kernel := testkit.File{Name: "k0", Data: zeros[:kernelSize], Method: format.MethodDeflate, Raw: kernelStream}
	add("overlap-shared-stream", "c", "64 directory entries point at one local header and one DEFLATE stream of 1 MiB", CodeEntriesOverlap, false,
		zip(func(a *testkit.Archive) {
			first := a.Central[0]
			for i := 1; i < 64; i++ {
				h := first
				h.SetName([]byte(fmt.Sprintf("k%d", i%10)))
				if i >= 10 {
					h.SetName([]byte(fmt.Sprintf("k%d%d", i/10, i%10)))
				}
				a.Central = append(a.Central, h)
			}
			a.FixDirectory()
		}, kernel))
	// (d) Nagłówek lokalny „cytowany” w bloku niezakodowanym poprzedniego wpisu (budowa bomby Fifielda): wpis A
	// zawiera blok niezakodowany, którego dane to nagłówek lokalny wpisu B, a dalej strumień wspólny dla obu.
	add("overlap-quoted-header", "d", "the stored block of entry A quotes the local header of entry B, and both entries continue into the same DEFLATE stream", CodeEntriesOverlap, false,
		quotedOverlap())
	// (e) Liczba wpisów.
	many := make([]testkit.File, 65537)
	for i := range many {
		many[i] = testkit.File{Name: fmt.Sprintf("%05d", i)}
	}
	add("entries-over-limit", "e", "65,537 empty entries, one more than the default limit", CodeLimitEntries, false,
		testkit.Build(testkit.Zip64Sentinels, many...).Bytes())
	add("entries-count-lie", "e", "the end record declares 60,000 entries in a directory of 102 bytes", CodeDirectoryCountMismatch, false,
		zip(func(a *testkit.Archive) { a.End.Entries, a.End.EntriesOnDisk = 60000, 60000 }))
	// (f) Katalog poza plikiem i katalog nachodzący na dane.
	add("directory-past-file", "f", "the central directory offset points past the end of the file", CodeDirectoryOutOfBounds, false,
		zip(func(a *testkit.Archive) { a.End.DirOffset = 1 << 30 }))
	add("directory-over-data", "f", "the central directory is declared to start inside the data of the first entry", CodeDirectorySignature, false,
		zip(func(a *testkit.Archive) { a.End.DirOffset = 10; a.End.DirSize += uint32(a.DirOffset()) - 10 }))
	// (g) Końce katalogu.
	inner := zip(nil, testkit.File{Name: "inner.txt", Data: []byte("inner")})
	add("two-ends-comment", "g", "a whole second archive in the comment of the first", CodeMultipleDirectoryEnds, false,
		zip(func(a *testkit.Archive) { a.End.SetComment(inner) }))
	add("concatenated", "g", "two archives one after the other", CodeDirectoryOutOfBounds, false, append(zip(nil), inner...))
	add("data-after-end", "g", "bytes after the comment of the end of central directory record", CodeTrailingData, false,
		zip(func(a *testkit.Archive) { a.Trailer = []byte("trailing bytes") }))
	// (h) Katalog i nagłówek lokalny mówią co innego.
	add("local-name", "h", "the local header names the entry e.exe, the directory a.txt", CodeLocalHeaderMismatch, false,
		zip(func(a *testkit.Archive) { a.Members[0].Header.Name = []byte("e.exe") }))
	add("local-method", "h", "the local header says Deflate, the directory says Stored", CodeLocalHeaderMismatch, false,
		zip(func(a *testkit.Archive) { a.Members[0].Header.Method = format.MethodDeflate }))
	add("local-size", "h", "the local header declares 3 bytes, the directory 5", CodeLocalHeaderMismatch, false,
		zip(func(a *testkit.Archive) {
			a.Members[0].Header.CompressedSize, a.Members[0].Header.UncompressedSize = 3, 3
		}))
	add("descriptor-size", "h", "the data descriptor declares another size than the directory", CodeDescriptorMismatch, false,
		zip(func(a *testkit.Archive) { a.Members[0].Descriptor.UncompressedSize = 6 },
			testkit.File{Name: "a.txt", Data: hello, Flags: format.FlagDataDescriptor}))
	// (i) Dane, na które nie wskazuje katalog.
	add("hidden-entry", "i", "a complete entry between two listed ones that the directory does not mention", CodeUnreferencedData, false,
		zip(func(a *testkit.Archive) { a.Central = append(a.Central[:1], a.Central[2:]...); a.FixDirectory() },
			plainFile, testkit.File{Name: "hidden.txt", Data: []byte("secret")}, testkit.File{Name: "c.txt", Data: hello}))
	add("prefix", "i", "a program before the first entry (self-extracting layout)", CodeUnreferencedData, false,
		zip(func(a *testkit.Archive) { a.Prefix = bytes.Repeat([]byte("MZ stub "), 16); a.Relayout() }))
	// (j) Nazwy.
	name := func(n string) []byte { return zip(nil, testkit.File{Name: n, Data: hello}) }
	add("name-traversal", "j", "the name ../../outside.txt", CodeNameTraversal, false, name("../../outside.txt"))
	add("name-absolute", "j", "the name /etc/passwd", CodeNameAbsolute, false, name("/etc/passwd"))
	add("name-drive", "j", "the name C:/Windows/win.ini", CodeNameAbsolute, false, name("C:/Windows/win.ini"))
	add("name-backslash", "j", "the name ..\\..\\outside.txt", CodeNameBackslash, false, name("..\\..\\outside.txt"))
	add("name-nul", "j", "the name safe.txt<NUL>.exe", CodeNameNUL, false, name("safe.txt\x00.exe"))
	add("name-duplicate", "j", "two entries named a.txt with different content", CodeNameDuplicate, false,
		zip(nil, plainFile, testkit.File{Name: "a.txt", Data: []byte("other")}))
	add("name-file-and-directory", "j", "a file a and an entry a/b", CodeNameDuplicate, false,
		zip(nil, testkit.File{Name: "a", Data: hello}, testkit.File{Name: "a/b", Data: hello}))
	// (k) ZIP64.
	add("zip64-size-past-file", "k", "the ZIP64 extra field of the directory declares 1 TiB of data, the local header 5 bytes", CodeLocalHeaderMismatch, false,
		zip(func(a *testkit.Archive) {
			x := testkit.Extra(format.ExtraZip64, testkit.U64(1<<40), testkit.U64(1<<40), testkit.U64(0))
			a.Central[0].SetExtra(x)
			a.FixDirectory()
		}, testkit.File{Name: "a.txt", Data: hello, Zip64: true}))
	add("zip64-disagrees", "k", "the ZIP64 end record declares another number of entries than the ordinary one", CodeZip64Inconsistent, false,
		func() []byte {
			a := testkit.Build(testkit.Zip64Records, plainFile)
			a.Zip64End.Entries, a.Zip64End.EntriesOnDisk = 2, 2
			return a.Bytes()
		}())
	add("zip64-locator-astray", "k", "the ZIP64 locator points into the data of the first entry", CodeZip64Inconsistent, false,
		func() []byte {
			a := testkit.Build(testkit.Zip64Sentinels, plainFile)
			a.Zip64Locator.Offset = 4
			return a.Bytes()
		}())
	// (l) Pola dodatkowe.
	add("extra-past-header", "l", "an extra field block declares more data than the extra field holds", CodeExtraField, false,
		zip(func(a *testkit.Archive) { a.Central[0].SetExtra([]byte{0x55, 0x54, 0xFF, 0xFF, 1}); a.FixDirectory() }))
	add("extra-length-past-directory", "l", "the extra field length of the last directory header runs past the directory", CodeDirectoryTruncated, false,
		zip(func(a *testkit.Archive) { a.Central[1].ExtraLength = 60000 }))
	// (m) Plik obcięty: trzy miejsca zapisane do korpusu; test sprawdza każde miejsce.
	whole := zip(nil)
	add("truncated-in-data", "m", "the file ends inside the data of the first entry", CodeNoDirectoryEnd, false, whole[:33])
	add("truncated-in-directory", "m", "the file ends inside the central directory", CodeNoDirectoryEnd, false, whole[:len(whole)-40])
	add("truncated-in-end", "m", "the file ends inside the end of central directory record", CodeNoDirectoryEnd, false, whole[:len(whole)-3])
	// (n) DEFLATE.
	deflate := func(id, what, code string, stream []byte) {
		add(id, "n", what, code, true, zip(nil, testkit.File{Name: "a.txt", Data: hello, Method: format.MethodDeflate, Raw: stream}))
	}
	good := testkit.StoredDeflate(hello)
	deflate("inflate-block-type", "a DEFLATE block of the reserved type", CodeInflateBlockType, []byte{0x07})
	deflate("inflate-stored-length", "a stored block whose NLEN is not the complement of LEN", CodeInflateStoredLength, []byte{0x01, 5, 0, 0, 0, 'h', 'e', 'l', 'l', 'o'})
	deflate("inflate-code-lengths", "a dynamic block that declares 287 literal/length codes", CodeInflateCodeLengths, []byte{0xF5, 0, 0, 0, 0, 0, 0, 0})
	deflate("inflate-distance", "a match that reaches before the start of the output", CodeInflateDistance, []byte{0x03, 0x02})
	deflate("inflate-truncated", "the compressed data end inside the stream", CodeInflateTruncated, good[:len(good)-2])
	deflate("inflate-trailing-data", "bytes after the end of the DEFLATE stream inside the entry", CodeInflateTrailingData, append(append([]byte(nil), good...), "smuggled"...))
	add("crc", "n", "the content does not have the declared CRC-32", CodeChecksum, true,
		zip(func(a *testkit.Archive) { a.Central[0].CRC32 ^= 1; a.Members[0].Header.CRC32 ^= 1 }))
	return out
}

// zeroStream zwraca strumień DEFLATE, który rozpakowuje się do 1 + 258·k bajtów zerowych (największa taka liczba
// nie większa niż n), i tę liczbę. Jeden blok z kodami dynamicznymi (RFC 1951, 3.2.7), w którym odwołanie
// <długość 258, odległość 1> zajmuje dwa bity: stosunek kompresji około 1032:1, największy możliwy w DEFLATE.
// Alfabet znaków: 285 (długość 258) kod „0”, znak 0 kod „10”, koniec bloku kod „11”; jedyny kod odległości „0”.
func zeroStream(n int) ([]byte, int) {
	var out []byte
	var acc uint64
	var nbits uint
	put := func(v uint64, k uint) { // k bitów, od najmniej znaczącego
		acc |= v << nbits
		nbits += k
		for nbits >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			nbits -= 8
		}
	}
	code := func(c uint64, k uint) { // kod Huffmana: od bitu najbardziej znaczącego
		for i := int(k) - 1; i >= 0; i-- {
			put(c>>uint(i)&1, 1)
		}
	}
	put(1, 1)       // BFINAL
	put(2, 2)       // BTYPE 10
	put(286-257, 5) // HLIT: symbole 0–285
	put(0, 5)       // HDIST: jeden kod odległości
	put(18-4, 4)    // HCLEN: 18 długości kodu długości kodów
	// Kod długości kodów: symbol 18 (ciąg zer) 1 bit, symbole 1 i 2 po 2 bity; kolejność z 3.2.7.
	for _, s := range []int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1} {
		switch s {
		case 18:
			put(1, 3)
		case 1, 2:
			put(2, 3)
		default:
			put(0, 3)
		}
	}
	// Kody: 18 → „0”, 1 → „10”, 2 → „11”. Długości: znak 0 ma 2, potem 255 zer, 256 ma 2, 28 zer, 285 ma 1,
	// kod odległości 0 ma 1.
	zerosRun := func(count uint64) { code(0, 1); put(count-11, 7) }
	code(3, 2)
	zerosRun(138)
	zerosRun(117)
	code(3, 2)
	zerosRun(28)
	code(2, 2)
	code(2, 2)
	// Dane: znak 0, potem same odwołania.
	code(2, 2)
	k := (n - 1) / 258
	for i := 0; i < k; i++ {
		put(0, 2)
	}
	code(3, 2) // koniec bloku
	if nbits > 0 {
		out = append(out, byte(acc))
	}
	return out, 1 + 258*k
}

// quotedOverlap buduje archiwum rodziny (d).
func quotedOverlap() []byte {
	stream, size := zeroStream(1 << 16)
	payload := make([]byte, size)
	b := testkit.Zip(testkit.File{Name: "b", Data: payload, Method: format.MethodDeflate, Raw: stream})
	memberB := b.Members[0].Bytes()
	headerB := memberB[:len(memberB)-len(stream)]
	// Dane wpisu A: blok niezakodowany (nieostatni) z nagłówkiem lokalnym B, potem strumień wspólny.
	quote := append([]byte{0x00, byte(len(headerB)), byte(len(headerB) >> 8), ^byte(len(headerB)), ^byte(len(headerB) >> 8)}, headerB...)
	contentA := append(append([]byte(nil), headerB...), payload...)
	a := testkit.Zip(testkit.File{Name: "a", Data: contentA, Method: format.MethodDeflate, Raw: append(quote, stream...)})
	// Wpis B w katalogu wskazuje nagłówek zacytowany wewnątrz danych A.
	hb := b.Central[0]
	hb.LocalOffset = uint32(30 + 1 + 5)
	a.Central = append(a.Central, hb)
	a.FixDirectory()
	return a.Bytes()
}

// verdictOf zwraca kod, którym czytnik odrzuca archiwum, i to, czy stało się to dopiero przy odczycie treści.
func verdictOf(data []byte, opts Options) (code string, atRead bool) {
	r, err := New(bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		return codeOrText(err), false
	}
	buf := make([]byte, 32<<10)
	for _, e := range r.Entries() {
		rc, err := r.Open(e)
		for err == nil {
			_, err = rc.Read(buf)
		}
		if err.Error() != "EOF" {
			return codeOrText(err), true
		}
	}
	return "", false
}

func codeOrText(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return "ERROR: " + err.Error()
}

// TestHostileArchives mierzy kryterium K6: każde archiwum korpusu wrogiego jest odrzucane oczekiwanym kodem,
// w czasie poniżej sekundy i z alokacjami poniżej 64 MiB (liczymy wszystkie bajty zaalokowane w czasie próby, co
// jest ostrzejsze niż szczyt pamięci). Plik obcięty w każdym miejscu też jest odrzucany. Manifest korpusu
// (testdata/hostile.json) trzyma sumy archiwów: budowa jest deterministyczna, więc inne narzędzia (tools/compare,
// tools/fuzz) dostają te same bajty z `go test ./zipreader -run TestHostileArchives -write-hostile KATALOG`.
// REQ: SAFE-003 SAFE-006 DET-002
func TestHostileArchives(t *testing.T) {
	archives := hostileArchives()
	families := map[string]bool{}
	seen := map[string]bool{}
	var slowest time.Duration
	var largest uint64
	for _, h := range archives {
		if seen[h.ID] {
			t.Errorf("%s: listed twice", h.ID)
		}
		seen[h.ID] = true
		families[h.Family] = true
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		code, atRead := verdictOf(h.data, Options{})
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		slowest, largest = max(slowest, elapsed), max(largest, allocated)
		if code != h.Code || atRead != h.AtRead {
			t.Errorf("%s: got %q (at read: %v), want %q (at read: %v)", h.ID, code, atRead, h.Code, h.AtRead)
		}
		if elapsed > time.Second || allocated > 64<<20 {
			t.Errorf("%s: %v and %d bytes allocated; the limits are 1 s and 64 MiB", h.ID, elapsed, allocated)
		}
		// Żadna opcja luzująca nie zamienia wrogiego archiwum w czytane bez śladu.
		// Wyjątki: dane wstępne i archiwum doklejone do archiwum, które z AllowPrefix są właśnie danymi
		// wstępnymi (użycie opcji widać w Relaxed).
		if code2, _ := verdictOf(h.data, Options{UnflaggedNames: UTF8, AllowPrefix: true}); code2 == "" && h.ID != "prefix" && h.ID != "concatenated" {
			t.Errorf("%s: accepted with relaxing options", h.ID)
		}
	}
	// Bomba jest poprawnym archiwum: bez limitu stosunku rozpakowuje się do 16 MiB zer z dobrym CRC-32.
	if code, _ := verdictOf(archives[0].data, Options{MaxRatio: -1}); archives[0].ID != "bomb-ratio" || code != "" {
		t.Errorf("the bomb read without the ratio limit gives %q", code)
	}
	for _, f := range "abcdefghijklmn" {
		if !families[string(f)] {
			t.Errorf("no archive of family (%c) of SAFE-003", f)
		}
	}
	// (m) Plik obcięty w każdym miejscu.
	whole := testkit.Zip(testkit.File{Name: "a.txt", Data: []byte("hello"), Flags: format.FlagDataDescriptor},
		testkit.File{Name: "b.txt", Data: []byte("world!"), Method: format.MethodDeflate, Comment: "c"}).Bytes()
	for cut := 0; cut < len(whole); cut++ {
		if code, _ := verdictOf(whole[:cut], Options{}); code == "" {
			t.Errorf("accepted the file cut at %d of %d bytes", cut, len(whole))
		}
	}
	t.Logf("hostile corpus: %d archives in %d families; slowest %v, most allocated %d KiB", len(archives), len(families), slowest, largest>>10)

	manifest, err := json.MarshalIndent(struct {
		Note     string           `json:"note"`
		Archives []hostileArchive `json:"archives"`
	}{"Hostile corpus of gozipgo (requirements/safety.md, SAFE-003). The archives are built by the test builder: " +
		"go test ./zipreader -run TestHostileArchives -write-hostile DIR writes them as DIR/<id>.zip. " +
		"code is the error code of the strict reader; at_read means New accepts the archive and reading an entry fails.", archives}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifest = append(manifest, '\n')
	path := filepath.Join("..", "testdata", "hostile.json")
	if *updateHostile {
		if err := os.WriteFile(path, manifest, 0o644); err != nil {
			t.Fatal(err)
		}
	} else if want, err := os.ReadFile(path); err != nil || !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), manifest) {
		t.Errorf("the hostile corpus differs from %s (%v); if the change is intended, run with -update-hostile", path, err)
	}
	if *writeHostile != "" {
		if err := os.MkdirAll(*writeHostile, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, h := range archives {
			if err := os.WriteFile(filepath.Join(*writeHostile, h.ID+".zip"), h.data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
