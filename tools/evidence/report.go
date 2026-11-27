package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Evidence to cała zawartość pakietu poza surowymi wynikami przebiegów. Dla tego samego commitu i tych samych
// danych testowych różni się tylko polem Generated (EVD-004).
type Evidence struct {
	Generated           string          `json:"generated"`
	Commit              string          `json:"commit"`
	Dirty               bool            `json:"dirty"`
	GoVersion           string          `json:"go_version"`
	Platform            string          `json:"platform"`
	Criteria            []Criterion     `json:"criteria"`
	Runs                []*TestRun      `json:"runs"`
	RequirementProblems []string        `json:"requirement_problems,omitempty"`
	Traces              []Trace         `json:"traces"`
	UnknownRefs         []Ref           `json:"unknown_refs,omitempty"`
	Inventory           []InventoryRow  `json:"inventory"`
	InventoryProblems   []string        `json:"inventory_problems,omitempty"`
	Ratchets            []Ratchet       `json:"ratchets,omitempty"`
	Fuzz                []FuzzRun       `json:"fuzz"`
	FuzzHours           float64         `json:"fuzz_hours_counted"`
	Measurement         *DocMeasurement `json:"measurement,omitempty"`
	Sources             []SourceSet     `json:"sources"`
	SourcesSHA          string          `json:"sources_json_sha256"`
	DataVerify          DataVerify      `json:"data_verify"`
	ADRs                []ADR           `json:"adrs"`
	Differences         []DiffSection   `json:"differences"`
	Files               []string        `json:"files"`
}

// Criterion to jeden wiersz tabeli K1–K9.
type Criterion struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Target string `json:"target"`
	// Status: met, not_met, pending.
	Status string `json:"status"`
	Source string `json:"source"`
}

// Inputs to dane, z których liczymy kryteria. Runs[0] to przebieg modułu głównego.
type Inputs struct {
	Runs                []*TestRun
	Traces              []Trace
	UnknownRefs         []Ref
	RequirementProblems []string
	Inventory           []InventoryRow
	InventoryProblems   []string
	FuzzHours           time.Duration
	FuzzFailures        int
	Measurement         *DocMeasurement
	Differences         []DiffSection
	DataVerify          DataVerify
}

// interopExceptionsSection to znacznik w tytule sekcji docs/differences.md, która wymienia wyjątki korpusu zgodności.
const interopExceptionsSection = "(K2)"

const (
	pkgRoot   = "github.com/polestorm/gozipgo"
	pkgReader = pkgRoot + "/zipreader"
	// pkgProject to pakiet testów całego repozytorium (wzorce API, pokrycie wymagań, katalog błędów, importy).
	pkgProject = pkgRoot + "/internal/project"
)

// Wiersze, które testy wypisują przez t.Logf; z nich biorą się liczby K1, K2 i K4.
var (
	inventoryLogRe    = regexp.MustCompile(`format inventory: (\d+) rows, (\d+) verified, (\d+) pending`)
	interopLogRe      = regexp.MustCompile(`interop corpus: (\d+) archives, (\d+) read as the manifest says, (\d+) listed exceptions`)
	requirementsLogRe = regexp.MustCompile(`requirements: (\d+) accepted, (\d+) draft; (\d+) referenced`)
)

const notMeasured = "jeszcze nie zmierzono"

func (in Inputs) result(key string) string {
	for _, r := range in.Runs {
		if a := r.Result(key); a != "" {
			return a
		}
	}
	return ""
}

func (in Inputs) output(key string) []string {
	for _, r := range in.Runs {
		if o := r.Output(key); len(o) > 0 {
			return o
		}
	}
	return nil
}

// byName zwraca wynik testu o danej nazwie w module głównym, w dowolnym pakiecie.
func (in Inputs) byName(name string) string {
	if len(in.Runs) == 0 {
		return ""
	}
	return in.Runs[0].ResultByName(name)
}

