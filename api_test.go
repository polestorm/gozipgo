package gozipgo

import (
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/polestorm/gozipgo/internal/testkit"
)

var update = flag.Bool("update", false, "rewrite the API snapshots api/*.txt")

const modulePath = "github.com/polestorm/gozipgo"

// publicPackages to pakiety biblioteki z publicznym API; każdy ma wzorzec api/<pakiet>.txt. Nowy pakiet dopisuje
// się tutaj.
var publicPackages = []string{"zipreader", "zipwriter"}

// libraryPackages to wszystkie pakiety biblioteki (bez narzędzi testów): ich importy pilnuje TestNoIOImports.
var libraryPackages = []string{".", "zipreader", "zipwriter", "internal/format", "internal/inflate", "internal/deflate", "internal/entryname"}

// allowedImports to pakiety biblioteki standardowej, które wolno importować kodowi biblioteki: żaden nie ma
// wejścia ani wyjścia poza interfejsami io. Nowy pakiet na liście wymaga decyzji (CLAUDE.md, „Zasady”).
var allowedImports = map[string]bool{
	"encoding/binary": true, "errors": true, "hash/crc32": true, "io": true, "math/bits": true, "sort": true,
	"strconv": true, "strings": true, "sync": true, "sync/atomic": true, "unicode/utf8": true,
}

// TestAPISnapshot mierzy kryterium K4: publiczne API każdego pakietu jest równe wzorcowi api/<pakiet>.txt.
// Zmiana API wymaga `go test . -run TestAPISnapshot -update`, przeglądu różnicy i wpisu w ADR albo CHANGELOG.md.
// REQ: API-001
func TestAPISnapshot(t *testing.T) {
	for _, pkg := range publicPackages {
		got, err := testkit.APISnapshot(pkg, modulePath+"/"+pkg)
		if err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("api", pkg+".txt")
		if *update {
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Errorf("%s: %v (run with -update)", pkg, err)
			continue
		}
		if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
			t.Errorf("public API of %s differs from %s; if the change is intended, run with -update and record it", pkg, golden)
		}
	}
}

