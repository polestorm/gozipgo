package zipreader

import (
	"archive/zip"
	"bytes"
	"hash/crc32"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/polestorm/gozipgo/internal/format"
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

// oracleSkipsZip64 rozpoznaje archiwa, których archive/zip nie przyjmuje, choć są poprawne: rozmiar katalogu
// w zwykłym rekordzie końca ma wartość graniczną, a liczba wpisów i przesunięcie katalogu nie. APPNOTE 4.4.1.4
// odsyła wtedy do rekordu ZIP64, ale archive/zip (zmierzone jako czarna skrzynka, Go 1.26.6) sięga po niego
// tylko przy wartości granicznej liczby wpisów albo przesunięcia i bierze rozmiar 0xFFFFFFFF dosłownie. Python,
// UnZip, 7-Zip i bsdtar czytają takie archiwum tak jak my (docs/differences.md; znalezisko z 2026-11-27).
func oracleSkipsZip64(data []byte) bool {
	end, err := findEnd(bytes.NewReader(data), int64(len(data)))
	return err == nil && end.dirSize == format.Sentinel32 && end.entries != format.Sentinel16 &&
		end.dirOffset != format.Sentinel32
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
		// Limity treści trzymają czas jednego wykonania w ryzach; są częścią każdego zestawu opcji.
		const entryLimit, totalLimit = 1 << 20, 4 << 20
		for _, opts := range []Options{{MaxEntrySize: entryLimit, MaxTotalSize: totalLimit},
			{UnflaggedNames: UTF8, FoldCase: true, AllowPrefix: true, AllowVersionHostByte: true, AllowUnusedZip64: true,
				MaxEntrySize: entryLimit, MaxTotalSize: totalLimit},
			{UnflaggedNames: CP437, MaxEntries: 3, MaxEntrySize: entryLimit, MaxTotalSize: totalLimit}} {
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
			strict := opts.UnflaggedNames == ASCIIOnly && !opts.AllowPrefix
			// Treść: każdy wpis czytamy do końca; błąd ma znany kod, a wydanych bajtów nie jest więcej, niż
			// deklaruje katalog. W trybie ścisłym archive/zip (czarna skrzynka) musi przyjąć archiwum z tą samą
			// listą nazw i dać te same bajty dla każdego wpisu, który przeczytaliśmy bez błędu (K5).
			var oracle *zip.Reader
			if strict {
				zr, zerr := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				switch {
				case zerr != nil && oracleSkipsZip64(data):
					// archive/zip odrzuca to archiwum, więc nie ma drugiego odczytania do porównania.
				case zerr != nil || len(zr.File) != len(r.Entries()):
					t.Fatalf("accepted strictly, archive/zip does not agree: %v", zerr)
				default:
					oracle = zr
				}
			}
			for i, e := range r.Entries() {
				if e.DataOffset < e.LocalHeaderOffset+30 || e.DataOffset+e.CompressedSize > uint64(len(data)) {
					t.Fatalf("entry %d: data outside the file: %+v", i, e)
				}
				if oracle != nil && oracle.File[i].Name != e.Name {
					t.Fatalf("entry %d: name %q, archive/zip has %q", i, e.Name, oracle.File[i].Name)
				}
				rc, err := r.Open(e)
				var body []byte
				if err == nil {
					body, err = io.ReadAll(rc)
				}
				if err != nil {
					x, ok := err.(*Error)
					if !ok || !codes[x.Code] || x.Entry != i {
						t.Fatalf("entry %d: bad content error %v", i, err)
					}
					if uint64(len(body)) > e.UncompressedSize {
						t.Fatalf("entry %d: %d bytes before the error, %d declared", i, len(body), e.UncompressedSize)
					}
					continue
				}
				if uint64(len(body)) != e.UncompressedSize || crc32.ChecksumIEEE(body) != e.CRC32 {
					t.Fatalf("entry %d: content accepted with another size or CRC-32", i)
				}
				if oracle != nil {
					zc, zerr := oracle.File[i].Open()
					var theirs []byte
					if zerr == nil {
						theirs, zerr = io.ReadAll(zc)
					}
					if zerr != nil || !bytes.Equal(theirs, body) {
						t.Fatalf("entry %d: content differs from archive/zip (%v)", i, zerr)
					}
				}
			}
			if opts.UnflaggedNames == ASCIIOnly && len(r.Relaxed()) != 0 && !opts.AllowPrefix {
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
