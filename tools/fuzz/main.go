// Command fuzz prowadzi kampanię fuzzingu gozipgo (kryterium K5, ADR 0001; narzędzie przeniesione z goxmlgo
// i dostosowane): znajduje wszystkie fuzzery w module biblioteki, uruchamia je po kolei przez `go test -fuzz`
// z równym podziałem czasu i dopisuje wynik każdego przebiegu do dziennika docs/fuzzing.md (data, commit, host
// i sprzęt, fuzzer, czas zegarowy, wykonania, wynik).
//
// Fuzzerów nie wymienia się tu z nazwy: narzędzie znajduje każdą funkcję `func FuzzX(f *testing.F)` w plikach
// *_test.go modułu głównego. Dziś jest to FuzzReader w ./zipreader; kolejne etapy dodają FuzzReaderOracle
// (./zipreader), FuzzInflate (./internal/inflate), FuzzRoundTrip (./zipwriter) i FuzzDeflate (./internal/deflate),
// a paczka i kampania obejmą je bez zmian w narzędziu. Niezmienniki opisuje tabela w docs/fuzzing.md.
//
// Użycie (z katalogu głównego repozytorium):
//
//	go -C tools/fuzz run . -root ../.. -list                    # tylko lista fuzzerów
//	go -C tools/fuzz run . -root ../.. -pack ../../out/fuzzpack # paczka dla serwera bez Go (pack.go)
//	go -C tools/fuzz run . -root ../.. -import journal.tsv      # wiersze z dziennika serwera do docs/fuzzing.md
//	go -C tools/fuzz run . -root ../.. -duration 3h             # wszystkie fuzzery, łącznie 3 h (lokalnie: patrz niżej)
//	go -C tools/fuzz run . -root ../.. -duration 21h -rounds 3  # trzy rundy po 7 h
//	go -C tools/fuzz run . -root ../.. -only Reader -duration 2m -parallel 2
//
// Kampanie biegną na serwerze (paczka z -pack w ~/fuzz/gozipgo/<commit>/, wspólna kolejka ~/fuzz/queue.sh, opis
// w docs/fuzzing.md); lokalnie tylko w nocy na zasilaniu sieciowym albo krótki przebieg sprawdzający poprawkę
// (≤ 2 min, -parallel 2).
//
// Pełne wyjście każdego przebiegu trafia do <root>/out/fuzz/. Znalezione wejście, które łamie niezmiennik, Go zapisuje
// w testdata/fuzz/<Fuzzer>/ pakietu; trzeba je przejrzeć, naprawić błąd i zostawić plik jako test regresji.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type fuzzer struct {
	pkg  string // ścieżka pakietu względem root, np. ./zipreader
	name string
}

type result struct {
	fuzzer
	start    time.Time
	elapsed  time.Duration
	execs    int64
	ok       bool
	detail   string
	logPath  string
	commit   string
	modified bool
	host     string // host i sprzęt, na którym przebieg działał (kolumna dziennika)
}

var (
	funcRe  = regexp.MustCompile(`(?m)^func (Fuzz\w+)\(f \*testing\.F\)`)
	execsRe = regexp.MustCompile(`execs: (\d+)`)
	failRe  = regexp.MustCompile(`Failing input written to (\S+)`)
)