// documented zwraca wartość kryterium zapisaną w tabeli „Pomiary” w docs/testing.md, jej stan i opis źródła.
// Stan pomiaru spoza `go test` zapisuje w komórce ten, kto mierzył: słowo „spełnione” albo „niespełnione”; bez
// niego wartość jest pokazana, a kryterium czeka.
func (in Inputs) documented(id string) (value, status, source string) {
	m := in.Measurement
	if m == nil {
		return "", "pending", "docs/testing.md (tabela „Pomiary” nie ma jeszcze datowanego wiersza)"
	}
	v, found := m.Values[id]
	if !found || v == "" || v == "—" {
		return "", "pending", fmt.Sprintf("docs/testing.md (pomiar %s, commit %s: bez wartości %s)", m.Date, orDash(m.Commit), id)
	}
	source = fmt.Sprintf("docs/testing.md (pomiar %s, commit %s)", m.Date, orDash(m.Commit))
	switch lower := strings.ToLower(v); {
	case strings.Contains(lower, "niespełnione"):
		return v, "not_met", source
	case strings.Contains(lower, "spełnione"):
		return v, "met", source
	}
	return v, "pending", source
}

// combine łączy stan z przebiegu testów (pass, fail, skip albo "") ze stanem pomiaru z dokumentów.
func combine(testResult, docStatus string) string {
	switch {
	case testResult == "fail" || docStatus == "not_met":
		return "not_met"
	case testResult == "pass" && docStatus == "met":
		return "met"
	}
	return "pending"
}

