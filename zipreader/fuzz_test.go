package zipreader

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// knownCodes zwraca zbiór kodów błędów zdefiniowanych w errors.go.
func knownCodes(t testing.TB) map[string]bool {
	src, err := os.ReadFile("errors.go")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*Code\w+\s*=\s*"([a-z0-9-]+)"`).FindAllSubmatch(src, -1) {
		codes[string(m[1])] = true
	}
	return codes
}

// FuzzReader: dowolne wejście nie powoduje paniki; błąd jest typu *Error ze znanym kodem i przesunięciem w pliku;
// archiwum przyjęte ma nazwy poprawne i niekolidujące, indeksy kolejne, a w trybie ścisłym pustą listę poluzowań;
// wynik nie zależy od podziału odczytów. Od etapu 2 dochodzą niezmienniki treści (docs/fuzzing.md).
// Wejścia startowe: korpus zgodności, wszystkie archiwa inwentarza formatu z budowniczego i zestawy pobrane.
func FuzzReader(f *testing.F) {
	for _, data := range sampleArchives(f) {
		f.Add(data)
	}
	// Zestawy pobrane (gdy są): archiwa do 64 kB, żeby korpus startowy został mały.
	for _, data := range fetchedArchives(f) {
		if len(data) <= 64<<10 {
			f.Add(data)
		}
	}
	codes := knownCodes(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, opts := range []Options{{}, {UnflaggedNames: UTF8, FoldCase: true}, {UnflaggedNames: CP437, MaxEntries: 3}} {
			r, err := open(data, opts)
			if err != nil {
				e, ok := err.(*Error)
				if !ok {
					t.Fatalf("error %v is not an *Error", err)
				}
				if !codes[e.Code] || e.Offset < 0 || e.Offset > int64(len(data)) || e.Entry < -1 || e.Other < -1 {
					t.Fatalf("bad error %+v for %d bytes", e, len(data))
				}
				continue
			}
			if opts.UnflaggedNames == ASCIIOnly && len(r.Relaxed()) != 0 {
				t.Fatalf("strict reading reports relaxations %v", r.Relaxed())
			}
			if opts.MaxEntries > 0 && len(r.Entries()) > opts.MaxEntries {
				t.Fatalf("%d entries over the limit of %d", len(r.Entries()), opts.MaxEntries)
			}
			seen := map[string]int{}
			for i, e := range r.Entries() {
				n := e.Name
				if e.Index != i || n == "" || !utf8.ValidString(n) || strings.ContainsAny(n, "\\\x00") || n[0] == '/' ||
					e.IsDir != strings.HasSuffix(n, "/") || e.IsDir && (e.UncompressedSize != 0 || e.CompressedSize != 0) {
					t.Fatalf("accepted entry %d breaks the rules: %+v", i, e)
				}
				for _, seg := range strings.Split(strings.TrimSuffix(n, "/"), "/") {
					if seg == "" || seg == "." || seg == ".." {
						t.Fatalf("accepted name %q has a bad segment", n)
					}
				}
				// Klucz kolizji: ścieżka bez końcowego ukośnika; każdy jej początek będący plikiem to też kolizja.
				key := strings.TrimSuffix(n, "/")
				if opts.FoldCase {
					key = asciiLower(key)
				}
				if j, dup := seen[key]; dup {
					t.Fatalf("accepted entries %d and %d have the same path %q", j, i, key)
				}
				seen[key] = i
			}
			for key := range seen {
				for p := strings.LastIndexByte(key, '/'); p >= 0; p = strings.LastIndexByte(key[:p], '/') {
					if j, ok := seen[key[:p]]; ok && !r.Entries()[j].IsDir {
						t.Fatalf("accepted entry %q lies under the file %q", key, key[:p])
					}
				}
			}
			if len(data) <= 4096 {
				if got, want := dump(New(oneByteReader{data}, int64(len(data)), opts)), dump(r, nil); got != want {
					t.Fatalf("one byte per read gives a different result")
				}
			}
		}
	})
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