func main() {
	root := flag.String("root", ".", "repository root")
	duration := flag.Duration("duration", 24*time.Hour, "total fuzzing time across all fuzzers and rounds")
	rounds := flag.Int("rounds", 1, "number of rounds; each round runs every fuzzer once")
	only := flag.String("only", "", "regular expression selecting fuzzers by name")
	parallel := flag.Int("parallel", min(4, max(1, runtime.NumCPU()-2)), "fuzzing workers per fuzzer (-parallel of go test); at most 4 on the shared server")
	journal := flag.String("journal", "docs/fuzzing.md", "journal file, relative to -root")
	list := flag.Bool("list", false, "only list the fuzzers")
	packDir := flag.String("pack", "", "build a self-contained package for a server without Go into this directory")
	targets := flag.String("targets", "linux/amd64", "-pack: GOOS/GOARCH list")
	withData := flag.Bool("data", true, "-pack: include the fetched test data the fuzzers read as seed inputs")
	importPath := flag.String("import", "", "append the rows of a server journal (journal.tsv) to the journal")
	host := flag.String("host", "", "journal column \"Host i sprzęt\" (default: "+serverHost+" for -import, "+localHost()+" for local runs)")
	flag.Parse()

	fuzzers, err := discover(*root)
	if err != nil {
		fail(err)
	}
	if *only != "" {
		re, err := regexp.Compile(*only)
		if err != nil {
			fail(err)
		}
		var sel []fuzzer
		for _, f := range fuzzers {
			if re.MatchString(f.name) {
				sel = append(sel, f)
			}
		}
		fuzzers = sel
	}
	if *list {
		// Lista bywa pusta, dopóki pakiety z fuzzerami nie powstały: to nie błąd.
		for _, f := range fuzzers {
			fmt.Printf("%s %s\n", f.pkg, f.name)
		}
		if len(fuzzers) == 0 {
			fmt.Fprintln(os.Stderr, "fuzz: no fuzzers found")
		}
		return
	}
	if *importPath != "" {
		if *host == "" {
			*host = serverHost
		}
		if err := importJournal(*importPath, journalPath(*root, *journal), *host); err != nil {
			fail(err)
		}
		return
	}
	if len(fuzzers) == 0 {
		fail(fmt.Errorf("no fuzzers selected"))
	}
	if *packDir != "" {
		if err := pack(*root, *packDir, fuzzers, strings.Split(*targets, ","), *withData); err != nil {
			fail(err)
		}
		return
	}
	slot := (*duration / time.Duration(len(fuzzers)**rounds)).Round(time.Second)
	if slot < 10*time.Second {
		fail(fmt.Errorf("%v per fuzzer is too short", slot))
	}
	if *host == "" {
		*host = localHost()
	}
	commit, modified := gitState(*root)
	fmt.Printf("campaign: %d fuzzers × %d round(s) × %v, commit %s%s, -parallel %d\n",
		len(fuzzers), *rounds, slot, commit, dirtyMark(modified), *parallel)
	logDir := filepath.Join(*root, "out", "fuzz")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fail(err)
	}
	var results []result
	failed := 0
	for r := 0; r < *rounds; r++ {
		for _, f := range fuzzers {
			res := run(*root, logDir, f, slot, *parallel)
			res.commit, res.modified, res.host = commit, modified, *host
			results = append(results, res)
			status := "ok"
			if !res.ok {
				status = "FAIL: " + res.detail
				failed++
			}
			fmt.Printf("%-22s %-20s %8v %12d execs  %s\n", f.pkg, f.name, res.elapsed.Round(time.Second), res.execs, status)
			if err := appendJournal(journalPath(*root, *journal), []result{res}); err != nil {
				fail(err)
			}
		}
	}
	var total time.Duration
	var execs int64
	for _, r := range results {
		total += r.elapsed
		execs += r.execs
	}
	fmt.Printf("total %v, %d execs, %d failure(s); journal: %s\n", total.Round(time.Second), execs, failed, *journal)
	if failed > 0 {
		os.Exit(1)
	}
}

// serverHost to opis serwera kampanii z docs/fuzzing.md; inny host podaje się przez -host.
const serverHost = "Synology StormSyn (DS224+, Celeron J4125, 4 rdzenie)"