// buildCriteria liczy tabelę K1–K9 (EVD-003). Wartości z przebiegów testów biorą się z ich wyjścia; pomiary spoza
// `go test` (K3, K5, K7, K8 u konsumenta) z docs/fuzzing.md i z tabeli „Pomiary” w docs/testing.md. Kryterium,
// którego testu albo pomiaru jeszcze nie ma, czeka („pending”), a nie jest niespełnione.
func buildCriteria(in Inputs) []Criterion {
	var out []Criterion

	// K1
	k1 := Criterion{ID: "K1", Name: "inwentarz formatu", Target: "każdy rekord, pole, bit flag, metoda i pole dodatkowe z zakresu specyfikacji ma werdykt sprawdzony archiwum; nic nie czeka",
		Source: "TestFormatInventory (zipreader), requirements/format.md"}
	noVerdict := 0
	for _, r := range in.Inventory {
		if r.Verdict == "" {
			noVerdict++
		}
	}
	inFile := fmt.Sprintf("requirements/format.md: %d wierszy, bez werdyktu %d", len(in.Inventory), noVerdict)
	n, found := findCounts(in.output(pkgReader+".TestFormatInventory"), inventoryLogRe)
	switch res := in.result(pkgReader + ".TestFormatInventory"); {
	case res == "" || res == "skip" || !found:
		k1.Value, k1.Status = notMeasured+" (TestFormatInventory "+orNone(res)+"); "+inFile, "pending"
		if res == "fail" || len(in.InventoryProblems) > 0 {
			k1.Status = "not_met"
		}
	default:
		k1.Value = fmt.Sprintf("wiersze %d, sprawdzone %d, czekające %d; %s", n[0], n[1], n[2], inFile)
		switch {
		case res != "pass" || len(in.InventoryProblems) > 0 || n[0] != len(in.Inventory):
			k1.Status = "not_met"
		case n[0] > 0 && n[2] == 0 && n[1] == n[0]:
			k1.Status = "met"
		default:
			k1.Status = "pending"
		}
		if n[0] != len(in.Inventory) {
			k1.Value += "; **test i plik różnią się liczbą wierszy**"
		}
	}
	if len(in.InventoryProblems) > 0 {
		k1.Value += fmt.Sprintf("; problemy w tabelach: %d", len(in.InventoryProblems))
	}
	out = append(out, k1)

	// K2
	k2 := Criterion{ID: "K2", Name: "korpus zgodności", Target: "100% archiwów czytanych w trybie ścisłym z listą i treścią z manifestu; wyjątek tylko wymieniony w docs/differences.md",
		Source: "TestInteropCorpus (zipreader), testdata/interop/manifest.json, docs/differences.md"}
	n, found = findCounts(in.output(pkgReader+".TestInteropCorpus"), interopLogRe)
	switch res := in.result(pkgReader + ".TestInteropCorpus"); {
	case res == "fail":
		k2.Value, k2.Status = "TestInteropCorpus nie przeszedł", "not_met"
	case res != "pass" || !found:
		k2.Value, k2.Status = notMeasured+" (TestInteropCorpus "+orNone(res)+")", "pending"
	default:
		described := 0
		for _, d := range in.Differences {
			if strings.Contains(d.Title, interopExceptionsSection) {
				described += d.Rows
			}
		}
		k2.Value = fmt.Sprintf("archiwa %d, zgodne z manifestem %d, wyjątki z manifestu %d; wiersze wyjątków w docs/differences.md: %d",
			n[0], n[1], n[2], described)
		switch {
		case n[0] == 0:
			k2.Status = "pending"
		case n[1]+n[2] != n[0]:
			k2.Status = "not_met"
		case n[2] > 0 && described == 0:
			// wyjątek liczy się tylko wtedy, gdy docs/differences.md podaje jego powód (ADR 0001, K2)
			k2.Value += " (**wyjątki nieopisane**)"
			k2.Status = "not_met"
		default:
			k2.Status = "met"
		}
	}
	out = append(out, k2)

	// K3
	k3 := Criterion{ID: "K3", Name: "zgodność z czarnymi skrzynkami", Target: "korpus zgodności: ta sama lista i treść co archive/zip, Info-ZIP, 7-Zip i zipfile (100%); korpus wrogi: każde archiwum odrzucone oczekiwanym kodem; każda różnica w docs/differences.md"}
	k3.Value, k3.Status, k3.Source = in.documented("K3")
	k3.Value = orNotMeasured(k3.Value)
	k3.Source += ", docs/comparison.md, tools/compare"
	out = append(out, k3)

	// K4
	k4 := Criterion{ID: "K4", Name: "wymagania i API", Target: "każde przyjęte wymaganie ma test, który przechodzi; każde REQ: wskazuje istniejące wymaganie; API równe wzorcowi; katalog kodów błędów zgodny z kodem",
		Source: "requirements/*.md, odwołania REQ: w testach, TestRequirementsCovered, TestAPISnapshot, TestErrorCatalog (moduł główny), macierz śledzenia w tym pakiecie"}
	counts := map[string]int{}
	accepted, draft := 0, 0
	for _, t := range in.Traces {
		if !t.Accepted() {
			draft++
			if t.Result == "fail" {
				counts["fail"]++
			}
			continue
		}
		accepted++
		counts[t.Result]++
	}
	k4.Value = fmt.Sprintf("%d przyjętych wymagań, %d szkiców; przyjęte: przeszło %d, na żądanie %d, pominięte %d, bez testu %d, nieprzypisane %d; nie przeszło (także szkice) %d; odwołania do nieznanych wymagań: %d",
		accepted, draft, counts["pass"], counts["manual"], counts["skip"], counts["missing"], counts["unresolved"], counts["fail"], len(in.UnknownRefs))
	gate := map[string]string{}
	gateFailed, gateOpen := false, false
	for _, name := range []string{"TestRequirementsCovered", "TestAPISnapshot", "TestErrorCatalog"} {
		gate[name] = in.result(pkgProject + "." + name)
		gateFailed = gateFailed || gate[name] == "fail"
		gateOpen = gateOpen || gate[name] != "pass"
		k4.Value += fmt.Sprintf("; %s %s", name, orNone(gate[name]))
	}
	mismatch := false
	if n, found := findCounts(in.output(pkgProject+".TestRequirementsCovered"), requirementsLogRe); found && (n[0] != accepted || n[1] != draft) {
		mismatch = true
		k4.Value += fmt.Sprintf("; **TestRequirementsCovered liczy inaczej: %d przyjętych, %d szkiców**", n[0], n[1])
	}
	if len(in.RequirementProblems) > 0 {
		k4.Value += fmt.Sprintf("; problemy w plikach wymagań: %d", len(in.RequirementProblems))
	}
	switch {
	case counts["fail"] > 0 || counts["missing"] > 0 || len(in.UnknownRefs) > 0 || len(in.RequirementProblems) > 0 || gateFailed || mismatch:
		k4.Status = "not_met"
	case counts["skip"] > 0 || counts["unresolved"] > 0 || gateOpen || accepted == 0:
		k4.Status = "pending"
	default:
		k4.Status = "met"
	}
	out = append(out, k4)

	// K5
	k5 := Criterion{ID: "K5", Name: "odporność", Target: "fuzzing ≥ 24 h łącznie na kandydacie bez paniki i bez naruszenia niezmienników",
		Source: "docs/fuzzing.md, tabela „Przebiegi” (przebiegi z wynikiem ok na bieżącym commicie albo na commitach bez zmian kodu biblioteki, bez dopisku „(zmiany)”)"}
	switch {
	case in.FuzzFailures > 0:
		k5.Value, k5.Status = fmt.Sprintf("przebiegi zakończone błędem na kodzie kandydata: %d", in.FuzzFailures), "not_met"
	case in.FuzzHours == 0:
		k5.Value, k5.Status = notMeasured+" (0 z 24 h)", "pending"
	case in.FuzzHours.Hours() >= 24:
		k5.Value, k5.Status = fmt.Sprintf("fuzzing %s z 24 h", fmtHours(in.FuzzHours)), "met"
	default:
		k5.Value, k5.Status = fmt.Sprintf("fuzzing %s z 24 h", fmtHours(in.FuzzHours)), "pending"
	}
	out = append(out, k5)

	// K6
	k6 := Criterion{ID: "K6", Name: "bezpieczeństwo", Target: "wrogie archiwa odrzucone, każde w < 1 s i < 64 MB; biblioteka nie importuje os, net ani archive/zip; czas liniowy",
		Source: "TestHostileArchives, TestNoIOImports, TestPathological (moduł główny), requirements/safety.md"}
	var parts []string
	pass, fail := 0, 0
	k6Tests := []string{"TestHostileArchives", "TestNoIOImports", "TestPathological"}
	for _, name := range k6Tests {
		res := in.byName(name)
		parts = append(parts, name+" "+orNone(res))
		switch res {
		case "pass":
			pass++
		case "fail":
			fail++
		}
	}
	k6.Value = strings.Join(parts, "; ")
	switch {
	case fail > 0:
		k6.Status = "not_met"
	case pass == len(k6Tests):
		k6.Status = "met"
	default:
		k6.Status = "pending"
		if pass == 0 {
			k6.Value = notMeasured + ": " + k6.Value
		}
	}
	out = append(out, k6)

	// K7
	k7 := Criterion{ID: "K7", Name: "wydajność i pamięć", Target: "rozpakowanie nie wolniejsze niż 1,5× archive/zip; pamięć stała względem rozmiaru treści; archiwum ZIP64 > 4 GB czytane w < 64 MB"}
	large := in.byName("TestLargeArchive")
	v, docStatus, source := in.documented("K7")
	k7.Value = fmt.Sprintf("TestLargeArchive %s; pomiar: %s", orNone(large), orNotMeasured(v))
	k7.Status = combine(large, docStatus)
	k7.Source = "TestLargeArchive (moduł główny); " + source + ", tools/bench, docs/results/"
	out = append(out, k7)

	// K8
	k8 := Criterion{ID: "K8", Name: "powtarzalność i konsument", Target: "te same wyniki w 10 przebiegach i na każdym systemie z CI; godocxgo czyta swój korpus bez zmian w gozipgo"}
	det := in.byName("TestDeterministic")
	v, docStatus, source = in.documented("K8")
	k8.Value = fmt.Sprintf("TestDeterministic %s; konsument: %s", orNone(det), orNotMeasured(v))
	k8.Status = combine(det, docStatus)
	k8.Source = "TestDeterministic (moduł główny); " + source
	out = append(out, k8)

	// K9
	failed := 0
	for _, r := range in.Runs {
		failed += r.Failed + len(r.BuildFailed)
	}
	k9 := Criterion{ID: "K9", Name: "pakiet dowodów", Target: "wymagania → testy → wynik ostatniego przebiegu; tabela K1–K9 ze źródłem każdej liczby; dane testowe zgodne z manifestem",
		Source: "ten pakiet (tools/evidence, requirements/evidence.md)"}
	k9.Value = fmt.Sprintf("przebiegi go test: %d nieudanych testów lub pakietów; dane pobierane zgodne z manifestem: %s; macierz: %d wymagań; inwentarz: %d wierszy",
		failed, okWord(in.DataVerify.OK), len(in.Traces), len(in.Inventory))
	switch {
	case failed > 0 || counts["fail"] > 0 || counts["missing"] > 0:
		k9.Status = "not_met"
	case !in.DataVerify.OK:
		k9.Status = "pending"
	default:
		k9.Status = "met"
	}
	out = append(out, k9)
	return out
}

