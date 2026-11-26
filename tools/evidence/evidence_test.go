package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fuzzJournalHeader = "## Przebiegi\n\n| Data | Commit | Host i sprzęt | Fuzzer | Czas zegarowy | Wykonania | Wynik |\n|---|---|---|---|---|---|---|\n"

// Tabela „Pomiary” w postaci, w jakiej jest w docs/testing.md przed pierwszym pomiarem.
const measurementsPlaceholder = "## Pomiary\n\n| Data | Commit | K1: inwentarz (z werdyktem / wszystkie) | K2: korpus zgodności | K3: zgodność z czarnymi skrzynkami | Uwagi |\n|---|---|---|---|---|---|\n| — | — | — | — | — | jeszcze bez kodu |\n"

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeRepo buduje w katalogu tymczasowym minimalne repozytorium: wymagania przyjęte i szkice, moduł z testami
// z odwołaniami do wymagań, inwentarz formatu, zapadkę, dziennik fuzzingu i dokumenty.
func fakeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"requirements/README.md": "| Prefiks | Plik | Zakres |\n|---|---|---|\n| `AB` | `a.md` | a |\n| `CDE` | `c.md` | c |\n| `FMT` | `format.md` | f |\n",
		"requirements/a.md": "# A\n\nStatus: przyjęte\n\n| Id | Wymaganie |\n|---|---|\n| AB-001 | pierwsze |\n| AB-002 | drugie, z \\| w treści |\n| AB-003 | tylko pomocnicza |\n| AB-004 | benchmark |\n| AB-005 | na żądanie |\n\n" +
			"Status: szkic\n\n| Id | Wymaganie |\n|---|---|\n| AB-006 | szkic bez testu |\n| AB-007 | szkic z testem |\n",
		"requirements/c.md": "Status: przyjęte\n\n| Id | Wymaganie |\n|---|---|\n| CDE-001 | bez testu |\n",
		"requirements/format.md": "# Inwentarz\n\nStatus: szkic\n\n| Id | Wymaganie |\n|---|---|\n| FMT-001 | każdy rekord ma wiersz |\n\n" +
			"## Jak czytać tabele\n\n| Werdykt | Znaczenie |\n|---|---|\n| `accept` | przyjmowane |\n| `reject KOD` | odrzucane |\n\n" +
			"## Flagi ogólne\n\n| Klucz | APPNOTE | Opis | Werdykt | Etap |\n|---|---|---|---|---|\n" +
			"| `flag.0` | 4.4.4 | szyfrowanie | `entry unsupported-encryption` | 1 |\n" +
			"| `flag.3` | 4.4.4 | deskryptor danych, bity 1 \\| 2 | `ok` | 2 |\n\n" +
			"## Metody\n\n| Klucz | APPNOTE | Opis | Werdykt | Etap |\n|---|---|---|---|---|\n" +
			"| `method.8` | 4.4.5 | Deflate | `ok` | 2 |\n",
		"go.mod": "module example.com/m\n\ngo 1.22\n",
		"p/p_test.go": `package p

import "testing"

// TestOne sprawdza pierwsze. REQ: AB-001
func TestOne(t *testing.T) {}

func TestTwo(t *testing.T) {
	// REQ: AB-002, XY-001
	// kolejny wiersz komentarza wymienia AB-005, ale to nie jest odwołanie
}

// REQ: AB-002, AB-003
func helper() {}

// REQ: AB-004
func BenchmarkFour(b *testing.B) {}

// REQ: AB-005
func TestFive(t *testing.T) {}

// REQ: AB-007
func TestSeven(t *testing.T) {}

var notAComment = "REQ: CDE-001"
`,
		"out/ignored_test.go":        "package ignored\n\n// REQ: CDE-001\nfunc TestIgnored() {}\n",
		"testdata/hostile.json":      "{\"archives\": [{\"name\": \"x\", \"note\": \"REQ: AB-001\"}]}\n",
		"testdata/sources.json":      "{\"note\": \"x\", \"sets\": []}\n",
		"testdata/interop.ratchet":   "# header\n# in scope 10, passing 8\n# mode: failing\na.zip\nb.zip\n",
		"testdata/fetched/x.ratchet": "# in scope 1, passing 1\n",
		"docs/adr/0001-scope.md":     "# ADR 0001: zakres\n\nStatus: przyjęta\n",
		"docs/adr/open-questions.md": "# Pytania\n",
		"docs/differences.md":        "# Różnice\n\n## A\n\n| x | y |\n|---|---|\n| — | — |\n\n## B\n\n| x | y |\n|---|---|\n| jeden | z |\n| dwa | z |\n\n## C\n\nnic\n",
		"docs/testing.md":            "# Testy\n\n| Warstwa | Gdzie |\n|---|---|\n| a | b |\n\n" + measurementsPlaceholder,
		"docs/fuzzing.md": "| Fuzzer | Pakiet | Niezmiennik | Od etapu |\n|---|---|---|---|\n| `FuzzA` | `x` | bez paniki | 1 |\n\n" + fuzzJournalHeader +
			"| — | — | — | — | — | — | jeszcze bez kodu |\n" +
			"| 2026-11-25 15:59 | `abc1234` | NAS (4 rdzenie) | `FuzzA` (./x) | 15m3s | 10 | ok |\n" +
			"| 2026-11-25 16:00 | `abc1234` (zmiany) | NAS | `FuzzA` (./x) | 2h0m0s | 10 | ok |\n" +
			"| 2026-11-26 10:00 | `def5678` | NAS | `FuzzB` | 3h0m0s | 10 | **błąd**: input a \\| b |\n" +
			"| 2026-11-26 11:00 | `abc1234` | NAS | `FuzzB` (./x) | — | — | nieuruchomiony |\n",
	})
	return root
}

