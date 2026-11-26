package zipreader

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateVerdicts = flag.Bool("update", false, "rewrite testdata/verdicts/fetched.tsv")

// fetchedArchives zwraca pliki zestawów pobranych (testdata/sources.json, tools/fetch), które wyglądają na
// archiwa ZIP: zaczynają się od „PK” albo mają gdziekolwiek sygnaturę końca katalogu. Klucz to ścieżka względem
// testdata/fetched. Gdy zestawów nie ma, zwraca nil.
func fetchedArchives(t testing.TB) map[string][]byte {
	root := filepath.Join("..", "testdata", "fetched")
	out := map[string][]byte{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.HasPrefix(b, []byte("PK")) || bytes.Contains(b, []byte("PK\x05\x06")) {
			rel, _ := filepath.Rel(root, p)
			out[filepath.ToSlash(rel)] = b
		}
		return nil
	})
	if len(out) == 0 {
		return nil
	}
	return out
}

// TestFetchedArchives przepuszcza przez czytnik archiwa z zestawów pobranych: zbiory testowe innych programów
// (yauzl, libzip, rc-zip, Go, CPython), archiwa-ciekawostki, bombę o nakładających się wpisach i dokumenty
// Worda. Sprawdza, że nic nie powoduje paniki, każdy błąd ma stały kod, a archiwum przyjęte ściśle ma w archive/zip
// (czarna skrzynka) tę samą listę nazw. Werdykt każdego pliku (kod w trybie ścisłym, kod z UnflaggedNames: UTF8 i AllowPrefix
// i wynik archive/zip) jest zapisany we wzorcu testdata/verdicts/fetched.tsv: zmiana werdyktu wymaga -update
// i przeglądu różnicy (API-007). Zestawy służą rozwojowi; liczby do publikacji bierzemy tylko z zestawów, których
// źródło i sumę podaje testdata/sources.json. Test jest pomijany, gdy zestawów nie pobrano.
func TestFetchedArchives(t *testing.T) {
	archives := fetchedArchives(t)
	if archives == nil {
		t.Skip("no fetched sets: run go -C tools/fetch run . -root ../..")
	}
	var paths []string
	for p := range archives {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	verdict := func(data []byte, opts Options) (r *Reader, v string) {
		defer func() {
			if x := recover(); x != nil {
				r, v = nil, fmt.Sprint("PANIC: ", x)
			}
		}()
		r, err := open(data, opts)
		if err == nil {
			unsupported := map[string]bool{}
			for _, e := range r.Entries() {
				if e.Unsupported != "" {
					unsupported[e.Unsupported] = true
				}
			}
			v = "ok"
			for _, code := range []string{CodeUnsupportedEncryption, CodeUnsupportedPatch, CodeUnsupportedMethod, CodeUnsupportedVersion} {
				if unsupported[code] {
					v += ", entries with " + code
				}
			}
			// Treść: każdy wpis w zakresie czytamy do końca; pierwszy kod błędu trafia do werdyktu.
			buf := make([]byte, 32<<10)
			for _, e := range r.Entries() {
				if e.Unsupported != "" {
					continue
				}
				rc, err := r.Open(e)
				for err == nil {
					_, err = rc.Read(buf)
				}
				if err != io.EOF {
					if codeOf(err) == "" {
						return r, "ERROR: " + err.Error()
					}
					v += ", content: " + codeOf(err)
					break
				}
			}
			return r, v
		}
		if e, ok := err.(*Error); ok {
			return nil, e.Code
		}
		return nil, "ERROR: " + err.Error()
	}
	var lines []string
	counts := map[string]int{}
	for _, p := range paths {
		data := archives[p]
		r, strict := verdict(data, Options{})
		_, relaxed := verdict(data, Options{UnflaggedNames: UTF8, AllowPrefix: true})
		for _, v := range []string{strict, relaxed} {
			if strings.HasPrefix(v, "PANIC") || strings.HasPrefix(v, "ERROR") {
				t.Errorf("%s: %s", p, v)
			}
		}
		oracle := "rejects"
		if zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
			oracle = "accepts"
			if r != nil {
				same := len(zr.File) == len(r.Entries())
				for i := 0; same && i < len(zr.File); i++ {
					same = zr.File[i].Name == r.Entries()[i].Name
				}
				if !same {
					oracle = "accepts, other names"
					t.Errorf("%s: accepted strictly, but archive/zip lists other names", p)
				}
				// Treść, którą obie strony czytają bez błędu, musi być ta sama.
				for i := 0; same && strict == "ok" && i < len(zr.File); i++ {
					e := r.Entries()[i]
					if e.Unsupported != "" {
						continue
					}
					ours, err1 := readEntry(r, e)
					var theirs []byte
					rc, err2 := zr.File[i].Open()
					if err2 == nil {
						theirs, err2 = io.ReadAll(rc)
					}
					if err1 != nil || err2 != nil || !bytes.Equal(ours, theirs) {
						oracle = "accepts, other content"
						t.Errorf("%s: entry %d: content differs from archive/zip (%v, %v)", p, i, err1, err2)
					}
				}
			}
		}
		counts[strict]++
		lines = append(lines, p+"\t"+strict+"\t"+relaxed+"\t"+oracle)
	}
	golden := filepath.Join("..", "testdata", "verdicts", "fetched.tsv")
	text := "# path under testdata/fetched\tstrict\twith UnflaggedNames: UTF8 and AllowPrefix\tarchive/zip of the Go that wrote this file\n" +
		strings.Join(lines, "\n") + "\n"
	if *updateVerdicts {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run with -update)", err)
		}
		// Kolumna archive/zip zależy od wersji Go, więc porównujemy tylko werdykty czytnika.
		ours := func(s string) string {
			var b strings.Builder
			for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
				if f := strings.Split(l, "\t"); len(f) == 4 && !strings.HasPrefix(l, "#") {
					b.WriteString(f[0] + "\t" + f[1] + "\t" + f[2] + "\n")
				}
			}
			return b.String()
		}
		if got, w := ours(text), ours(string(want)); got != w {
			gl, wl := strings.Split(got, "\n"), strings.Split(w, "\n")
			shown := 0
			for i := 0; i < len(gl) && shown < 10; i++ {
				if i >= len(wl) || gl[i] != wl[i] {
					t.Errorf("verdict changed: got %q", gl[i])
					shown++
				}
			}
			t.Errorf("verdicts differ from %s; if the change is intended, run with -update and review the diff", golden)
		}
	}
	var summary []string
	for code, n := range counts {
		summary = append(summary, fmt.Sprintf("%s %d", code, n))
	}
	sort.Strings(summary)
	t.Logf("fetched archives: %d files; strict verdicts: %s", len(paths), strings.Join(summary, ", "))
}

// readEntry czyta treść wpisu do końca.
func readEntry(r *Reader, e Entry) ([]byte, error) {
	rc, err := r.Open(e)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