// TestErrorCatalog: każdy kod błędu z kodu źródłowego jest w docs/errors.md i odwrotnie, a każda stała kodu jest
// użyta w co najmniej jednym teście swojego pakietu albo jej kod stoi w werdykcie inwentarza formatu, który test sprawdza.
// REQ: API-003 API-004
func TestErrorCatalog(t *testing.T) {
	constRe := regexp.MustCompile(`(?m)^\s*(Code\w+)\s*=\s*"([a-z0-9-]+)"`)
	rowRe := regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| `(Code\\w+)` \\|")
	read := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	inventory := read("requirements/format.md")
	inCode := map[string]string{} // kod → stała
	seenIn := map[string]string{} // kod → pakiet, który go zdefiniował jako ostatni
	for _, pkg := range publicPackages {
		files, _ := filepath.Glob(filepath.Join(pkg, "*.go"))
		tests := ""
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				tests += read(f)
			}
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			for _, m := range constRe.FindAllStringSubmatch(read(f), -1) {
				name, code := m[1], m[2]
				// Ten sam kod w dwóch pakietach to ta sama stała: pisarz odmawia nazw, które czytnik odrzuca.
				if other, dup := inCode[code]; dup && (other != name || seenIn[code] == pkg) {
					t.Errorf("code %s is defined twice", code)
				}
				inCode[code], seenIn[code] = name, pkg
				used := regexp.MustCompile(`\b`+name+`\b`).MatchString(tests) ||
					strings.Contains(inventory, "`reject "+code+"` | 1 |") || strings.Contains(inventory, "`entry "+code+"` | 1 |")
				if !used {
					t.Errorf("no test of %s raises %s (%s)", pkg, code, name)
				}
			}
		}
	}
	inDoc := map[string]bool{}
	for _, m := range rowRe.FindAllStringSubmatch(read("docs/errors.md"), -1) {
		if inDoc[m[1]] {
			t.Errorf("docs/errors.md lists %s twice", m[1])
		}
		inDoc[m[1]] = true
		if inCode[m[1]] != "" && inCode[m[1]] != m[2] {
			t.Errorf("docs/errors.md gives %s the constant %s, the code has %s", m[1], m[2], inCode[m[1]])
		}
	}
	var missing, extra []string
	for c := range inCode {
		if !inDoc[c] {
			missing = append(missing, c)
		}
	}
	for c := range inDoc {
		if inCode[c] == "" {
			extra = append(extra, c)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("codes missing from docs/errors.md: %s", strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		t.Errorf("codes in docs/errors.md that the code does not define: %s", strings.Join(extra, ", "))
	}
	t.Logf("%d error codes", len(inCode))
}

// TestExportedDocumented: każdy eksportowany identyfikator pakietu publicznego ma komentarz dokumentacyjny,
// a pakiet ma przykład Example* (sprawdzany przez go test).
// REQ: API-005
func TestExportedDocumented(t *testing.T) {
	for _, pkg := range publicPackages {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, pkg, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		examples := 0
		for _, p := range pkgs {
			for name, f := range p.Files {
				if strings.HasSuffix(name, "_test.go") {
					examples += strings.Count(readFile(t, name), "\nfunc Example")
					continue
				}
				for _, msg := range undocumented(f) {
					t.Errorf("%s: %s has no doc comment", fset.Position(f.Pos()).Filename, msg)
				}
			}
		}
		if examples == 0 {
			t.Errorf("package %s has no Example", pkg)
		}
	}
}

func readFile(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestNoDependencies: moduł główny nie ma sekcji require ani replace (CGO_ENABLED=0 i najniższą wersję Go
// sprawdza CI).
// REQ: API-006
func TestNoDependencies(t *testing.T) {
	for _, line := range strings.Split(readFile(t, "go.mod"), "\n") {
		if f := strings.Fields(line); len(f) > 0 && (f[0] == "require" || f[0] == "replace") {
			t.Errorf("go.mod: %q; the library module has no dependencies", line)
		}
	}
}

// TestNoIOImports mierzy część kryterium K6: pakiety biblioteki importują tylko pakiety z listy allowedImports
// i własne pakiety modułu; w szczególności nie importują os, net, archive/zip, compress/flate, unsafe ani fmt
// (które wciąga os). Pliki testów nie są sprawdzane: tam biblioteka standardowa jest wyrocznią.
// REQ: SAFE-001
func TestNoIOImports(t *testing.T) {
	checked := 0
	for _, pkg := range libraryPackages {
		files, _ := filepath.Glob(filepath.Join(pkg, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			for _, imp := range parsed.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if path == modulePath+"/internal/testkit" || !allowedImports[path] && !strings.HasPrefix(path, modulePath+"/") {
					t.Errorf("%s imports %s, which library code may not import", f, path)
				}
			}
		}
	}
	if checked < 5 {
		t.Errorf("only %d library files checked", checked)
	}
	// Lista pakietów biblioteki musi być pełna: każdy katalog z kodem Go poza narzędziami testów jest na niej.
	known := map[string]bool{"internal/testkit": true}
	for _, p := range libraryPackages {
		known[p] = true
	}
	filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p == "tools" || p == "testdata" || p == "out" || p == "ref" || strings.HasPrefix(d.Name(), ".") && p != "." {
			return filepath.SkipDir
		}
		if files, _ := filepath.Glob(filepath.Join(p, "*.go")); len(files) > 0 && !known[filepath.ToSlash(p)] {
			t.Errorf("package %s is not in libraryPackages", p)
		}
		return nil
	})
}

// TestAPIOnlyGrows: API czytnika wydane w v0.1.1 (api/baseline/zipreader-v0.1.1.txt, wzorzec z tagu) jest nadal
// częścią bieżącego wzorca: żadna stała, pole, typ, funkcja ani metoda nie zniknęły i nie zmieniły zapisu. Zmiana
// niezgodna wymaga ADR i nowego wzorca odniesienia. Odstępy są pomijane, bo nowe pola zmieniają wyrównanie.
// REQ: API-002
func TestAPIOnlyGrows(t *testing.T) {
	lines := func(path string) map[string]bool {
		out := map[string]bool{}
		for _, l := range strings.Split(strings.ReplaceAll(readFile(t, path), "\r\n", "\n"), "\n") {
			if l = strings.Join(strings.Fields(l), " "); l != "" {
				out[l] = true
			}
		}
		return out
	}
	now := lines(filepath.Join("api", "zipreader.txt"))
	var gone []string
	for l := range lines(filepath.Join("api", "baseline", "zipreader-v0.1.1.txt")) {
		if !now[l] {
			gone = append(gone, l)
		}
	}
	sort.Strings(gone)
	for _, l := range gone {
		t.Errorf("released in v0.1.1 and no longer in the API: %s", l)
	}
}

// TestStrictVerdictsRatchet: werdykt trybu ścisłego dla każdego pliku zestawów pobranych jest taki jak w v0.1.1
// (testdata/verdicts/strict-v0.1.1.tsv), chyba że zmiana jest zapisana w testdata/verdicts/changes.tsv z wersją,
// w której CHANGELOG.md ją opisuje. Domyślne zachowanie wobec archiwum nie zmienia się po cichu.
// REQ: API-007
func TestStrictVerdictsRatchet(t *testing.T) {
	table := func(path string, cols int) map[string][]string {
		out := map[string][]string{}
		for _, l := range strings.Split(strings.ReplaceAll(readFile(t, path), "\r\n", "\n"), "\n") {
			if f := strings.Split(l, "\t"); l != "" && !strings.HasPrefix(l, "#") && len(f) >= cols {
				out[f[0]] = f[1:]
			}
		}
		return out
	}
	base := table(filepath.Join("testdata", "verdicts", "strict-v0.1.1.tsv"), 2)
	now := table(filepath.Join("testdata", "verdicts", "fetched.tsv"), 2)
	changes := table(filepath.Join("testdata", "verdicts", "changes.tsv"), 4)
	changelog := readFile(t, "CHANGELOG.md")
	if len(base) < 500 {
		t.Fatalf("only %d baseline verdicts", len(base))
	}
	for path, was := range base {
		is, ok := now[path]
		if !ok {
			continue // plik wypadł z zestawu pobieranego: zmiana danych, nie zachowania
		}
		if is[0] == was[0] {
			continue
		}
		c, recorded := changes[path]
		switch {
		case !recorded || c[0] != was[0] || c[1] != is[0]:
			t.Errorf("%s: strict verdict was %q in v0.1.1 and is %q; record the change in testdata/verdicts/changes.tsv and in CHANGELOG.md", path, was[0], is[0])
		case !strings.Contains(changelog, "## "+c[2]) && c[2] != "Unreleased":
			t.Errorf("%s: changes.tsv names %q, which CHANGELOG.md does not have", path, c[2])
		}
	}
}

// TestNoPrivatePaths: pliki repozytorium nie zawierają ścieżek katalogów domowych, czyli śladu maszyny, na której
// powstały (dane prywatne nie trafiają do repozytorium). Pobrane dane i katalogi robocze są poza repozytorium.
// REQ: SAFE-008
func TestNoPrivatePaths(t *testing.T) {
	home := regexp.MustCompile(`/(Users|home)/[A-Za-z][A-Za-z0-9._-]+/|[A-Z]:\\Users\\[A-Za-z]`)
	allowed := regexp.MustCompile(`/home/runner/|/Users/runner/|/home/user/|/Users/me/`)
	checked := 0
	filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p == "out" || p == "ref" || p == filepath.Join("testdata", "fetched") || strings.HasPrefix(d.Name(), ".") && p != "." {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		checked++
		for _, m := range home.FindAll(b, -1) {
			if !allowed.Match(m) {
				t.Errorf("%s holds a home directory path: %s", p, m)
			}
		}
		return nil
	})
	if checked < 100 {
		t.Errorf("only %d files checked", checked)
	}
}