const fakeEvents = `{"Action":"output","Package":"example.com/m/p","Test":"TestOne","Output":"=== RUN TestOne\n"}
{"Action":"pass","Package":"example.com/m/p","Test":"TestOne"}
{"Action":"pass","Package":"example.com/m/p","Test":"TestOne/sub"}
{"Action":"skip","Package":"example.com/m/p","Test":"TestTwo"}
{"Action":"output","Package":"example.com/m/p","Test":"TestFive","Output":"    x_test.go:1: set GOZIPGO_LARGE=1 to run\n"}
{"Action":"skip","Package":"example.com/m/p","Test":"TestFive"}
{"Action":"pass","Package":"example.com/m/p","Test":"TestSeven"}
{"Action":"pass","Package":"example.com/m/p"}
{"Action":"fail","Package":"example.com/m/q"}
not json
`

// REQ: EVD-002
func TestTraceabilityMatrix(t *testing.T) {
	root := fakeRepo(t)
	reqs, problems, err := parseRequirements(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("problems: %v", problems)
	}
	if len(reqs) != 9 || reqs[1].Text != `drugie, z \| w treści` || !reqs[4].Accepted() || reqs[5].Status != statusDraft {
		t.Fatalf("requirements: %+v", reqs)
	}
	refs, err := collectRefs(root)
	if err != nil {
		t.Fatal(err)
	}
	run, err := parseTestEvents("m", "go test", strings.NewReader(fakeEvents))
	if err != nil {
		t.Fatal(err)
	}
	if run.Passed != 2 || run.Skipped != 2 || len(run.BuildFailed) != 1 || run.BuildFailed[0] != "example.com/m/q" {
		t.Fatalf("run: %+v", run)
	}
	traces, unknown := buildTraces(reqs, refs, []*TestRun{run})
	want := map[string]string{"AB-001": "pass", "AB-002": "skip", "AB-003": "unresolved", "AB-004": "manual",
		"AB-005": "manual", "AB-006": "draft", "AB-007": "pass", "CDE-001": "missing", "FMT-001": "draft"}
	byID := map[string]Trace{}
	for _, tr := range traces {
		byID[tr.ID] = tr
		if tr.Result != want[tr.ID] {
			t.Errorf("%s: result %s, want %s (evidence %v)", tr.ID, tr.Result, want[tr.ID], tr.Evidence)
		}
	}
	// Pierwsze wymaganie ma dwa dowody: test i wiersz manifestu korpusu wrogiego.
	if ev := byID["AB-001"].Evidence; len(ev) != 2 || ev[1] != "testdata/hostile.json" {
		t.Errorf("AB-001 evidence: %v", ev)
	}
	// Drugie: test pominięty z braku danych (nie na żądanie) i funkcja pomocnicza, która stanu nie pogarsza.
	if len(byID["AB-002"].Evidence) != 2 {
		t.Errorf("AB-002 evidence: %v", byID["AB-002"].Evidence)
	}
	// Identyfikator, którego nie ma w wymaganiach, jest odwołaniem nieznanym, także z prefiksem spoza README.
	if len(unknown) != 1 || unknown[0].ID != "XY-001" || unknown[0].Test != "TestTwo" {
		t.Errorf("unknown refs: %+v", unknown)
	}
}

