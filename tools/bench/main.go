// Command bench measures criterion K7: how fast gozipgo reads against archive/zip on the same archives, and how
// much memory it allocates. The archives are written by archive/zip from data this program generates, so the
// input is the same for anyone who runs it with the same Go version.
//
// It writes every run as a row of a TSV file and the summary table of docs/comparison.md. The method is in
// docs/methodology.md.
//
//	go -C tools/bench run . -root ../..
package main

import (
	"archive/zip"
	"bytes"
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

	"github.com/polestorm/gozipgo/zipreader"
)

const runs = 7

// generate returns n bytes of deterministic data of the named kind.
func generate(kind string, n int) []byte {
	b := make([]byte, n)
	x := uint32(12345)
	next := func() uint32 { x = x*1664525 + 1013904223; return x >> 8 }
	switch kind {
	case "xml": // markup with a small vocabulary, like the parts of an office document
		words := []string{"<w:p>", "</w:p>", "<w:r>", "</w:r>", "<w:t>", "</w:t>", "<w:rPr>", "<w:b/>", "</w:rPr>",
			"Lorem", "ipsum", "dolor", "sit", "amet", "zażółć", "gęślą", "jaźń", " ", " ", " ", "\n", "0", "1", "2024"}
		for i := 0; i < n; {
			i += copy(b[i:], words[next()%uint32(len(words))])
		}
	case "random": // does not compress
		for i := range b {
			b[i] = byte(next())
		}
	case "table": // numbers in rows, like a spreadsheet exported as text
		for i := 0; i < n; {
			i += copy(b[i:], fmt.Sprintf("%d;%d.%02d;%s\n", next()%100000, next()%1000, next()%100, []string{"PLN", "EUR", "USD"}[next()%3]))
		}
	case "zeros":
	}
	return b
}

// archive writes one entry with archive/zip.
func archive(data []byte, method uint16) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "data.bin", Method: method})
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		fatal(err)
	}
	return buf.Bytes()
}

func fatal(v ...any) {
	fmt.Fprintln(os.Stderr, v...)
	os.Exit(1)
}

var buffer = make([]byte, 32<<10)

func drain(r io.Reader) int64 {
	var n int64
	for {
		k, err := r.Read(buffer)
		n += int64(k)
		if err == io.EOF {
			return n
		}
		if err != nil {
			fatal(err)
		}
	}
}

func readOurs(data []byte) int64 {
	r, err := zipreader.New(bytes.NewReader(data), int64(len(data)), zipreader.Options{MaxEntries: -1, MaxRatio: -1})
	if err != nil {
		fatal(err)
	}
	var n int64
	for _, e := range r.Entries() {
		rc, err := r.Open(e)
		if err != nil {
			fatal(err)
		}
		n += drain(rc)
		rc.Close()
	}
	return n
}

func readStdlib(data []byte) int64 {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fatal(err)
	}
	var n int64
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			fatal(err)
		}
		n += drain(rc)
		rc.Close()
	}
	return n
}

func openOurs(data []byte) int64 {
	r, err := zipreader.New(bytes.NewReader(data), int64(len(data)), zipreader.Options{MaxEntries: -1})
	if err != nil {
		fatal(err)
	}
	return int64(len(r.Entries()))
}

func openStdlib(data []byte) int64 {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		fatal(err)
	}
	return int64(len(r.File))
}

// measure runs f `runs` times and returns the durations and the bytes allocated in the last run.
func measure(f func() int64) ([]time.Duration, uint64) {
	f() // warm up
	var times []time.Duration
	var alloc uint64
	for i := 0; i < runs; i++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		f()
		times = append(times, time.Since(start))
		runtime.ReadMemStats(&after)
		alloc = after.TotalAlloc - before.TotalAlloc
	}
	return times, alloc
}