func fmtHours(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1f h", d.Hours())
}

func orNone(s string) string {
	if s == "" {
		return "brak w przebiegu"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orNotMeasured(s string) string {
	if s == "" {
		return notMeasured
	}
	return s
}

func okWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "nie"
}

var statusPL = map[string]string{"met": "spełnione", "not_met": "**niespełnione**", "pending": "czeka",
	"pass": "przeszedł", "fail": "**nie przeszedł**", "skip": "**niespełnione: test pominięty (brak danych)**",
	"missing": "**niespełnione: brak testu**", "unresolved": "**nieprzypisany do testu z przebiegu**",
	"manual": "na żądanie (benchmark albo zmienna " + envPrefix + "*)", "draft": "szkic bez testu"}

// renderReport składa report.md (po polsku).
func renderReport(e *Evidence) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	dirty := ""
	if e.Dirty {
		dirty = " (ze zmianami w drzewie roboczym — pakiet nie dotyczy czystego commitu)"
	}
	p("# Pakiet dowodów gozipgo\n\n")
	p("Commit `%s`%s; %s, %s. Wygenerowano %s przez `go -C tools/evidence run . -root ../..` (requirements/evidence.md).\n\n",
		e.Commit, dirty, e.GoVersion, e.Platform, e.Generated)

	p("## Kryteria gotowości (ADR 0001)\n\n| # | Kryterium | Wynik | Cel | Stan | Źródło |\n|---|---|---|---|---|---|\n")
	for _, c := range e.Criteria {
		p("| %s | %s | %s | %s | %s | %s |\n", c.ID, c.Name, cell(c.Value), cell(c.Target), statusPL[c.Status], cell(c.Source))
	}

	p("\n## Przebiegi testów\n\n| Przebieg | Polecenie | Przeszło | Nie przeszło | Pominięte |\n|---|---|---|---|---|\n")
	for _, r := range e.Runs {
		p("| %s | `%s` | %d | %d | %d |\n", r.Name, r.Command, r.Passed, r.Failed+len(r.BuildFailed), r.Skipped)
	}
	p("\n")
	for _, r := range e.Runs {
		for _, t := range r.FailedTests {
			p("- nie przeszedł: `%s`\n", t)
		}
		for _, t := range r.BuildFailed {
			p("- pakiet bez wyniku (błąd kompilacji?): `%s`\n", t)
		}
		for _, t := range r.SkippedTests {
			p("- pominięty: `%s`\n", t)
		}
	}

	p("\n## Macierz śledzenia (K4)\n")
	if len(e.RequirementProblems) > 0 {
		p("\nProblemy w plikach wymagań:\n\n")
		for _, s := range e.RequirementProblems {
			p("- %s\n", s)
		}
	}
	file := ""
	for _, t := range e.Traces {
		if t.File != file {
			file = t.File
			p("\n### %s\n\n| Wymaganie | Status | Treść | Dowód | Wynik |\n|---|---|---|---|---|\n", file)
		}
		var ev []string
		for _, x := range t.Evidence {
			ev = append(ev, "`"+x+"`")
		}
		p("| %s | %s | %s | %s | %s |\n", t.ID, orDash(t.Status), keepEscapes(t.Text), strings.Join(ev, "<br>"), statusPL[t.Result])
	}
	if len(e.UnknownRefs) > 0 {
		p("\nOdwołania do nieistniejących wymagań:\n\n")
		for _, r := range e.UnknownRefs {
			p("- `%s` w `%s` (%s)\n", r.ID, r.File, orDash(r.Test))
		}
	}

	p("\n## Inwentarz formatu (K1)\n\nŹródło: `requirements/format.md`; werdykt sprawdza `TestFormatInventory`.\n")
	if len(e.Inventory) == 0 {
		p("\nTabele inwentarza nie mają jeszcze wierszy.\n")
	}
	for _, s := range e.InventoryProblems {
		p("\n- %s", s)
	}
	if len(e.InventoryProblems) > 0 {
		p("\n")
	}
	group, first := "", true
	for _, r := range e.Inventory {
		if first || r.Group != group {
			group, first = r.Group, false
			p("\n### %s\n\n| Klucz | APPNOTE | Opis | Werdykt | Etap |\n|---|---|---|---|---|\n", orDash(group))
		}
		verdict := "**brak**"
		if r.Verdict != "" {
			verdict = "`" + r.Verdict + "`"
		}
		p("| `%s` | %s | %s | %s | %s |\n", r.Key, keepEscapes(r.Section), keepEscapes(r.Description), verdict, keepEscapes(r.Stage))
	}

	if len(e.Ratchets) > 0 {
		p("\n## Zapadki\n\n| Plik | W zakresie | Przechodzi | Tryb | Na liście |\n|---|---|---|---|---|\n")
		for _, r := range e.Ratchets {
			p("| `%s` | %d | %d | %s | %d |\n", r.File, r.InScope, r.Passing, r.Mode, r.Listed)
		}
	}

	p("\n## Fuzzing (K5)\n\nLiczone do K5: %s (commit `%s` i commity bez zmian kodu biblioteki; reguły w docs/fuzzing.md).\n\n",
		fmtHours(time.Duration(e.FuzzHours*float64(time.Hour))), e.Commit)
	p("| Data | Commit | Host i sprzęt | Fuzzer | Czas zegarowy | Wykonania | Wynik | Liczony |\n|---|---|---|---|---|---|---|---|\n")
	for _, f := range e.Fuzz {
		counted := "nie"
		if f.Counted {
			counted = "tak"
		}
		commit := "`" + f.Commit + "`"
		if f.Dirty {
			commit += " (zmiany)"
		}
		name := "`" + f.Fuzzer + "`"
		if f.Package != "" {
			name += " (" + f.Package + ")"
		}
		p("| %s | %s | %s | %s | %s | %s | %s | %s |\n", f.Date, commit, keepEscapes(f.Host), name, f.Duration, f.Execs, keepEscapes(f.Result), counted)
	}
	if len(e.Fuzz) == 0 {
		p("| — | — | — | — | — | — | dziennik nie ma jeszcze przebiegów | — |\n")
	}

	p("\n## Pomiary spoza `go test` (K3, K7, K8)\n\n")
	if m := e.Measurement; m == nil {
		p("Tabela „Pomiary” w `docs/testing.md` nie ma jeszcze datowanego wiersza.\n")
	} else {
		p("Ostatni wiersz tabeli „Pomiary” w `docs/testing.md`: %s, commit `%s`.\n\n| Kryterium | Wartość |\n|---|---|\n", m.Date, orDash(m.Commit))
		for _, id := range []string{"K1", "K2", "K3", "K4", "K5", "K6", "K7", "K8", "K9"} {
			if v, ok := m.Values[id]; ok {
				p("| %s | %s |\n", id, keepEscapes(orDash(v)))
			}
		}
		if m.Notes != "" {
			p("\nUwagi: %s\n", m.Notes)
		}
	}

	p("\n## Dane testowe\n\nManifest `testdata/sources.json`, SHA-256 `%s`.\n\n| Zestaw | Rodzaj | Katalog | Pliki | SHA-256 archiwum | SHA-256 drzewa | Tylko lokalnie |\n|---|---|---|---|---|---|---|\n", e.SourcesSHA)
	for _, s := range e.Sources {
		p("| %s | %s | `%s` | %d | %s | %s | %d |\n", s.Name, s.Kind, s.Dest, s.Files, orDash(s.SHA256), orDash(s.TreeSHA256), s.LocalOnly)
	}
	if len(e.Sources) == 0 {
		p("| — | — | — | — | — | — | — |\n")
	}
	p("\nZgodność pobranych danych z manifestem: %s\n\n", okWord(e.DataVerify.OK))
	for _, l := range e.DataVerify.Lines {
		p("- %s\n", l)
	}

	p("\n## Decyzje (docs/adr)\n\n| Plik | Tytuł | Status |\n|---|---|---|\n")
	for _, a := range e.ADRs {
		p("| `%s` | %s | %s |\n", a.File, cell(a.Title), cell(orDash(a.Status)))
	}

	p("\n## Różnice (docs/differences.md)\n\n| Sekcja | Wiersze |\n|---|---|\n")
	for _, d := range e.Differences {
		p("| %s | %d |\n", cell(d.Title), d.Rows)
	}

	p("\n## Pliki pakietu\n\n")
	for _, f := range e.Files {
		p("- `%s`\n", f)
	}
	return b.String()
}

// cell zabezpiecza tekst wstawiany do komórki tabeli Markdown.
func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", "\\|")
}

// keepEscapes przenosi komórkę przeczytaną z tabeli Markdown, w której „\|” już jest zabezpieczone.
func keepEscapes(s string) string {
	return strings.ReplaceAll(s, "\n", " ")
}
