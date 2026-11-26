package gozipgo

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// prefixRow to wiersz tabeli prefiksów w requirements/README.md, np. "| `XML` | `xml.md` | ...".
var prefixRow = regexp.MustCompile("^\\| `([A-Z]{2,5})` \\|")

// anyID to wszystko, co wygląda jak identyfikator wymagania; w wierszu „REQ:” każde takie słowo musi być
// zdefiniowane, także gdy jego prefiksu nie ma w tabeli (np. identyfikator przeniesiony z innego projektu).
var anyID = regexp.MustCompile(`\b[A-Z]{2,5}-\d{3}\b`)

// TestRequirementsCovered mierzy kryterium K4: każde przyjęte wymaganie z requirements/ ma test, a każde odwołanie „REQ:” wskazuje istniejące wymaganie (przyjęte albo szkic). Wiersz „Status: szkic”
// albo „Status: przyjęte” w pliku wymagań dotyczy tabel pod nim, do następnego takiego wiersza
// (requirements/README.md). Przeniesiony z goxmlgo (requirements_test.go).
// REQ: API-001
func TestRequirementsCovered(t *testing.T) {
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	prefixes := map[string]bool{}
	for _, line := range strings.Split(read("requirements/README.md"), "\n") {
		if m := prefixRow.FindStringSubmatch(line); m != nil {
			prefixes[m[1]] = true
		}
	}
	if len(prefixes) == 0 {
		t.Fatal("no prefixes in requirements/README.md")
	}

	accepted := map[string]bool{} // identyfikator → czy przyjęty
	docs, _ := filepath.Glob("requirements/*.md")
	for _, doc := range docs {
		if filepath.Base(doc) == "README.md" {
			continue
		}
		status := ""
		for _, line := range strings.Split(read(doc), "\n") {
			if s, ok := strings.CutPrefix(line, "Status: "); ok {
				status = strings.TrimSpace(s)
				if status != "szkic" && status != "przyjęte" {
					t.Errorf("%s: unknown status %q", doc, status)
				}
				continue
			}
			id := anyID.FindString(line)
			if id == "" || !strings.HasPrefix(line, "| "+id+" |") {
				continue
			}
			if !prefixes[id[:strings.IndexByte(id, '-')]] {
				t.Errorf("%s: %s has a prefix that is not in requirements/README.md", doc, id)
			}
			if status == "" {
				t.Errorf("%s: %s is defined before any Status line", doc, id)
			}
			if _, dup := accepted[id]; dup {
				t.Errorf("requirement %s is defined twice", id)
			}
			accepted[id] = status == "przyjęte"
		}
	}

	var sources []string
	filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// pobrane dane i katalogi robocze nie są źródłem odwołań
			// testy tools/evidence mają własne, zmyślone wymagania w danych testowych
			if p == filepath.Join("testdata", "fetched") || p == filepath.Join("tools", "evidence") || p == "out" || p == "ref" || (strings.HasPrefix(d.Name(), ".") && p != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			sources = append(sources, p)
		}
		return nil
	})
	used := map[string]bool{}
	for _, f := range sources {
		for _, line := range strings.Split(read(f), "\n") {
			i := strings.Index(line, "REQ:")
			if i < 0 || f == "requirements_test.go" && !strings.Contains(line, "// REQ:") {
				continue
			}
			for _, id := range anyID.FindAllString(line[i:], -1) {
				used[id] = true
				if _, ok := accepted[id]; !ok {
					t.Errorf("%s refers to unknown requirement %s", f, id)
				}
			}
		}
	}
	var ids []string
	for id := range accepted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nAccepted, nDraft := 0, 0
	for _, id := range ids {
		if !accepted[id] {
			nDraft++
			continue
		}
		nAccepted++
		if !used[id] {
			t.Errorf("accepted requirement %s has no test", id)
		}
	}
	t.Logf("requirements: %d accepted, %d draft; %d referenced by tests or corpus files", nAccepted, nDraft, len(used))
}
