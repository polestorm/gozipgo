package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Requirement to wiersz tabeli wymagań z requirements/*.md. Status to wiersz „Status:” obowiązujący nad tabelą:
// „przyjęte” albo „szkic” (requirements/README.md).
type Requirement struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Text   string `json:"text"`
	Status string `json:"status"`
}

// Accepted mówi, czy wymaganie jest przyjęte, czyli czy musi mieć test (K4).
func (r Requirement) Accepted() bool { return r.Status == statusAccepted }

const (
	statusAccepted = "przyjęte"
	statusDraft    = "szkic"
)

// Ref to odwołanie „REQ:” w teście albo w pliku korpusu. Test jest pusty, gdy odwołania nie da się przypisać
// do funkcji testowej (np. komentarz przy funkcji pomocniczej).
type Ref struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	Package string `json:"package,omitempty"`
	Test    string `json:"test,omitempty"`
	Corpus  bool   `json:"corpus,omitempty"`
}

// Key to klucz wyniku testu: ścieżka importu pakietu i nazwa testu najwyższego poziomu.
func (r Ref) Key() string { return r.Package + "." + r.Test }

// prefixRow to wiersz tabeli prefiksów w requirements/README.md, np. "| `DIR` | `directory.md` | ...".
var prefixRow = regexp.MustCompile("^\\| `([A-Z]{2,5})` \\|")

// anyID to wszystko, co wygląda jak identyfikator wymagania; w wierszu „REQ:” każde takie słowo musi być
// zdefiniowane, także gdy jego prefiksu nie ma w tabeli. Te same reguły ma TestRequirementsCovered w module głównym.
var anyID = regexp.MustCompile(`\b[A-Z]{2,5}-\d{3}\b`)

// parseRequirements czyta prefiksy z tabeli w requirements/README.md i wymagania ze wszystkich pozostałych plików
// requirements/*.md, w kolejności plików i wierszy. Wiersz „Status: szkic” albo „Status: przyjęte” dotyczy tabel
// pod nim, do następnego takiego wiersza. Niespójności (nieznany status, wymaganie przed wierszem statusu, prefiks
// spoza README, powtórzony identyfikator) wracają jako lista problemów: pakiet powstaje także wtedy, a K4 nie jest
// spełnione.
func parseRequirements(root string) (reqs []Requirement, problems []string, err error) {
	readme, err := os.ReadFile(filepath.Join(root, "requirements", "README.md"))
	if err != nil {
		return nil, nil, err
	}
	prefixes := map[string]bool{}
	for _, line := range strings.Split(string(readme), "\n") {
		if m := prefixRow.FindStringSubmatch(line); m != nil {
			prefixes[m[1]] = true
		}
	}
	if len(prefixes) == 0 {
		return nil, nil, fmt.Errorf("no prefixes in requirements/README.md")
	}
	files, _ := filepath.Glob(filepath.Join(root, "requirements", "*.md"))
	sort.Strings(files)
	seen := map[string]bool{}
	for _, f := range files {
		if filepath.Base(f) == "README.md" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		name := rel(root, f)
		status := ""
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimRight(line, "\r")
			if s, ok := strings.CutPrefix(line, "Status: "); ok {
				status = strings.TrimSpace(s)
				if status != statusDraft && status != statusAccepted {
					problems = append(problems, fmt.Sprintf("%s: nieznany status „%s”", name, status))
				}
				continue
			}
			id := anyID.FindString(line)
			if id == "" || !strings.HasPrefix(line, "| "+id+" |") {
				continue
			}
			if !prefixes[id[:strings.IndexByte(id, '-')]] {
				problems = append(problems, fmt.Sprintf("%s: %s ma prefiks, którego nie ma w requirements/README.md", name, id))
			}
			if status == "" {
				problems = append(problems, fmt.Sprintf("%s: %s jest zdefiniowane przed wierszem „Status:”", name, id))
			}
			if seen[id] {
				problems = append(problems, fmt.Sprintf("%s: %s jest zdefiniowane drugi raz", name, id))
				continue
			}
			seen[id] = true
			text := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "| "+id+" |")), "|"))
			reqs = append(reqs, Requirement{ID: id, File: name, Text: text, Status: status})
		}
	}
	return reqs, problems, nil
}

// InventoryRow to wiersz inwentarza formatu z requirements/format.md (K1): klucz, rozdział APPNOTE, opis, werdykt
// (obsługa albo kod odrzucenia) i etap, w którym werdykt jest sprawdzany testem.
type InventoryRow struct {
	Group       string `json:"group"`
	Key         string `json:"key"`
	Section     string `json:"section"`
	Description string `json:"description"`
	Verdict     string `json:"verdict"`
	Stage       string `json:"stage"`
}

var inventoryKeyRe = regexp.MustCompile("^`([^`]+)`$")

// inventoryHeader to pierwsza komórka nagłówka tabel inwentarza: „| Klucz | APPNOTE | Opis | Werdykt | Etap |”.
// Inne tabele pliku (wymagania FMT, legenda werdyktów) nie są inwentarzem.
const inventoryHeader = "Klucz"

// parseInventory czyta tabele inwentarza z requirements/format.md: tabele o nagłówku zaczynającym się od „Klucz”,
// w których pierwsza komórka wiersza to klucz w odwrotnych apostrofach (np. „| `flag.0` | 4.4.4 | … |
// `entry unsupported-encryption` | 1 |”). Group to najbliższy nagłówek rozdziału nad tabelą. Wiersz bez klucza,
// o innej liczbie kolumn albo bez werdyktu jest problemem (K1: nic nie zostaje bez werdyktu).
func parseInventory(text string) (rows []InventoryRow, problems []string) {
	group := ""
	seen := map[string]bool{}
	inTable, inventory := false, false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "|") {
			inTable, inventory = false, false
			if h := strings.TrimLeft(line, "#"); h != line && strings.HasPrefix(h, " ") {
				group = strings.TrimSpace(h)
			}
			continue
		}
		cells := splitRow(line)
		if !inTable {
			inTable, inventory = true, cells[0] == inventoryHeader
			continue
		}
		if !inventory || strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "| ---") {
			continue
		}
		m := inventoryKeyRe.FindStringSubmatch(cells[0])
		if m == nil {
			problems = append(problems, fmt.Sprintf("inwentarz (%s): wiersz „%s” nie ma klucza w odwrotnych apostrofach", group, cells[0]))
			continue
		}
		row := InventoryRow{Group: group, Key: m[1]}
		if len(cells) != 5 {
			problems = append(problems, fmt.Sprintf("inwentarz: wiersz `%s` ma %d kolumn zamiast 5", row.Key, len(cells)))
		}
		for i, dst := range []*string{&row.Section, &row.Description, &row.Verdict, &row.Stage} {
			if i+1 < len(cells) {
				*dst = cells[i+1]
			}
		}
		row.Verdict = strings.TrimSpace(strings.Trim(row.Verdict, "`"))
		if row.Verdict == "" || row.Verdict == "—" {
			row.Verdict = ""
			problems = append(problems, fmt.Sprintf("inwentarz: wiersz `%s` nie ma werdyktu", row.Key))
		}
		if seen[row.Key] {
			problems = append(problems, fmt.Sprintf("inwentarz: klucz `%s` występuje drugi raz", row.Key))
		}
		seen[row.Key] = true
		rows = append(rows, row)
	}
	return rows, problems
}

// splitRow dzieli wiersz tabeli Markdown na komórki; „\|” jest częścią komórki.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = strings.TrimSuffix(line, "|")
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cur.WriteString(`\|`)
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// skipDirs to katalogi, których nie przeszukujemy: wyniki, kopie odniesienia i metadane.
var skipDirs = map[string]bool{".git": true, ".claude": true, "out": true, "ref": true, "node_modules": true}

// corpusRefFiles to pliki danych testowych (wzorce względem katalogu głównego), w których wiersz z „REQ:” liczy się
// jako dowód tak jak test: manifest korpusu wrogiego i manifest korpusu zgodności.
var corpusRefFiles = []string{"testdata/hostile.json", "testdata/interop/manifest.json"}

// collectRefs zbiera odwołania „REQ:” z komentarzy w plikach *_test.go (każdego modułu w repozytorium) i z plików
// corpusRefFiles. Komentarz wewnątrz funkcji należy do tej funkcji; komentarz poza funkcją — do najbliższej
// funkcji poniżej. Funkcje Test*, Fuzz*, Example* i Benchmark* są dowodem; inne (pomocnicze) — dowodem wspierającym.
func collectRefs(root string) ([]Ref, error) {
	var refs []Ref
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (skipDirs[name] || name == "testdata" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fileRefs, err := refsInGoFile(root, p)
		refs = append(refs, fileRefs...)
		return err
	})
	if err != nil {
		return nil, err
	}
	for _, pattern := range corpusRefFiles {
		files, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			for _, id := range idsAfterREQ(string(b)) {
				refs = append(refs, Ref{ID: id, File: rel(root, f), Corpus: true})
			}
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].ID != refs[j].ID {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].File+refs[i].Test < refs[j].File+refs[j].Test
	})
	return dedupRefs(refs), nil
}

// idsAfterREQ zwraca identyfikatory z każdego wiersza tekstu, od słowa „REQ:” do końca wiersza.
func idsAfterREQ(text string) []string {
	var ids []string
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, "REQ:"); i >= 0 {
			ids = append(ids, anyID.FindAllString(line[i:], -1)...)
		}
	}
	return ids
}

func refsInGoFile(root, p string) ([]Ref, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel(root, p), err)
	}
	pkg, err := importPath(root, filepath.Dir(p))
	if err != nil {
		return nil, err
	}
	var funcs []*ast.FuncDecl
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			funcs = append(funcs, fn)
		}
	}
	var refs []Ref
	for _, cg := range f.Comments {
		ids := idsAfterREQ(cg.Text())
		if len(ids) == 0 {
			continue
		}
		var owner *ast.FuncDecl
		for _, fn := range funcs {
			if cg.Pos() >= fn.Pos() && cg.End() <= fn.End() {
				owner = fn
				break
			}
		}
		if owner == nil {
			for _, fn := range funcs {
				if fn.Pos() > cg.End() {
					owner = fn
					break
				}
			}
		}
		test := ""
		if owner != nil && isTestName(owner.Name.Name) {
			test = owner.Name.Name
		}
		for _, id := range ids {
			refs = append(refs, Ref{ID: id, File: rel(root, p), Package: pkg, Test: test})
		}
	}
	return refs, nil
}

func isTestName(name string) bool {
	for _, prefix := range []string{"Test", "Fuzz", "Example", "Benchmark"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func dedupRefs(refs []Ref) []Ref {
	seen := map[Ref]bool{}
	out := refs[:0]
	for _, r := range refs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// importPath ustala ścieżkę importu katalogu z najbliższego go.mod w górę drzewa (do root włącznie).
func importPath(root, dir string) (string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		b, err := os.ReadFile(filepath.Join(d, "go.mod"))
		if err == nil {
			mod := ""
			for _, line := range strings.Split(string(b), "\n") {
				if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					mod = strings.TrimSpace(m)
					break
				}
			}
			if mod == "" {
				return "", fmt.Errorf("%s/go.mod: no module line", d)
			}
			r, err := filepath.Rel(d, dir)
			if err != nil {
				return "", err
			}
			if r == "." {
				return mod, nil
			}
			return mod + "/" + filepath.ToSlash(r), nil
		}
		if d == root || d == filepath.Dir(d) {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
	}
}

// TestRun to wynik jednego przebiegu `go test -json`.
type TestRun struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Passed  int    `json:"passed"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
	// FailedTests i SkippedTests to klucze testów najwyższego poziomu (pakiet.Test), posortowane.
	FailedTests  []string `json:"failed_tests,omitempty"`
	SkippedTests []string `json:"skipped_tests,omitempty"`
	// BuildFailed to pakiety, które się nie skompilowały albo zakończyły bez wyniku testu.
	BuildFailed []string `json:"build_failed,omitempty"`
	results     map[string]string
	output      map[string][]string
}

type testEvent struct {
	Action, Package, Test, Output string
}

// parseTestEvents czyta strumień `go test -json` i zbiera wyniki oraz wyjście testów najwyższego poziomu.
func parseTestEvents(name, command string, r io.Reader) (*TestRun, error) {
	run := &TestRun{Name: name, Command: command, results: map[string]string{}, output: map[string][]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	pkgFailed := map[string]bool{}
	pkgHasTests := map[string]bool{}
	for sc.Scan() {
		var e testEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Test == "" {
			if e.Action == "fail" {
				pkgFailed[e.Package] = true
			}
			continue
		}
		if strings.Contains(e.Test, "/") {
			continue
		}
		key := e.Package + "." + e.Test
		pkgHasTests[e.Package] = true
		switch e.Action {
		case "pass", "fail", "skip":
			run.results[key] = e.Action
		case "output":
			run.output[key] = append(run.output[key], strings.TrimRight(e.Output, "\n"))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for key, action := range run.results {
		switch action {
		case "pass":
			run.Passed++
		case "fail":
			run.Failed++
			run.FailedTests = append(run.FailedTests, key)
		case "skip":
			run.Skipped++
			run.SkippedTests = append(run.SkippedTests, key)
		}
	}
	for pkg := range pkgFailed {
		if !pkgHasTests[pkg] {
			run.BuildFailed = append(run.BuildFailed, pkg)
		}
	}
	sort.Strings(run.FailedTests)
	sort.Strings(run.SkippedTests)
	sort.Strings(run.BuildFailed)
	return run, nil
}

// Result zwraca wynik testu (pass, fail, skip) albo "" dla nieznanego.
func (r *TestRun) Result(key string) string { return r.results[key] }

// Output zwraca wiersze wyjścia testu.
func (r *TestRun) Output(key string) []string { return r.output[key] }

// ResultByName zwraca wynik testu o danej nazwie w dowolnym pakiecie przebiegu: fail, gdy nie przeszedł
// w którymkolwiek pakiecie, potem skip, potem pass; "" gdy takiego testu w przebiegu nie było.
func (r *TestRun) ResultByName(name string) string {
	rank := map[string]int{"": 0, "pass": 1, "skip": 2, "fail": 3}
	worst := ""
	for key, res := range r.results {
		if strings.HasSuffix(key, "."+name) && rank[res] > rank[worst] {
			worst = res
		}
	}
	return worst
}

// Trace to wiersz macierzy śledzenia: wymaganie, jego dowody i wynik.
type Trace struct {
	Requirement
	Evidence []string `json:"evidence"`
	// Result: pass (wszystkie testy przeszły), manual (dowód uruchamiany na żądanie: benchmark albo test pomijany
	// bez zmiennej GOZIPGO_*), skip (test pominięty, np. bez danych), unresolved (tylko funkcje pomocnicze albo
	// test nieznany w przebiegu), fail, missing (wymaganie przyjęte bez dowodu), draft (szkic bez dowodu: test
	// nie jest jeszcze wymagany).
	Result string `json:"result"`
}

var resultRank = map[string]int{"pass": 0, "manual": 1, "skip": 2, "unresolved": 3, "fail": 4, "missing": 5}

// buildTraces łączy wymagania, odwołania i wyniki przebiegów w macierz śledzenia; zwraca też odwołania do
// nieistniejących wymagań. Odwołanie przy funkcji pomocniczej (np. sprawdzeniu wołanym przez kilka testów) jest
// dowodem wspierającym: nie pogarsza stanu, ale samo nie wystarcza.
func buildTraces(reqs []Requirement, refs []Ref, runs []*TestRun) ([]Trace, []Ref) {
	defined := map[string]bool{}
	for _, r := range reqs {
		defined[r.ID] = true
	}
	byID := map[string][]Ref{}
	var unknown []Ref
	for _, ref := range refs {
		if !defined[ref.ID] {
			unknown = append(unknown, ref)
			continue
		}
		byID[ref.ID] = append(byID[ref.ID], ref)
	}
	lookup := func(key string) (string, []string) {
		for _, run := range runs {
			if a := run.Result(key); a != "" {
				return a, run.Output(key)
			}
		}
		return "", nil
	}
	var traces []Trace
	for _, req := range reqs {
		t := Trace{Requirement: req, Result: "pass"}
		resolved, supporting := 0, 0
		worse := func(s string) {
			if resultRank[s] > resultRank[t.Result] {
				t.Result = s
			}
		}
		for _, ref := range byID[req.ID] {
			switch {
			case ref.Corpus:
				t.Evidence = append(t.Evidence, ref.File)
				resolved++
			case ref.Test == "":
				t.Evidence = append(t.Evidence, ref.File+" (funkcja pomocnicza)")
				supporting++
			case strings.HasPrefix(ref.Test, "Benchmark"):
				t.Evidence = append(t.Evidence, ref.Key()+" (benchmark, na żądanie)")
				resolved++
				worse("manual")
			default:
				resolved++
				res, out := lookup(ref.Key())
				switch res {
				case "pass":
					t.Evidence = append(t.Evidence, ref.Key())
				case "skip":
					if onDemand(out) {
						t.Evidence = append(t.Evidence, ref.Key()+" (na żądanie)")
						worse("manual")
					} else {
						t.Evidence = append(t.Evidence, ref.Key()+" (pominięty)")
						worse("skip")
					}
				case "fail":
					t.Evidence = append(t.Evidence, ref.Key())
					worse("fail")
				default:
					t.Evidence = append(t.Evidence, ref.Key()+" (brak w przebiegu)")
					worse("unresolved")
				}
			}
		}
		switch {
		case resolved == 0 && !req.Accepted():
			t.Result = "draft"
		case resolved == 0 && supporting > 0:
			t.Result = "unresolved"
		case resolved == 0:
			t.Result = "missing"
		}
		traces = append(traces, t)
	}
	return traces, unknown
}

// envPrefix to prefiks zmiennych środowiskowych, którymi włącza się testy na żądanie.
const envPrefix = "GOZIPGO_"

// onDemand mówi, czy test pominięto dlatego, że uruchamia się go zmienną środowiskową GOZIPGO_*, a nie z braku
// danych.
func onDemand(output []string) bool {
	for _, l := range output {
		if strings.Contains(l, envPrefix) && !strings.Contains(l, "not fetched") {
			return true
		}
	}
	return false
}

func atoi(ss []string) []int {
	out := make([]int, len(ss))
	for i, s := range ss {
		out[i], _ = strconv.Atoi(s)
	}
	return out
}

// findCounts szuka w wierszach wyjścia testu pierwszego dopasowania wyrażenia i zwraca jego grupy jako liczby.
func findCounts(lines []string, re *regexp.Regexp) ([]int, bool) {
	for _, l := range lines {
		if m := re.FindStringSubmatch(l); m != nil {
			return atoi(m[1:]), true
		}
	}
	return nil, false
}

// Ratchet to nagłówek pliku zapadki.
type Ratchet struct {
	File    string `json:"file"`
	InScope int    `json:"in_scope"`
	Passing int    `json:"passing"`
	Mode    string `json:"mode"`
	Listed  int    `json:"listed"`
}

var ratchetCountsRe = regexp.MustCompile(`^# in scope (\d+), passing (\d+)`)

// readRatchets czyta nagłówki wszystkich plików *.ratchet w testdata (bez danych pobranych).
func readRatchets(root string) ([]Ratchet, error) {
	var out []Ratchet
	base := filepath.Join(root, "testdata")
	if _, err := os.Stat(base); err != nil {
		return nil, nil
	}
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if p == filepath.Join(base, "fetched") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".ratchet") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		r := Ratchet{File: rel(root, p), Mode: "passing"}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case ratchetCountsRe.MatchString(line):
				n := atoi(ratchetCountsRe.FindStringSubmatch(line)[1:3])
				r.InScope, r.Passing = n[0], n[1]
			case strings.HasPrefix(line, "# mode: "):
				r.Mode = strings.TrimPrefix(line, "# mode: ")
			case line == "" || strings.HasPrefix(line, "#"):
			default:
				r.Listed++
			}
		}
		out = append(out, r)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, err
}

// FuzzRun to wiersz tabeli „Przebiegi” z docs/fuzzing.md (pisze go tools/fuzz).
type FuzzRun struct {
	Date     string `json:"date"`
	Commit   string `json:"commit"`
	Host     string `json:"host"`
	Fuzzer   string `json:"fuzzer"`
	Package  string `json:"package,omitempty"`
	Duration string `json:"duration"`
	Execs    string `json:"execs"`
	Result   string `json:"result"`
	// Dirty: przebieg na kodzie z niezatwierdzonymi zmianami (oznaczenie „(zmiany)”); nie liczy się do K5.
	Dirty bool `json:"dirty,omitempty"`
	// Counted: wiersz liczy się do K5 (wynik „ok”, commit kandydata albo bez zmian biblioteki, bez „(zmiany)”).
	Counted bool `json:"counted"`
	// Failure: przebieg zakończony błędem na kodzie kandydata; K5 nie jest wtedy spełnione.
	Failure bool `json:"failure,omitempty"`
	seconds time.Duration
}

var (
	hexRe       = regexp.MustCompile(`[0-9a-f]{7,40}`)
	fuzzDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	fuzzerCell  = regexp.MustCompile("^`?(Fuzz\\w+)`?(?:\\s*\\(([^)]*)\\))?$")
	fuzzFailure = regexp.MustCompile(`^(\*\*błąd\*\*|FAIL)`)
)

// parseFuzzJournal czyta tabelę przebiegów z docs/fuzzing.md: wiersze z datą, commitem i nazwą fuzzera. Kolumny:
// data, commit, host i sprzęt, fuzzer (z pakietem w nawiasie), czas zegarowy, wykonania, wynik. Wiersz zastępczy
// („—”) nie jest przebiegiem.
func parseFuzzJournal(text string) []FuzzRun {
	var out []FuzzRun
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			continue
		}
		cells := splitRow(line)
		if len(cells) < 7 {
			continue
		}
		m := fuzzerCell.FindStringSubmatch(cells[3])
		if !fuzzDateRe.MatchString(cells[0]) || m == nil {
			continue
		}
		r := FuzzRun{Date: cells[0], Commit: hexRe.FindString(cells[1]), Host: cells[2], Fuzzer: m[1], Package: m[2],
			Duration: cells[4], Execs: cells[5], Result: strings.Join(cells[6:], " | ")}
		r.Dirty = strings.Contains(cells[1], "(zmiany)") || strings.Contains(r.Result, "(zmiany)")
		if d, err := time.ParseDuration(r.Duration); err == nil {
			r.seconds = d
		}
		out = append(out, r)
	}
	return out
}

// countFuzzHours oznacza wiersze liczone do K5 i zwraca ich łączny czas oraz liczbę przebiegów zakończonych
// błędem na kodzie kandydata. same mówi, czy commit ma ten sam kod biblioteki co kandydat. Przebieg zakończony
// błędem zeruje licznik dla kodu, którego dotyczy.
func countFuzzHours(runs []FuzzRun, same func(commit string) bool) (total time.Duration, failures int) {
	for i := range runs {
		r := &runs[i]
		if r.Dirty || r.Commit == "" || !same(r.Commit) {
			continue
		}
		switch {
		case fuzzFailure.MatchString(r.Result):
			r.Failure = true
			failures++
		case strings.HasPrefix(r.Result, "ok") && r.seconds > 0:
			r.Counted = true
			total += r.seconds
		}
	}
	if failures > 0 {
		for i := range runs {
			runs[i].Counted = false
		}
		total = 0
	}
	return total, failures
}

// DocMeasurement to ostatni datowany wiersz tabeli „Pomiary” w docs/testing.md: pomiary spoza `go test` (K3, K7,
// K8 u konsumenta) z datą i commitem. Values ma klucze „K1”…„K9” według nagłówków kolumn.
type DocMeasurement struct {
	Date   string            `json:"date"`
	Commit string            `json:"commit"`
	Values map[string]string `json:"values"`
	Notes  string            `json:"notes,omitempty"`
}

var criterionHeader = regexp.MustCompile(`^(K[1-9])\b`)

// parseMeasurements czyta tabelę, której nagłówek zaczyna się od „| Data | Commit |”; kolumna, której nagłówek
// zaczyna się od „K<n>”, niesie wartość kryterium. Zwraca nil, gdy tabela nie ma jeszcze datowanego wiersza.
func parseMeasurements(text string) *DocMeasurement {
	var header []string
	var last *DocMeasurement
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			header = nil
			continue
		}
		cells := splitRow(line)
		if header == nil {
			if len(cells) >= 2 && cells[0] == "Data" && cells[1] == "Commit" {
				header = cells
			}
			continue
		}
		if len(cells) < 2 || !fuzzDateRe.MatchString(cells[0]) {
			continue
		}
		m := &DocMeasurement{Date: cells[0], Commit: hexRe.FindString(cells[1]), Values: map[string]string{}}
		for i, h := range header {
			if i >= len(cells) {
				break
			}
			if k := criterionHeader.FindStringSubmatch(h); k != nil {
				m.Values[k[1]] = cells[i]
			} else if h == "Uwagi" {
				m.Notes = cells[i]
			}
		}
		last = m
	}
	return last
}

// ADR to decyzja z docs/adr.
type ADR struct {
	File   string `json:"file"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func readADRs(root string) []ADR {
	files, _ := filepath.Glob(filepath.Join(root, "docs", "adr", "[0-9]*.md"))
	sort.Strings(files)
	var out []ADR
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		a := ADR{File: rel(root, f)}
		for _, line := range strings.Split(string(b), "\n") {
			if a.Title == "" && strings.HasPrefix(line, "# ") {
				a.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
			if s, ok := strings.CutPrefix(line, "Status: "); ok && a.Status == "" {
				a.Status = strings.TrimSpace(s)
			}
		}
		out = append(out, a)
	}
	return out
}

// DiffSection to sekcja docs/differences.md z liczbą wierszy tabeli (bez nagłówka i wierszy zastępczych „—”).
type DiffSection struct {
	Title string `json:"title"`
	Rows  int    `json:"rows"`
}

func diffSections(text string) []DiffSection {
	var out []DiffSection
	header := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "## "):
			out = append(out, DiffSection{Title: strings.TrimPrefix(line, "## ")})
			header = false
		case len(out) > 0 && strings.HasPrefix(line, "|---"):
			header = true
		case len(out) > 0 && header && strings.HasPrefix(line, "| "):
			if cells := splitRow(line); cells[0] != "—" && cells[0] != "" {
				out[len(out)-1].Rows++
			}
		case !strings.HasPrefix(line, "|"):
			header = false
		}
	}
	return out
}

// SourceSet to zestaw danych testowych z testdata/sources.json.
type SourceSet struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Dest       string `json:"dest"`
	Files      int    `json:"files"`
	SHA256     string `json:"sha256,omitempty"`
	TreeSHA256 string `json:"tree_sha256,omitempty"`
	LocalOnly  int    `json:"local_only,omitempty"`
}

// DataVerify mówi, czy dane pobrane według testdata/sources.json mają sumy z manifestu. Sprawdzenie liczy te same
// sumy co `tools/fetch -verify`, ale w tym procesie: pakiet dowodów nie uruchamia innych programów (EVD-005).
type DataVerify struct {
	OK    bool     `json:"ok"`
	Lines []string `json:"lines"`
}

type sourcesManifest struct {
	Sets []struct {
		Name, Kind, Dest string
		SHA256           string `json:"sha256"`
		TreeSHA256       string `json:"tree_sha256"`
		FileCount        int    `json:"file_count"`
		Files            []struct {
			Path      string `json:"path"`
			SHA256    string `json:"sha256"`
			LocalOnly bool   `json:"local_only"`
		}
	}
}

// fetchedRoot to katalog danych pobieranych przez tools/fetch; poza nim zestawu nie sprawdzamy.
const fetchedRoot = "testdata/fetched"

// readSources czyta manifest danych pobieranych, liczy jego sumę i sprawdza pobrane dane.
func readSources(root string) ([]SourceSet, string, DataVerify, error) {
	b, err := os.ReadFile(filepath.Join(root, "testdata", "sources.json"))
	if err != nil {
		return nil, "", DataVerify{}, err
	}
	var m sourcesManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", DataVerify{}, fmt.Errorf("testdata/sources.json: %w", err)
	}
	var out []SourceSet
	verify := DataVerify{OK: true}
	note := func(ok bool, format string, args ...any) {
		verify.OK = verify.OK && ok
		verify.Lines = append(verify.Lines, fmt.Sprintf(format, args...))
	}
	for _, s := range m.Sets {
		set := SourceSet{Name: s.Name, Kind: s.Kind, Dest: s.Dest, SHA256: s.SHA256, TreeSHA256: s.TreeSHA256, Files: s.FileCount}
		if s.Kind == "files" {
			set.Files = len(s.Files)
			for _, f := range s.Files {
				if f.LocalOnly {
					set.LocalOnly++
				}
			}
		}
		out = append(out, set)
		if !fs.ValidPath(s.Dest) || !strings.HasPrefix(s.Dest, fetchedRoot+"/") {
			note(false, "%s: katalog „%s” nie leży w %s/", s.Name, s.Dest, fetchedRoot)
			continue
		}
		dest := filepath.Join(root, filepath.FromSlash(s.Dest))
		switch s.Kind {
		case "archive":
			sum, n, err := treeSum(dest)
			switch {
			case err != nil:
				note(false, "%s: brak danych w %s", s.Name, s.Dest)
			case s.TreeSHA256 == "" || sum != s.TreeSHA256 || n != s.FileCount:
				note(false, "%s: suma drzewa %s (%d plików), manifest %s (%d plików)", s.Name, sum, n, orDash(s.TreeSHA256), s.FileCount)
			default:
				note(true, "%s: ok (%d plików)", s.Name, n)
			}
		case "files":
			okCount, skipped, bad := 0, 0, 0
			for _, f := range s.Files {
				var fb []byte
				err := errors.New("unsafe path")
				if fs.ValidPath(f.Path) {
					fb, err = os.ReadFile(filepath.Join(dest, filepath.FromSlash(f.Path)))
				}
				switch {
				case err != nil && f.LocalOnly:
					skipped++
				case err != nil || f.SHA256 == "" || hexSum(fb) != f.SHA256:
					bad++
				default:
					okCount++
				}
			}
			note(bad == 0, "%s: zgodne %d z %d, tylko lokalne i nieobecne %d, brak albo inna suma %d", s.Name, okCount, len(s.Files), skipped, bad)
		default:
			note(false, "%s: nieznany rodzaj „%s”", s.Name, s.Kind)
		}
	}
	if len(m.Sets) == 0 {
		verify.Lines = []string{"manifest nie ma zestawów: nie ma czego sprawdzać"}
	}
	return out, hexSum(b), verify, nil
}

// treeSum liczy sumę drzewa tak jak tools/fetch: SHA-256 z posortowanych wierszy „<sha256 pliku>  <ścieżka>\n”.
// Za dowiązaniami nie podąża, żeby nie czytać spoza repozytorium.
func treeSum(dir string) (string, int, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines = append(lines, hexSum(b)+"  "+rel(dir, p)+"\n")
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	if len(lines) == 0 {
		return "", 0, errors.New("empty")
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		io.WriteString(h, l)
	}
	return hex.EncodeToString(h.Sum(nil)), len(lines), nil
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func rel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}