// REQ: EVD-002
func TestRequirementProblemsAreReported(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"requirements/README.md": "| `AB` | `a.md` | a |\n",
		"requirements/a.md":      "| AB-001 | przed statusem |\n\nStatus: gotowe\n\n| AB-002 | zły status |\n\nStatus: szkic\n\n| AB-002 | drugi raz |\n| ZZ-001 | obcy prefiks |\n",
	})
	reqs, problems, err := parseRequirements(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 3 {
		t.Errorf("requirements: %+v", reqs)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"AB-001 jest zdefiniowane przed", "nieznany status „gotowe”", "AB-002 jest zdefiniowane drugi raz", "ZZ-001 ma prefiks"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	// Problem w plikach wymagań oznacza K4 niespełnione i kod wyjścia 1.
	criteria := buildCriteria(Inputs{RequirementProblems: problems, DataVerify: DataVerify{OK: true}})
	if criteria[3].ID != "K4" || criteria[3].Status != "not_met" || exitCode(criteria) != 1 {
		t.Errorf("K4: %+v, exit %d", criteria[3], exitCode(criteria))
	}
}

// REQ: EVD-002
func TestFormatInventory(t *testing.T) {
	root := fakeRepo(t)
	b, _ := os.ReadFile(filepath.Join(root, "requirements", "format.md"))
	rows, problems := parseInventory(string(b))
	if len(problems) != 0 || len(rows) != 3 {
		t.Fatalf("rows %+v, problems %v", rows, problems)
	}
	want := InventoryRow{Group: "Flagi ogólne", Key: "flag.0", Section: "4.4.4", Description: "szyfrowanie", Verdict: "entry unsupported-encryption", Stage: "1"}
	if rows[0] != want {
		t.Errorf("row 0: %+v", rows[0])
	}
	if rows[1].Description != `deskryptor danych, bity 1 \| 2` || rows[1].Verdict != "ok" || rows[2].Group != "Metody" {
		t.Errorf("rows: %+v", rows[1:])
	}
	// Tabela legendy (nagłówek „Werdykt”) nie jest inwentarzem, choć jej wiersze też zaczynają się od klucza.
	for _, r := range rows {
		if r.Key == "accept" || r.Group == "Jak czytać tabele" {
			t.Errorf("legend row in the inventory: %+v", r)
		}
	}
	_, problems = parseInventory("## G\n\n| Klucz | APPNOTE | Opis | Werdykt | Etap |\n|---|---|---|---|---|\n" +
		"| `a.1` | 1 | opis | | 1 |\n| `a.2` | 1 | opis | `ok` |\n| `a.2` | 1 | opis | `ok` | 2 |\n| a.3 | 1 | opis | `ok` | 2 |\n")
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"`a.1` nie ma werdyktu", "`a.2` ma 4 kolumn", "`a.2` występuje drugi raz", "„a.3” nie ma klucza"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	if rows, problems := parseInventory("# Inwentarz\n\nStatus: szkic\n\n| Id | Wymaganie |\n|---|---|\n| FMT-001 | tekst z `kodem` |\n"); len(rows) != 0 || len(problems) != 0 {
		t.Errorf("file without inventory tables: %+v %v", rows, problems)
	}
}

// REQ: EVD-003
func TestFuzzHoursCountOnlyCleanRunsOfTheCandidate(t *testing.T) {
	root := fakeRepo(t)
	b, _ := os.ReadFile(filepath.Join(root, "docs", "fuzzing.md"))
	runs := parseFuzzJournal(string(b))
	if len(runs) != 4 {
		t.Fatalf("%d runs: %+v", len(runs), runs)
	}
	if runs[0].Host != "NAS (4 rdzenie)" || runs[0].Fuzzer != "FuzzA" || runs[0].Package != "./x" || runs[2].Fuzzer != "FuzzB" || runs[2].Result != `**błąd**: input a \| b` {
		t.Errorf("rows: %+v", runs)
	}
	// Kandydatem jest abc1234: liczy się tylko czysty przebieg z wynikiem ok; błąd na innym kodzie nie szkodzi.
	total, failures := countFuzzHours(runs, func(c string) bool { return c == "abc1234" })
	if total != 15*time.Minute+3*time.Second || failures != 0 {
		t.Errorf("counted %v, failures %d; rows %+v", total, failures, runs)
	}
	if !runs[0].Counted || !runs[1].Dirty || runs[1].Counted || runs[2].Counted || runs[2].Failure || runs[3].Counted {
		t.Errorf("flags: %+v", runs)
	}
	// Gdy błąd dotyczy kodu kandydata, licznik się zeruje, a K5 nie jest spełnione.
	total, failures = countFuzzHours(runs, func(string) bool { return true })
	if total != 0 || failures != 1 || runs[0].Counted || !runs[2].Failure {
		t.Errorf("with a failure on the candidate: %v, %d, %+v", total, failures, runs)
	}
	// Dziennik bez przebiegów (stan sprzed pierwszej kampanii) to zero godzin, nie błąd.
	if runs := parseFuzzJournal(fuzzJournalHeader + "| — | — | — | — | — | — | jeszcze bez kodu |\n"); len(runs) != 0 {
		t.Errorf("placeholder row parsed as a run: %+v", runs)
	}
}

// REQ: EVD-003
func TestMeasurementsTable(t *testing.T) {
	if m := parseMeasurements("# Testy\n\n" + measurementsPlaceholder); m != nil {
		t.Errorf("placeholder row parsed as a measurement: %+v", m)
	}
	text := "| Data | Commit | K1: inwentarz | K2: korpus | K3: czarne skrzynki | K7: wydajność | Uwagi |\n|---|---|---|---|---|---|---|\n" +
		"| 2026-12-01 | `abc1234` | 10 / 10 | 30 / 30 | 99% | — | pierwszy |\n" +
		"| 2026-12-05 | `def5678` | 12 / 12 | 38 / 38 | 100% (38 / 38), spełnione | 1,7× archive/zip, niespełnione | drugi |\n"
	m := parseMeasurements(text)
	if m == nil || m.Date != "2026-12-05" || m.Commit != "def5678" || m.Values["K3"] != "100% (38 / 38), spełnione" || m.Notes != "drugi" {
		t.Fatalf("measurement: %+v", m)
	}
	in := Inputs{Measurement: m}
	if v, status, source := in.documented("K3"); status != "met" || v == "" || !strings.Contains(source, "2026-12-05") || !strings.Contains(source, "def5678") {
		t.Errorf("K3: %q %s %q", v, status, source)
	}
	if _, status, _ := in.documented("K7"); status != "not_met" {
		t.Errorf("K7: %s", status)
	}
	// Kolumny K8 nie ma: nie zmierzono.
	if v, status, _ := in.documented("K8"); v != "" || status != "pending" {
		t.Errorf("K8: %q %s", v, status)
	}
}

func events(lines ...string) *TestRun {
	run, _ := parseTestEvents("moduł główny", "go test", strings.NewReader(strings.Join(lines, "\n")))
	return run
}

func pass(pkg, test string, logs ...string) string {
	var out []string
	for _, l := range logs {
		b, _ := json.Marshal(map[string]string{"Action": "output", "Package": pkg, "Test": test, "Output": "    x_test.go:1: " + l + "\n"})
		out = append(out, string(b))
	}
	return strings.Join(append(out, `{"Action":"pass","Package":"`+pkg+`","Test":"`+test+`"}`), "\n")
}

func statuses(cs []Criterion) map[string]string {
	got := map[string]string{}
	for _, c := range cs {
		got[c.ID] = c.Status
	}
	return got
}

// REQ: EVD-003, EVD-005
func TestCriteriaFromInputs(t *testing.T) {
	accepted := Requirement{ID: "AB-001", Status: statusAccepted}
	inventory := []InventoryRow{{Key: "flag.0", Verdict: "ok"}, {Key: "flag.1", Verdict: "ok"}}
	run := events(
		pass(pkgReader, "TestFormatInventory", "format inventory: 2 rows, 2 verified, 0 pending (stage 2)"),
		pass(pkgReader, "TestInteropCorpus", "interop corpus: 38 archives, 37 read as the manifest says, 1 listed exceptions"),
		pass(pkgRoot, "TestRequirementsCovered", "requirements: 1 accepted, 0 draft; 1 referenced by tests or corpus files"),
		pass(pkgRoot, "TestAPISnapshot"),
		pass(pkgRoot, "TestErrorCatalog"),
		pass(pkgReader, "TestHostileArchives"),
		pass(pkgRoot, "TestNoIOImports"),
		pass(pkgReader, "TestPathological"),
		pass(pkgRoot+"/internal/inflate", "TestPathological"),
		pass(pkgReader, "TestLargeArchive"),
		pass(pkgReader, "TestDeterministic"),
	)
	in := Inputs{Runs: []*TestRun{run}, Inventory: inventory, DataVerify: DataVerify{OK: true}, FuzzHours: 25 * time.Hour,
		Differences: []DiffSection{{Title: "Korpus zgodności: wyjątki (K2)", Rows: 1}},
		Traces:      []Trace{{Requirement: accepted, Result: "pass"}},
		Measurement: &DocMeasurement{Date: "2026-12-05", Commit: "def5678", Values: map[string]string{
			"K3": "100%, spełnione", "K7": "1,2× archive/zip, spełnione", "K8": "godocxgo: 12 / 12, spełnione"}}}
	criteria := buildCriteria(in)
	for id, s := range statuses(criteria) {
		if s != "met" {
			t.Errorf("%s: %s, want met", id, s)
		}
	}
	if len(criteria) != 9 || exitCode(criteria) != 0 {
		t.Errorf("%d criteria, exit %d", len(criteria), exitCode(criteria))
	}
	for _, c := range criteria {
		if c.Value == "" || c.Target == "" || c.Source == "" {
			t.Errorf("%s: empty cell: %+v", c.ID, c)
		}
	}

	// Stan etapu 0: testów niosących liczby jeszcze nie ma, wymagania są szkicami, dokumenty mają wiersze „—”.
	// Nic nie jest niespełnione, wszystko poza K9 czeka, kod wyjścia 0.
	empty := buildCriteria(Inputs{Runs: []*TestRun{events(pass(pkgRoot, "TestRequirementsCovered", "requirements: 0 accepted, 1 draft; 0 referenced by tests or corpus files"))},
		Traces: []Trace{{Requirement: Requirement{ID: "AB-002", Status: statusDraft}, Result: "draft"}}, DataVerify: DataVerify{OK: true}})
	want := map[string]string{"K1": "pending", "K2": "pending", "K3": "pending", "K4": "pending", "K5": "pending", "K6": "pending",
		"K7": "pending", "K8": "pending", "K9": "met"}
	for id, s := range statuses(empty) {
		if s != want[id] {
			t.Errorf("stage 0, %s: %s, want %s", id, s, want[id])
		}
	}
	if exitCode(empty) != 0 {
		t.Error("stage 0: exit 1")
	}
	for _, c := range empty {
		if (c.ID == "K3" || c.ID == "K5") && !strings.Contains(c.Value, notMeasured) {
			t.Errorf("%s value: %q", c.ID, c.Value)
		}
	}

	// Pojedyncze odchylenia od stanu spełnionego.
	change := func(f func(in *Inputs)) map[string]string {
		c := in
		c.Traces = append([]Trace(nil), in.Traces...)
		f(&c)
		return statuses(buildCriteria(c))
	}
	if s := change(func(in *Inputs) {
		in.Traces = append(in.Traces, Trace{Requirement: Requirement{ID: "AB-009", Status: statusAccepted}, Result: "missing"})
	}); s["K4"] != "not_met" || s["K9"] != "not_met" {
		t.Errorf("accepted requirement without a test: %v", s)
	}
	if s := change(func(in *Inputs) { in.Traces[0].Result = "skip" }); s["K4"] != "pending" {
		t.Errorf("skipped test: K4 %s", s["K4"])
	}
	if s := change(func(in *Inputs) { in.UnknownRefs = []Ref{{ID: "XY-001"}} }); s["K4"] != "not_met" {
		t.Errorf("unknown reference: K4 %s", s["K4"])
	}
	if s := change(func(in *Inputs) {
		in.Traces = append(in.Traces, Trace{Requirement: Requirement{ID: "AB-010", Status: statusDraft}, Result: "draft"})
	}); s["K4"] != "not_met" {
		t.Errorf("tool and TestRequirementsCovered disagree on counts: K4 %s", s["K4"])
	}
	if s := change(func(in *Inputs) { in.Inventory = append(in.Inventory, InventoryRow{Key: "flag.2"}) }); s["K1"] != "not_met" {
		t.Errorf("inventory row count differs from the test: K1 %s", s["K1"])
	}
	if s := change(func(in *Inputs) { in.Differences = nil }); s["K2"] != "not_met" {
		t.Errorf("corpus exception not described in differences.md: K2 %s", s["K2"])
	}
	if s := change(func(in *Inputs) { in.FuzzHours = 23 * time.Hour }); s["K5"] != "pending" {
		t.Errorf("23 h of fuzzing: K5 %s", s["K5"])
	}
	if s := change(func(in *Inputs) { in.FuzzFailures = 1 }); s["K5"] != "not_met" {
		t.Errorf("fuzzing failure: K5 %s", s["K5"])
	}
	if s := change(func(in *Inputs) { in.DataVerify.OK = false }); s["K9"] != "pending" {
		t.Errorf("fetched data missing: K9 %s", s["K9"])
	}
	if s := change(func(in *Inputs) { in.Measurement = nil }); s["K3"] != "pending" || s["K7"] != "pending" || s["K8"] != "pending" {
		t.Errorf("no measurement row: %v", s)
	}

	// Niezaliczony test z K6 i czekające wiersze inwentarza.
	failing := events(
		pass(pkgReader, "TestFormatInventory", "format inventory: 2 rows, 1 verified, 1 pending (stage 2)"),
		`{"Action":"fail","Package":"`+pkgReader+`","Test":"TestHostileArchives"}`,
		pass(pkgRoot, "TestNoIOImports"),
	)
	s := statuses(buildCriteria(Inputs{Runs: []*TestRun{failing}, Inventory: inventory, DataVerify: DataVerify{OK: true}}))
	if s["K1"] != "pending" || s["K6"] != "not_met" || s["K9"] != "not_met" {
		t.Errorf("failing run: %v", s)
	}
}

// REQ: EVD-001, EVD-003
func TestRatchetsDifferencesAndDecisions(t *testing.T) {
	root := fakeRepo(t)
	rs, err := readRatchets(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].File != "testdata/interop.ratchet" || rs[0].InScope != 10 || rs[0].Passing != 8 || rs[0].Mode != "failing" || rs[0].Listed != 2 {
		t.Fatalf("ratchets: %+v", rs)
	}
	if rs, err := readRatchets(t.TempDir()); err != nil || len(rs) != 0 {
		t.Errorf("repository without testdata: %+v, %v", rs, err)
	}
	diff, _ := os.ReadFile(filepath.Join(root, "docs", "differences.md"))
	sections := diffSections(string(diff))
	if len(sections) != 3 || sections[0].Rows != 0 || sections[1].Rows != 2 || sections[2].Rows != 0 {
		t.Errorf("sections: %+v", sections)
	}
	adrs := readADRs(root)
	if len(adrs) != 1 || adrs[0].Title != "ADR 0001: zakres" || adrs[0].Status != "przyjęta" {
		t.Errorf("ADRs: %+v", adrs)
	}
}

// REQ: EVD-001, EVD-005
func TestSourcesAreVerifiedInProcess(t *testing.T) {
	root := fakeRepo(t)
	sets, sum, verify, err := readSources(root)
	if err != nil || len(sets) != 0 || len(sum) != 64 || !verify.OK || len(verify.Lines) != 1 {
		t.Fatalf("empty manifest: %+v %q %+v %v", sets, sum, verify, err)
	}
	writeFiles(t, root, map[string]string{
		"testdata/fetched/set/a.zip":     "a",
		"testdata/fetched/set/sub/b.zip": "b",
		"testdata/fetched/one/c.zip":     "c",
	})
	tree, n, err := treeSum(filepath.Join(root, "testdata", "fetched", "set"))
	if err != nil || n != 2 {
		t.Fatal(tree, n, err)
	}
	manifest := func(treeSHA string) string {
		return `{"sets": [
			{"name": "set", "kind": "archive", "dest": "testdata/fetched/set", "url": "https://example.com/x.tar.gz", "sha256": "00", "tree_sha256": "` + treeSHA + `", "file_count": 2},
			{"name": "one", "kind": "files", "dest": "testdata/fetched/one", "files": [
				{"path": "c.zip", "urls": [], "sha256": "` + hexSum([]byte("c")) + `"},
				{"path": "d.zip", "urls": [], "sha256": "00", "local_only": true}]},
			{"name": "outside", "kind": "archive", "dest": "docs", "tree_sha256": "00", "file_count": 1}]}`
	}
	writeFiles(t, root, map[string]string{"testdata/sources.json": manifest(tree)})
	sets, _, verify, err = readSources(root)
	if err != nil || len(sets) != 3 || sets[1].Files != 2 || sets[1].LocalOnly != 1 {
		t.Fatalf("sets: %+v, %v", sets, err)
	}
	// Dwa zestawy zgodne; trzeci wskazuje katalog spoza testdata/fetched/ i nie jest czytany.
	if verify.OK || len(verify.Lines) != 3 || !strings.Contains(verify.Lines[0], "ok (2 plików)") ||
		!strings.Contains(verify.Lines[1], "zgodne 1 z 2") || !strings.Contains(verify.Lines[2], "nie leży w testdata/fetched/") {
		t.Errorf("verify: %+v", verify)
	}
	writeFiles(t, root, map[string]string{"testdata/sources.json": manifest(strings.Repeat("0", 64))})
	if _, _, verify, _ = readSources(root); !strings.Contains(verify.Lines[0], "suma drzewa") {
		t.Errorf("tree mismatch not reported: %+v", verify)
	}
}

// Suma drzewa liczy się tak samo jak w tools/fetch (ta sama wartość odniesienia co w jego teście).
func TestTreeSumMatchesFetch(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.xsd": "a", "sub/b.xml": "b"})
	if sum, n, err := treeSum(dir); err != nil || n != 2 || sum != "af2a37d086ae3007af6beb69df9cf0d1569699b41a4e2e0134a877a5db68afd0" {
		t.Errorf("treeSum: %s, %d, %v", sum, n, err)
	}
}

// REQ: EVD-004
func TestReportIsDeterministic(t *testing.T) {
	e := &Evidence{Commit: "abc1234",
		Criteria:  []Criterion{{ID: "K1", Name: "x", Value: "a | b", Status: "met"}},
		Traces:    []Trace{{Requirement: Requirement{ID: "AB-001", File: "requirements/a.md", Text: "t", Status: statusAccepted}, Evidence: []string{"p.TestOne"}, Result: "pass"}},
		Inventory: []InventoryRow{{Group: "Flagi", Key: "flag.0", Verdict: "ok"}, {Group: "Flagi", Key: "flag.1"}},
		Measurement: &DocMeasurement{Date: "2026-12-05", Commit: "def5678",
			Values: map[string]string{"K8": "h", "K3": "c", "K7": "g", "K1": "a", "K2": "b"}},
		Files: []string{"evidence.json", "report.md"}}
	render := func(when string) (string, string) {
		c := *e
		c.Generated = when
		js, err := marshal(&c)
		if err != nil {
			t.Fatal(err)
		}
		return renderReport(&c), string(js)
	}
	r1, j1 := render("2026-11-25 10:00")
	for i := 0; i < 20; i++ {
		r2, j2 := render("2026-11-26 11:00")
		if strings.Replace(r1, "2026-11-25 10:00", "2026-11-26 11:00", 1) != r2 {
			t.Fatal("report differs beyond the generation time")
		}
		if strings.Replace(j1, "2026-11-25 10:00", "2026-11-26 11:00", 1) != j2 {
			t.Fatal("evidence.json differs beyond the generation time")
		}
	}
	for _, want := range []string{`a \| b`, "| `flag.0` |  |  | `ok` |  |", "| `flag.1` |  |  | **brak** |  |", "| AB-001 | przyjęte | t | `p.TestOne` | przeszedł |"} {
		if !strings.Contains(r1, want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

// REQ: EVD-005
func TestEnvironmentOfTestRuns(t *testing.T) {
	t.Setenv("GOZIPGO_LARGE", "1")
	t.Setenv("GOZIPGO_PRIVATE_CORPUS", "/secret")
	t.Setenv("GOPROXY", "https://proxy.example")
	t.Setenv("GOTOOLCHAIN", "auto")
	got := map[string]string{}
	for _, kv := range testEnv() {
		name, value, _ := strings.Cut(kv, "=")
		if _, dup := got[name]; dup {
			t.Errorf("%s set twice", name)
		}
		got[name] = value
		if strings.HasPrefix(name, "GOZIPGO_") {
			t.Errorf("test environment keeps %s", kv)
		}
	}
	// Przebiegi nie mogą pobrać modułu ani innej wersji Go.
	if got["GOPROXY"] != "off" || got["GOTOOLCHAIN"] != "local" {
		t.Errorf("GOPROXY=%q GOTOOLCHAIN=%q", got["GOPROXY"], got["GOTOOLCHAIN"])
	}
}

// Przebieg całego narzędzia na repozytorium w stanie etapu 0: bez pakietu zipreader, wymagania jako szkice,
// dokumenty z wierszami „—”, pusta lista zestawów. Pakiet powstaje, nic nie jest niespełnione, kod wyjścia 0;
// moduł tools/compare nie jest uruchamiany.
// REQ: EVD-001, EVD-005
func TestBuildOnStageZeroRepository(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test on a fixture module")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":                 "module " + pkgRoot + "\n\ngo 1.22\n",
		"requirements/README.md": "| `API` | `api.md` | a | 0 | szkic |\n| `EVD` | `evidence.md` | e | 5 | szkic |\n",
		"requirements/api.md":    "# API\n\nStatus: szkic\n\n| Id | Wymaganie |\n|---|---|\n| API-001 | wzorzec API |\n",
		"requirements/format.md": "# Inwentarz\n\nStatus: szkic\n\nTabele wypełnia etap 0.\n",
		"requirements_test.go": "package gozipgo\n\nimport \"testing\"\n\n// REQ: API-001\nfunc TestRequirementsCovered(t *testing.T) {\n" +
			"\tt.Logf(\"requirements: %d accepted, %d draft; %d referenced by tests or corpus files\", 0, 1, 1)\n}\n\nfunc TestAPISnapshot(t *testing.T) {}\n",
		"tools/fetch/go.mod":         "module " + pkgRoot + "/tools/fetch\n\ngo 1.22\n",
		"tools/fetch/main_test.go":   "package main\n\nimport \"testing\"\n\nfunc TestFetch(t *testing.T) {}\n",
		"tools/compare/go.mod":       "module " + pkgRoot + "/tools/compare\n\ngo 1.22\n",
		"tools/compare/main_test.go": "package main\n\nimport \"testing\"\n\nfunc TestCompare(t *testing.T) { t.Fatal(\"must not run\") }\n",
		"testdata/sources.json":      "{\"note\": \"x\", \"sets\": []}\n",
		"docs/testing.md":            measurementsPlaceholder,
		"docs/fuzzing.md":            fuzzJournalHeader + "| — | — | — | — | — | — | jeszcze bez kodu |\n",
		"docs/differences.md":        "## A\n\n| x | y |\n|---|---|\n| — | — |\n",
		"docs/comparison.md":         "# Comparison\n\nStatus: empty until stage 3.\n",
	})
	e, raw, err := build(root, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Runs) != 2 || e.Runs[0].Passed != 2 || e.Runs[0].Failed+len(e.Runs[0].BuildFailed) != 0 || e.Runs[1].Name != "tools/fetch" || e.Runs[1].Passed != 1 {
		t.Fatalf("runs: %+v\n%s", e.Runs, raw["go-test-module.json"])
	}
	want := map[string]string{"K1": "pending", "K2": "pending", "K3": "pending", "K4": "pending", "K5": "pending", "K6": "pending",
		"K7": "pending", "K8": "pending", "K9": "met"}
	for _, c := range e.Criteria {
		if c.Status != want[c.ID] {
			t.Errorf("%s: %s, want %s (%s)", c.ID, c.Status, want[c.ID], c.Value)
		}
	}
	if exitCode(e.Criteria) != 0 {
		t.Error("exit 1")
	}
	if len(e.Traces) != 1 || e.Traces[0].Result != "pass" || e.Traces[0].Evidence[0] != pkgRoot+".TestRequirementsCovered" {
		t.Errorf("traces: %+v", e.Traces)
	}
	if !e.Dirty || e.Commit == "" || e.GoVersion == "" || !e.DataVerify.OK || e.Generated != "2026-11-26 12:00" {
		t.Errorf("header: commit %q dirty %v go %q verify %+v generated %q", e.Commit, e.Dirty, e.GoVersion, e.DataVerify, e.Generated)
	}
	dir := filepath.Join(t.TempDir(), "package")
	if err := write(dir, e, raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range e.Files {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Size() == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	if strings.Join(e.Files, " ") != "evidence.json go-test-fetch.json go-test-module.json report.md" {
		t.Errorf("files: %v", e.Files)
	}
	if err := tarDir(dir, dir+".tar.gz"); err != nil {
		t.Error(err)
	}
}
