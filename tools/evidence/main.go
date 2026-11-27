// Command evidence składa pakiet dowodów gozipgo (kryterium K9, ADR 0001, requirements/evidence.md; narzędzie
// przeniesione z goxsdgo i dostosowane): surowy wynik ostatniego przebiegu testów, macierz śledzenia wymagania →
// testy → wynik, inwentarz formatu z werdyktami, tabelę K1–K9 ze źródłami, sumy danych testowych, dziennik fuzzingu,
// listę decyzji i różnic.
//
//	go -C tools/evidence run . -root ../..       # pakiet w out/evidence/<data>-<commit>/
//	go -C tools/evidence run . -root ../.. -tar  # także archiwum .tar.gz
//
// Pakiet: report.md (dla czytelnika), evidence.json (ta sama treść w postaci maszynowej), go-test-*.json (surowe
// wyniki `go test -count=1 -json ./...` modułu głównego i modułów narzędzi). Raport i evidence.json dla tego samego
// commitu i tych samych danych różnią się tylko datą wygenerowania; czasy są wyłącznie w plikach surowych (EVD-004).
//
// Narzędzie jest osobnym modułem bez zależności. Uruchamia tylko `go` (testy i odczyt wersji) oraz `git` (odczyt
// commitu i stanu drzewa); nie sięga do sieci, nie uruchamia fuzzingu ani programów porównywanych i nie czyta spoza
// repozytorium (EVD-005). Kończy się kodem 1, gdy K4 albo K9 nie są spełnione; pakiet zapisuje także wtedy.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func main() {
	root := flag.String("root", ".", "gozipgo repository root")
	outBase := flag.String("out", "", "directory for packages (default <root>/out/evidence)")
	makeTar := flag.Bool("tar", false, "also write <package>.tar.gz")
	flag.Parse()

	abs, err := filepath.Abs(*root)
	if err != nil {
		fail(err)
	}
	if *outBase == "" {
		*outBase = filepath.Join(abs, "out", "evidence")
	}
	now := time.Now()
	e, raw, err := build(abs, now)
	if err != nil {
		fail(err)
	}
	dir := filepath.Join(*outBase, now.Format("2006-01-02")+"-"+e.Commit)
	if e.Dirty && e.Commit != unknownCommit {
		dir += "-dirty"
	}
	if err := write(dir, e, raw); err != nil {
		fail(err)
	}
	if *makeTar {
		if err := tarDir(dir, dir+".tar.gz"); err != nil {
			fail(err)
		}
	}
	fmt.Printf("evidence package: %s\n", dir)
	for _, c := range e.Criteria {
		fmt.Printf("  %-3s %-8s %s\n", c.ID, c.Status, c.Value)
	}
	os.Exit(exitCode(e.Criteria))
}

// exitCode zwraca 1, gdy K4 albo K9 nie są spełnione (EVD-005); kryterium, które czeka na pomiar, nie jest błędem.
func exitCode(criteria []Criterion) int {
	for _, c := range criteria {
		if (c.ID == "K4" || c.ID == "K9") && c.Status == "not_met" {
			return 1
		}
	}
	return 0
}

// unknownCommit zastępuje skrót commitu, gdy repozytorium nie ma jeszcze commitu albo nie jest repozytorium git.
const unknownCommit = "unknown"

// skipToolModules to moduły narzędzi, których testów pakiet dowodów nie uruchamia: tools/compare woła programy
// porównywane (czarne skrzynki), a pakiet nie uruchamia programów zewnętrznych (EVD-005).
var skipToolModules = map[string]bool{"compare": true}

type runSpec struct{ name, dir, file string }

// testRuns zwraca przebiegi do wykonania: moduł główny i każdy moduł narzędzi z tools/ (EVD-001), w stałej
// kolejności.
func testRuns(root string) []runSpec {
	specs := []runSpec{{"moduł główny", ".", "go-test-module.json"}}
	mods, _ := filepath.Glob(filepath.Join(root, "tools", "*", "go.mod"))
	sort.Strings(mods)
	for _, m := range mods {
		name := filepath.Base(filepath.Dir(m))
		if skipToolModules[name] {
			continue
		}
		specs = append(specs, runSpec{"tools/" + name, "tools/" + name, "go-test-" + name + ".json"})
	}
	return specs
}

