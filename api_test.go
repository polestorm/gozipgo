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