// localHost opisuje maszynę przebiegu lokalnego bez jej nazwy sieciowej (dziennik jest publiczny).
func localHost() string {
	return fmt.Sprintf("lokalnie (%s/%s, %d rdzeni)", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fuzz:", err)
	os.Exit(1)
}

// journalPath zwraca ścieżkę dziennika: bezwzględną bez zmian, względną względem root.
func journalPath(root, journal string) string {
	if filepath.IsAbs(journal) {
		return journal
	}
	return filepath.Join(root, journal)
}

func dirtyMark(m bool) string {
	if m {
		return " (with uncommitted changes)"
	}
	return ""
}

// discover znajduje fuzzery w module biblioteki (bez tools/ i testdata/).
func discover(root string) ([]fuzzer, error) {
	var out []fuzzer
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			base := d.Name()
			if rel != "." && (base == "tools" || base == "testdata" || base == "out" || base == "ref" || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range funcRe.FindAllSubmatch(b, -1) {
			out = append(out, fuzzer{pkg: "./" + filepath.ToSlash(filepath.Dir(rel)), name: string(m[1])})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].pkg != out[j].pkg {
			return out[i].pkg < out[j].pkg
		}
		return out[i].name < out[j].name
	})
	return out, err
}

// gitState zwraca skrót bieżącego commitu i to, czy śledzone pliki poza dziennikiem mają niezatwierdzone zmiany.
func gitState(root string) (string, bool) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown", true
	}
	commit := strings.TrimSpace(string(out))
	st, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=no").Output()
	if err != nil {
		return commit, true
	}
	modified := false
	for _, line := range strings.Split(strings.TrimSpace(string(st)), "\n") {
		if line != "" && !strings.HasSuffix(line, "docs/fuzzing.md") {
			modified = true
		}
	}
	return commit, modified
}

func run(root, logDir string, f fuzzer, d time.Duration, parallel int) result {
	res := result{fuzzer: f, start: time.Now()}
	res.logPath = filepath.Join(logDir, res.start.Format("20060102-150405")+"-"+f.name+".log")
	logf, err := os.Create(res.logPath)
	if err != nil {
		res.detail = err.Error()
		return res
	}
	defer logf.Close()
	cmd := exec.Command("go", "test", f.pkg, "-run", "^$", "-fuzz", "^"+f.name+"$",
		"-fuzztime", d.String(), "-parallel", strconv.Itoa(parallel))
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.detail = err.Error()
		return res
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		res.detail = err.Error()
		return res
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var failInput string
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(logf, line)
		if m := execsRe.FindStringSubmatch(line); m != nil {
			if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > res.execs {
				res.execs = n
			}
		}
		if m := failRe.FindStringSubmatch(line); m != nil {
			failInput = m[1]
		}
	}
	err = cmd.Wait()
	res.elapsed = time.Since(res.start)
	res.ok = err == nil
	if !res.ok {
		res.detail = "see " + res.logPath
		if failInput != "" {
			res.detail = "input " + failInput
		}
	}
	return res
}

// appendJournal dopisuje wiersze do tabeli „Przebiegi”, którą kończy się docs/fuzzing.md. Kolumny: data, commit
// (z dopiskiem „(zmiany)”, gdy drzewo robocze miało niezatwierdzone zmiany), host i sprzęt, fuzzer z pakietem, czas
// zegarowy, wykonania, wynik. Ten sam układ czyta tools/evidence.
func appendJournal(path string, rs []result) error {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(old) > 0 && old[len(old)-1] != '\n' {
		// plik bez końcowego znaku nowego wiersza: bez tego pierwszy wiersz skleiłby się z ostatnim
		fmt.Fprintln(f)
	}
	for _, r := range rs {
		status := "ok"
		if !r.ok {
			status = "**błąd**: " + cell(r.detail)
		}
		host := r.host
		if host == "" {
			host = "—"
		}
		fmt.Fprintf(f, "| %s | `%s`%s | %s | `%s` (%s) | %s | %d | %s |\n", r.start.Format("2006-01-02 15:04"), r.commit,
			map[bool]string{true: " (zmiany)", false: ""}[r.modified], cell(host), r.name, r.pkg,
			r.elapsed.Round(time.Second), r.execs, status)
	}
	return f.Close()
}

// cell zabezpiecza tekst wstawiany do komórki tabeli Markdown.
func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "|", "\\|")
}