// build uruchamia przebiegi i składa pakiet. raw to surowe wyniki przebiegów (nazwa pliku → treść); zawierają czasy,
// więc nie wchodzą do części powtarzalnej.
func build(root string, now time.Time) (*Evidence, map[string][]byte, error) {
	reqs, reqProblems, err := parseRequirements(root)
	if err != nil {
		return nil, nil, err
	}
	refs, err := collectRefs(root)
	if err != nil {
		return nil, nil, err
	}
	formatText, _ := os.ReadFile(filepath.Join(root, "requirements", "format.md"))
	inventory, invProblems := parseInventory(string(formatText))

	raw := map[string][]byte{}
	var runs []*TestRun
	for _, spec := range testRuns(root) {
		out := runCmd(filepath.Join(root, filepath.FromSlash(spec.dir)), testEnv(), "go", "test", "-count=1", "-json", "./...")
		raw[spec.file] = out
		cmd := "go test -count=1 -json ./..."
		if spec.dir != "." {
			cmd = "go -C " + spec.dir + " test -count=1 -json ./..."
		}
		run, err := parseTestEvents(spec.name, cmd, bytes.NewReader(out))
		if err != nil {
			return nil, nil, err
		}
		runs = append(runs, run)
	}

	commit, dirty := gitState(root)
	traces, unknown := buildTraces(reqs, refs, runs)
	in := Inputs{Runs: runs, Traces: traces, UnknownRefs: unknown, RequirementProblems: reqProblems,
		Inventory: inventory, InventoryProblems: invProblems}

	diffText, _ := os.ReadFile(filepath.Join(root, "docs", "differences.md"))
	in.Differences = diffSections(string(diffText))

	journal, _ := os.ReadFile(filepath.Join(root, "docs", "fuzzing.md"))
	fuzz := parseFuzzJournal(string(journal))
	in.FuzzHours, in.FuzzFailures = countFuzzHours(fuzz, func(c string) bool { return sameLibrary(root, commit, c) })

	testingDoc, _ := os.ReadFile(filepath.Join(root, "docs", "testing.md"))
	in.Measurement = parseMeasurements(string(testingDoc))

	sources, sourcesSHA, verify, err := readSources(root)
	if err != nil {
		return nil, nil, err
	}
	in.DataVerify = verify
	ratchets, err := readRatchets(root)
	if err != nil {
		return nil, nil, err
	}
	goVersion, err := runCmdErr(root, testEnv(), "go", "env", "GOVERSION")
	if err != nil {
		goVersion = []byte(runtime.Version())
	}

	e := &Evidence{
		Generated:           now.Format("2006-01-02 15:04"),
		Commit:              commit,
		Dirty:               dirty,
		GoVersion:           strings.TrimSpace(string(goVersion)),
		Platform:            runtime.GOOS + "/" + runtime.GOARCH,
		Criteria:            buildCriteria(in),
		Runs:                runs,
		RequirementProblems: reqProblems,
		Traces:              traces,
		UnknownRefs:         unknown,
		Inventory:           inventory,
		InventoryProblems:   invProblems,
		Ratchets:            ratchets,
		Fuzz:                fuzz,
		FuzzHours:           in.FuzzHours.Hours(),
		Measurement:         in.Measurement,
		Sources:             sources,
		SourcesSHA:          sourcesSHA,
		DataVerify:          verify,
		ADRs:                readADRs(root),
		Differences:         in.Differences,
	}
	for name := range raw {
		e.Files = append(e.Files, name)
	}
	e.Files = append(e.Files, "evidence.json", "report.md")
	sort.Strings(e.Files)
	return e, raw, nil
}

// testEnv to środowisko przebiegów: bez zmiennych GOZIPGO_*, które włączają testy na żądanie albo zmieniają dane
// (pakiet opisuje przebieg domyślny), i z wyłączonym pobieraniem modułów oraz narzędzi Go, żeby przebieg nie mógł
// sięgnąć do sieci (EVD-005).
func testEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, envPrefix) || name == "GOPROXY" || name == "GOTOOLCHAIN" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GOPROXY=off", "GOTOOLCHAIN=local")
}

// gitState zwraca skrót bieżącego commitu i to, czy drzewo robocze różni się od niego (zmiany albo nowe pliki
// poza ignorowanymi). Bez commitu albo bez repozytorium: unknownCommit i true.
func gitState(root string) (string, bool) {
	out, err := runCmdErr(root, nil, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return unknownCommit, true
	}
	commit := strings.TrimSpace(string(out))
	st, err := runCmdErr(root, nil, "git", "status", "--porcelain")
	return commit, err != nil || strings.TrimSpace(string(st)) != ""
}

// sameLibrary mówi, czy commit ma ten sam kod biblioteki co kandydat: różnica poza testami, danymi testowymi,
// dokumentami i narzędziami jest pusta (docs/fuzzing.md).
func sameLibrary(root, candidate, commit string) bool {
	if candidate == "" || candidate == unknownCommit || commit == "" {
		return false
	}
	if strings.HasPrefix(candidate, commit) || strings.HasPrefix(commit, candidate) {
		return true
	}
	// Kod biblioteki to pliki Go pakietów biblioteki bez testów i go.mod; dokumenty, wzorce, narzędzia i testy
	// całego repozytorium do niego nie należą.
	cmd := exec.Command("git", "diff", "--quiet", candidate, commit, "--", "go.mod", "zipreader", "zipwriter", "internal",
		":!*_test.go", ":!internal/testkit", ":!internal/project", ":!**/testdata")
	cmd.Dir = root
	return cmd.Run() == nil
}

func runCmd(dir string, env []string, name string, args ...string) []byte {
	out, _ := runCmdErr(dir, env, name, args...)
	return out
}

func runCmdErr(dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

func write(dir string, e *Evidence, raw map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, b := range raw {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	js, err := marshal(e)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "evidence.json"), js, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(renderReport(e)), 0o644)
}

func marshal(e *Evidence) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(e)
	return buf.Bytes(), err
}

func tarDir(dir, target string) error {
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	base := filepath.Base(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: base + "/" + ent.Name(), Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := io.Copy(tw, bytes.NewReader(b)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "evidence:", err)
	os.Exit(1)
}
