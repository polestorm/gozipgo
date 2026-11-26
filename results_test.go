package gozipgo

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// rawRows czyta surowy wynik narzędzia: wiersze TSV bez komentarzy i nagłówka.
func rawRows(t *testing.T, path string) [][]string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for i, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		rows = append(rows, strings.Split(l, "\t"))
		_ = i
	}
	if len(rows) < 2 {
		t.Fatalf("%s holds no rows", path)
	}
	return rows[1:]
}

// TestCompareRecorded sprawdza zapisany wynik tools/compare (K3): gozipgo czyta każde archiwum korpusu zgodności
// zgodnie z manifestem i odrzuca każde archiwum korpusu wrogiego; liczba archiwów zgadza się z manifestami.
// Liczby w docs/comparison.md pochodzą z tego samego pliku. Pomiar powtarza `go -C tools/compare run . -root ../..`.
// REQ: DET-007
func TestCompareRecorded(t *testing.T) {
	interop, hostile := 0, 0
	for _, r := range rawRows(t, "docs/results/compare.tsv") {
		if len(r) < 9 || r[2] != "gozipgo" {
			continue
		}
		switch r[0] {
		case "interop":
			interop++
			if r[8] != "true" {
				t.Errorf("conformance corpus: %s: %s %s", r[1], r[3], r[4])
			}
		case "hostile":
			hostile++
			if r[3] != "rejects" {
				t.Errorf("hostile corpus: %s: %s", r[1], r[3])
			}
		}
	}
	count := func(path, key string) int {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), key)
	}
	if want := count("testdata/interop/manifest.json", `"file":`); interop != want {
		t.Errorf("the recorded comparison has %d conformance archives, the manifest %d: run tools/compare again", interop, want)
	}
	if want := count("testdata/hostile.json", `"id":`); hostile != want {
		t.Errorf("the recorded comparison has %d hostile archives, the manifest %d: run tools/compare again", hostile, want)
	}
}

// TestBenchRecorded sprawdza zapisany wynik tools/bench (K7): dla każdego przypadku odczytu treści mediana czasu
// gozipgo jest najwyżej 1,5 raza większa niż mediana archive/zip na tych samych danych. Pomiar powtarza
// `go -C tools/bench run . -root ../..`; test nie mierzy niczego sam, więc nie zależy od obciążenia maszyny.
// REQ: DET-007
func TestBenchRecorded(t *testing.T) {
	times := map[string]map[string][]float64{}
	for _, r := range rawRows(t, "docs/results/bench.tsv") {
		if len(r) < 6 {
			continue
		}
		ns, err := strconv.ParseFloat(r[3], 64)
		if err != nil {
			t.Fatal(err)
		}
		if times[r[0]] == nil {
			times[r[0]] = map[string][]float64{}
		}
		times[r[0]][r[1]] = append(times[r[0]][r[1]], ns)
	}
	median := func(v []float64) float64 {
		sort.Float64s(v)
		return v[len(v)/2]
	}
	content := 0
	for label, byReader := range times {
		ours, theirs := byReader["gozipgo"], byReader["archive/zip"]
		if len(ours) == 0 || len(theirs) == 0 {
			t.Errorf("%s: a reader is missing", label)
			continue
		}
		if strings.HasPrefix(label, "open an archive") {
			continue // otwarcie archiwum: gozipgo czyta nagłówki lokalne, archive/zip nie; K7 mówi o rozpakowaniu
		}
		content++
		if ratio := median(ours) / median(theirs); ratio > 1.5 {
			t.Errorf("%s: gozipgo takes %.2f times the time of archive/zip; K7 allows 1.5", label, ratio)
		}
	}
	if content < 5 {
		t.Errorf("only %d cases of reading content recorded", content)
	}
}
