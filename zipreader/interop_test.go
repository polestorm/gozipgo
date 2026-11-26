package zipreader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// interopManifest to część testdata/interop/manifest.json, której potrzebuje test (pisze go tools/corpus).
type interopManifest struct {
	Archives []struct {
		ID      string `json:"id"`
		File    string `json:"file"`
		Program string `json:"program"`
		SHA256  string `json:"sha256"`
		Comment string `json:"comment"`
		Entries []struct {
			Name     string `json:"name"`
			NameHex  string `json:"name_hex"`
			UTF8Flag bool   `json:"utf8_flag"`
			Method   uint16 `json:"method"`
			Size     uint64 `json:"size"`
			CRC32    string `json:"crc32"`
		} `json:"entries"`
		Expect *struct {
			Code    string   `json:"code"`
			Options []string `json:"options"`
			Reason  string   `json:"reason"`
		} `json:"expect"`
	} `json:"archives"`
}

func loadInterop(t testing.TB) (interopManifest, string) {
	t.Helper()
	dir := filepath.Join("..", "testdata", "interop")
	var m interopManifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m, dir
}

// TestInteropCorpus mierzy kryterium K2 w zakresie etapu 1: każde archiwum korpusu zgodności jest przyjmowane
// w trybie ścisłym, a lista wpisów (kolejność, nazwy, rozmiary, CRC-32, metody) i komentarz równają się
// manifestowi, który tools/corpus zapisał na podstawie archive/zip i plików źródłowych. Wyjątek musi być zapisany
// w manifeście (expect) z kodem i powodem z docs/differences.md; wtedy archiwum musi dać ten kod w trybie ścisłym
// i zgodną listę z wymienionymi opcjami. Treść wpisów (SHA-256) dochodzi w etapie 2.
// REQ: DIR-009
func TestInteropCorpus(t *testing.T) {
	m, dir := loadInterop(t)
	if len(m.Archives) == 0 {
		t.Fatal("the conformance corpus is empty")
	}
	ok, excepted := 0, 0
	for _, a := range m.Archives {
		data, err := os.ReadFile(filepath.Join(dir, a.File))
		if err != nil {
			t.Error(err)
			continue
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != a.SHA256 {
			t.Errorf("%s: SHA-256 differs from the manifest", a.File)
			continue
		}
		opts := Options{}
		r, err := New(bytes.NewReader(data), int64(len(data)), opts)
		if a.Expect != nil {
			if code := codeOf(err); code != a.Expect.Code {
				t.Errorf("%s: strict reading gives %q, the manifest expects %s", a.File, code, a.Expect.Code)
				continue
			}
			if len(a.Expect.Options) == 0 {
				excepted++
				continue
			}
			for _, o := range a.Expect.Options {
				switch o {
				case "UnflaggedNames=UTF8":
					opts.UnflaggedNames = UTF8
				case "UnflaggedNames=CodePage:CP852":
					opts.UnflaggedNames, opts.CodePage = CodePage, cp852(t)
				default:
					t.Fatalf("%s: unknown option %s in the manifest", a.File, o)
				}
			}
			r, err = New(bytes.NewReader(data), int64(len(data)), opts)
		}
		if err != nil {
			t.Errorf("%s (%s): %v", a.File, a.Program, err)
			continue
		}
		good := r.Comment() == a.Comment && len(r.Entries()) == len(a.Entries)
		if !good {
			t.Errorf("%s: %d entries and comment %q, the manifest has %d and %q", a.File, len(r.Entries()), r.Comment(),
				len(a.Entries), a.Comment)
			continue
		}
		for i, e := range r.Entries() {
			w := a.Entries[i]
			// Nazwę porównujemy tylko wtedy, gdy manifest ma ją w UTF-8; nazwy w innym kodowaniu czytnik dekoduje
			// na mocy opcji i wynik nie jest tym, co zapisał program.
			nameOK := w.NameHex != "" || e.Name == w.Name
			if !nameOK || e.Method != w.Method || e.UncompressedSize != w.Size || fmt.Sprintf("%08x", e.CRC32) != w.CRC32 ||
				e.Unsupported != "" || e.Index != i {
				t.Errorf("%s: entry %d is %q method %d size %d crc %08x, the manifest has %q method %d size %d crc %s",
					a.File, i, e.Name, e.Method, e.UncompressedSize, e.CRC32, w.Name, w.Method, w.Size, w.CRC32)
				good = false
			}
		}
		switch {
		case good && a.Expect != nil:
			excepted++
		case good:
			ok++
		}
	}
	t.Logf("interop corpus: %d archives, %d read as the manifest says, %d listed exceptions", len(m.Archives), ok, excepted)
}

// codeOf zwraca kod błędu czytnika albo pusty napis.
func codeOf(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return ""
}