func median(d []time.Duration) time.Duration {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func firstLine(name string, args ...string) string {
	out, _ := exec.Command(name, args...).CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// resolve returns path as it is when absolute, otherwise relative to the repository root.
func resolve(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func main() {
	root := flag.String("root", ".", "repository root")
	rawPath := flag.String("raw", "docs/results/bench.tsv", "raw result, relative to -root")
	docPath := flag.String("doc", "docs/comparison.md", "document whose table is rewritten, relative to -root")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}
	commit := firstLine("git", "-C", abs, "rev-parse", "--short", "HEAD")
	if firstLine("git", "-C", abs, "status", "--porcelain") != "" {
		commit += " with uncommitted changes"
	}
	machine := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOOS == "darwin" {
		machine += ", " + firstLine("sysctl", "-n", "machdep.cpu.brand_string") + ", macOS " + firstLine("sw_vers", "-productVersion")
	}
	date := time.Now().Format("2006-01-02")
	var raw, doc strings.Builder
	fmt.Fprintf(&raw, "# gozipgo tools/bench\n# date\t%s\n# commit\t%s\n# go\t%s\n# machine\t%s\n# runs\t%d\n", date, commit, runtime.Version(), machine, runs)
	raw.WriteString("case\treader\trun\tnanoseconds\tbytes of content or entries\tbytes allocated\n")

	type row struct {
		label        string
		units        int64 // bytes of content, or entries
		perEntry     bool
		ours, theirs func() int64
	}
	const size = 32 << 20
	var rows []row
	for _, kind := range []string{"xml", "table", "random", "zeros"} {
		data := generate(kind, size)
		a := archive(data, zip.Deflate)
		rows = append(rows, row{fmt.Sprintf("Deflate, %s, 32 MiB (%d bytes compressed)", kind, len(a)), size, false,
			func() int64 { return readOurs(a) }, func() int64 { return readStdlib(a) }})
	}
	stored := archive(generate("random", size), zip.Store)
	rows = append(rows, row{"Stored, 32 MiB", size, false, func() int64 { return readOurs(stored) }, func() int64 { return readStdlib(stored) }})
	// Many small entries: an office document has tens of parts, a bundle may have thousands.
	var many bytes.Buffer
	w := zip.NewWriter(&many)
	part := generate("xml", 4096)
	const entries = 100000
	for i := 0; i < entries; i++ {
		f, err := w.Create(fmt.Sprintf("parts/%03d/item%06d.xml", i%500, i))
		if err == nil {
			_, err = f.Write(part[:1024+i%3072])
		}
		if err != nil {
			fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		fatal(err)
	}
	manyData := many.Bytes()
	rows = append(rows,
		row{fmt.Sprintf("open an archive of %d entries (%d MiB)", entries, len(manyData)>>20), entries, true,
			func() int64 { return openOurs(manyData) }, func() int64 { return openStdlib(manyData) }},
		row{fmt.Sprintf("open and read all %d entries", entries), entries, true,
			func() int64 { return readOurs(manyData) }, func() int64 { return readStdlib(manyData) }})

	doc.WriteString("| Case | gozipgo | archive/zip | time, gozipgo / archive/zip | allocated, gozipgo | allocated, archive/zip |\n|---|---|---|---|---|---|\n")
	worst := 0.0
	for _, r := range rows {
		var med [2]time.Duration
		var alloc [2]uint64
		for k, f := range []func() int64{r.ours, r.theirs} {
			name := []string{"gozipgo", "archive/zip"}[k]
			times, a := measure(f)
			med[k], alloc[k] = median(times), a
			for i, d := range times {
				fmt.Fprintf(&raw, "%s\t%s\t%d\t%d\t%d\t%d\n", r.label, name, i+1, d.Nanoseconds(), r.units, a)
			}
		}
		ratio := float64(med[0]) / float64(med[1])
		if !r.perEntry {
			worst = max(worst, ratio)
		}
		speed := func(d time.Duration) string {
			if r.perEntry {
				return fmt.Sprintf("%.1f ms (%.0f ns per entry)", float64(d)/1e6, float64(d)/float64(r.units))
			}
			return fmt.Sprintf("%.0f MB/s", float64(r.units)/1e6/d.Seconds())
		}
		mem := func(a uint64) string {
			if r.perEntry {
				return fmt.Sprintf("%.1f MiB (%d bytes per entry)", float64(a)/(1<<20), a/uint64(r.units))
			}
			return fmt.Sprintf("%d KiB", a>>10)
		}
		fmt.Fprintf(&doc, "| %s | %s | %s | %.2f | %s | %s |\n", r.label, speed(med[0]), speed(med[1]), ratio, mem(alloc[0]), mem(alloc[1]))
		fmt.Printf("%-55s %.2f\n", r.label, ratio)
	}
	header := fmt.Sprintf("Measured on %s at commit `%s` with %s on %s. Median of %d runs after one warm-up run; a ratio below 1 means\ngozipgo is faster. Reading includes opening the archive and, for both readers, checking the CRC-32.\nThe largest ratio for reading content is %.2f; criterion K7 allows 1.5.\n\n", date, commit, runtime.Version(), machine, runs, worst)
	if err := os.WriteFile(resolve(abs, *rawPath), []byte(raw.String()), 0o644); err != nil {
		fatal(err)
	}
	docFile := resolve(abs, *docPath)
	old, err := os.ReadFile(docFile)
	if err != nil {
		fatal(err)
	}
	const begin, end = "<!-- bench:begin -->\n", "<!-- bench:end -->\n"
	i, j := strings.Index(string(old), begin), strings.Index(string(old), end)
	if i < 0 || j < i {
		fatal(*docPath + " has no bench:begin and bench:end markers")
	}
	if err := os.WriteFile(docFile, []byte(string(old[:i])+begin+header+doc.String()+string(old[j:])), 0o644); err != nil {
		fatal(err)
	}
}
