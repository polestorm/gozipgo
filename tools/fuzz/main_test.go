package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Fuzzery znajdowane są w plikach *_test.go modułu biblioteki, w każdym pakiecie, który już istnieje; katalogi
// narzędzi, danych i wyników są pomijane. Pakiet, którego jeszcze nie ma, po prostu nie daje żadnego fuzzera.
func TestDiscover(t *testing.T) {
	root := t.TempDir()
	fuzzTest := func(pkg string, names ...string) string {
		s := "package " + pkg + "\n\nimport \"testing\"\n"
		for _, n := range names {
			s += "\nfunc " + n + "(f *testing.F) {}\n"
		}
		return s
	}
	writeTestFile(t, filepath.Join(root, "zipreader", "fuzz_test.go"), fuzzTest("zipreader", "FuzzReaderOracle", "FuzzReader"))
	writeTestFile(t, filepath.Join(root, "internal", "inflate", "fuzz_test.go"), fuzzTest("inflate", "FuzzInflate"))
	writeTestFile(t, filepath.Join(root, "zipreader", "reader.go"), "package zipreader\n\n// func FuzzNotATest(f *testing.F) {}\n")
	writeTestFile(t, filepath.Join(root, "zipreader", "helper_test.go"), "package zipreader\n\nfunc fuzzHelper() {}\n")
	for _, skipped := range []string{"tools/x", "testdata/x", "out/x", "ref/x", ".hidden/x"} {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(skipped), "fuzz_test.go"), fuzzTest("x", "FuzzSkipped"))
	}
	got, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []fuzzer{{"./internal/inflate", "FuzzInflate"}, {"./zipreader", "FuzzReader"}, {"./zipreader", "FuzzReaderOracle"}}
	if len(got) != len(want) {
		t.Fatalf("discover: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fuzzer %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Wiersz dziennika ma układ tabeli „Przebiegi” z docs/fuzzing.md: data, commit, host i sprzęt, fuzzer, czas
// zegarowy, wykonania, wynik; dopisanie nie skleja wiersza z ostatnim wierszem pliku bez znaku nowego wiersza.
func TestAppendJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fuzzing.md")
	header := "## Przebiegi\n\n| Data | Commit | Host i sprzęt | Fuzzer | Czas zegarowy | Wykonania | Wynik |\n|---|---|---|---|---|---|---|\n| — | — | — | — | — | — | jeszcze bez kodu |"
	writeTestFile(t, path, header)
	start := time.Date(2026, 11, 27, 1, 5, 0, 0, time.Local)
	rs := []result{
		{fuzzer: fuzzer{"./zipreader", "FuzzReader"}, start: start, elapsed: 2*time.Hour + 400*time.Millisecond, execs: 123456,
			ok: true, commit: "abc1234", host: "Synology StormSyn (DS224+)"},
		{fuzzer: fuzzer{"./internal/inflate", "FuzzInflate"}, start: start, elapsed: 90 * time.Second, execs: 7,
			detail: "input testdata/fuzz/FuzzInflate/0a1b | x", commit: "abc1234", modified: true},
	}
	if err := appendJournal(path, rs); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := header + "\n" +
		"| 2026-11-27 01:05 | `abc1234` | Synology StormSyn (DS224+) | `FuzzReader` (./zipreader) | 2h0m0s | 123456 | ok |\n" +
		"| 2026-11-27 01:05 | `abc1234` (zmiany) | — | `FuzzInflate` (./internal/inflate) | 1m30s | 7 | **błąd**: input testdata/fuzz/FuzzInflate/0a1b \\| x |\n"
	if string(b) != want {
		t.Errorf("journal:\n%s\nwant:\n%s", b, want)
	}
}

// Dziennik serwera (journal.tsv z run.sh) przechodzi do docs/fuzzing.md z hostem; commit paczki zbudowanej ze
// zmianami w drzewie dostaje dopisek „(zmiany)”, więc nie liczy się do K5.
func TestImportJournal(t *testing.T) {
	dir := t.TempDir()
	tsv := filepath.Join(dir, "journal.tsv")
	writeTestFile(t, tsv, "2026-11-27 01:05\tabc1234\t./zipreader\tFuzzReader\t7200\t1000\tok\n"+
		"2026-11-27 03:05\tabc1234-dirty\t./internal/inflate\tFuzzInflate\t60\t5\tFAIL rc=1 input=x log=y\n"+
		"short\trow\n")
	journal := filepath.Join(dir, "fuzzing.md")
	if err := importJournal(tsv, journal, "NAS"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(journal)
	want := "| 2026-11-27 01:05 | `abc1234` | NAS | `FuzzReader` (./zipreader) | 2h0m0s | 1000 | ok |\n" +
		"| 2026-11-27 03:05 | `abc1234` (zmiany) | NAS | `FuzzInflate` (./internal/inflate) | 1m0s | 5 | **błąd**: FAIL rc=1 input=x log=y |\n"
	if string(b) != want {
		t.Errorf("journal:\n%s\nwant:\n%s", b, want)
	}
}

// run.sh wymienia każdy fuzzer z katalogiem pakietu i binarką, nie używa rozszerzeń bash i ogranicza liczbę
// procesów roboczych do 4 (wspólny serwer).
func TestRunScript(t *testing.T) {
	s := runScript("abc1234", []fuzzer{{"./zipreader", "FuzzReader"}, {"./internal/inflate", "FuzzInflate"}})
	for _, want := range []string{
		"#!/bin/sh\n",
		"COMMIT=abc1234\n",
		"zipreader zipreader FuzzReader\n",
		"internal/inflate internal-inflate FuzzInflate\n",
		"~/fuzz/gozipgo/abc1234",
		`if [ "$PAR" -gt 4 ]; then PAR=4; fi`,
		"-test.fuzzcachedir",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("run.sh lacks %q", want)
		}
	}
	for _, bashism := range []string{"[[", "function ", "local ", "<<<", "${BASH"} {
		if strings.Contains(s, bashism) {
			t.Errorf("run.sh contains %q", bashism)
		}
	}
}

func TestBinName(t *testing.T) {
	for pkg, want := range map[string]string{"./zipreader": "zipreader", "./internal/inflate": "internal-inflate", "./.": "root", ".": "root"} {
		if got := binName(pkg); got != want {
			t.Errorf("binName(%q) = %q, want %q", pkg, got, want)
		}
	}
}

// Dane startowe trafiają do paczki w układzie repozytorium, także gdy źródłem jest pojedynczy plik.
func TestCopyTree(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(src, "testdata", "interop", "a", "x.zip"), "x")
	writeTestFile(t, filepath.Join(src, "testdata", "hostile.json"), "{}")
	for _, d := range []string{"testdata/interop", "testdata/hostile.json"} {
		if err := copyTree(filepath.Join(src, filepath.FromSlash(d)), filepath.Join(dst, "tree", filepath.FromSlash(d))); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"testdata/interop/a/x.zip", "testdata/hostile.json"} {
		if _, err := os.Stat(filepath.Join(dst, "tree", filepath.FromSlash(f))); err != nil {
			t.Error(err)
		}
	}
}
