// Command compare measures criterion K3: what gozipgo and other ZIP readers do with the archives of the
// conformance corpus (testdata/interop) and of the hostile corpus (testdata/hostile.json, built by the tests of
// the repository). The other readers are black boxes: archive/zip of the Go that builds this tool, in process,
// and whatever of Info-ZIP unzip, 7-Zip, Python zipfile and bsdtar (libarchive) is installed, as commands.
//
// It writes the raw result as TSV and the tables of docs/comparison.md. The method is in docs/methodology.md.
//
//	go -C tools/compare run . -root ../..
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/polestorm/gozipgo/zipreader"
)

// cp852High is IBM code page 852 for the bytes 0x80 to 0xFF (Unicode Consortium, MAPPINGS/VENDORS/MICSFT/PC/CP852.TXT):
// the conformance corpus has an archive made by 7-Zip on a Polish Windows, with names in this code page.
const cp852High = "ÇüéâäůćçłëŐőîŹÄĆÉĹĺôöĽľŚśÖÜŤťŁ×čáíóúĄąŽžĘę¬źČş«»░▒▓│┤ÁÂĚŞ╣║╗╝Żż┐└┴┬├─┼Ăă╚╔╩╦╠═╬¤đĐĎËďŇÍÎě┘┌█▄ŢŮ▀ÓßÔŃńňŠšŔÚŕŰýÝţ´\u00ad˝˛ˇ˘§÷¸°¨˙űŘř■\u00a0"

// outcome is what one reader did with one archive.
type outcome struct {
	Verdict string            // "reads", "reads with a warning", "rejects", "timeout", "not installed"
	Detail  string            // error code, exit status or message
	Files   map[string]string // name → SHA-256 of the content, for readers that list names
	Sums    []string          // SHA-256 of every file read, sorted
	Escaped int               // files written outside the target directory
}

func (o outcome) digest() string {
	h := sha256.Sum256([]byte(strings.Join(o.Sums, "\n")))
	return hex.EncodeToString(h[:4])
}

type candidate struct {
	Name    string
	Version string
	Run     func(path string, data []byte, opts zipreader.Options) outcome
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func runGozipgo(_ string, data []byte, opts zipreader.Options) outcome {
	r, err := zipreader.New(bytes.NewReader(data), int64(len(data)), opts)
	if err != nil {
		return rejected(err)
	}
	o := outcome{Verdict: "reads", Files: map[string]string{}}
	for _, e := range r.Entries() {
		if e.IsDir {
			continue
		}
		rc, err := r.Open(e)
		if err != nil {
			return rejected(err)
		}
		body, err := io.ReadAll(rc)
		if err != nil {
			return rejected(err)
		}
		o.Files[e.Name] = sum(body)
		o.Sums = append(o.Sums, sum(body))
	}
	sort.Strings(o.Sums)
	return o
}

func rejected(err error) outcome {
	if e, ok := err.(*zipreader.Error); ok {
		return outcome{Verdict: "rejects", Detail: e.Code}
	}
	return outcome{Verdict: "rejects", Detail: err.Error()}
}

func runStdlib(_ string, data []byte, _ zipreader.Options) outcome {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return outcome{Verdict: "rejects", Detail: err.Error()}
	}
	o := outcome{Verdict: "reads", Files: map[string]string{}}
	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return outcome{Verdict: "rejects", Detail: err.Error()}
		}
		body, err := io.ReadAll(io.LimitReader(rc, 1<<30))
		if err != nil {
			return outcome{Verdict: "rejects", Detail: err.Error()}
		}
		o.Files[f.Name] = sum(body)
		o.Sums = append(o.Sums, sum(body))
	}
	sort.Strings(o.Sums)
	return o
}

const pythonScript = `
import sys, zipfile, hashlib, json
try:
    z = zipfile.ZipFile(sys.argv[1])
    out = []
    for i in z.infolist():
        if i.is_dir():
            continue
        out.append([i.filename, hashlib.sha256(z.read(i)).hexdigest()])
    print(json.dumps({"ok": out}))
except Exception as e:
    print(json.dumps({"error": type(e).__name__ + ": " + str(e)[:160]}))
`

const commandTimeout = 120 * time.Second

func runPython(python string) func(string, []byte, zipreader.Options) outcome {
	return func(path string, _ []byte, _ zipreader.Options) outcome {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, python, "-I", "-c", pythonScript, path).Output()
		if ctx.Err() != nil {
			return outcome{Verdict: "timeout"}
		}
		var res struct {
			OK    [][2]string `json:"ok"`
			Error string      `json:"error"`
		}
		if json.Unmarshal(out, &res) != nil {
			return outcome{Verdict: "rejects", Detail: fmt.Sprint("no result: ", err)}
		}
		if res.Error != "" {
			return outcome{Verdict: "rejects", Detail: res.Error}
		}
		o := outcome{Verdict: "reads", Files: map[string]string{}}
		for _, f := range res.OK {
			o.Files[f[0]] = f[1]
			o.Sums = append(o.Sums, f[1])
		}
		sort.Strings(o.Sums)
		return o
	}
}

// runExtractor runs a program that extracts the archive into a directory. The target is four levels deep inside
// a scratch directory, so an archive whose names climb out of the target is caught instead of doing harm.
// warn is the exit status that means "extracted, with a warning".
func runExtractor(warn int, command func(archive, target string) []string) func(string, []byte, zipreader.Options) outcome {
	return func(path string, _ []byte, _ zipreader.Options) outcome {
		scratch, err := os.MkdirTemp("", "gozipgo-compare-")
		if err != nil {
			return outcome{Verdict: "rejects", Detail: err.Error()}
		}
		defer func() {
			filepath.WalkDir(scratch, func(p string, d fs.DirEntry, err error) error {
				if err == nil && d.IsDir() {
					os.Chmod(p, 0o755)
				}
				return nil
			})
			os.RemoveAll(scratch)
		}()
		target := filepath.Join(scratch, "a", "b", "c", "target")
		if err := os.MkdirAll(target, 0o755); err != nil {
			return outcome{Verdict: "rejects", Detail: err.Error()}
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		args := command(path, target)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = target
		cmd.Stdin = nil
		runErr := cmd.Run()
		if ctx.Err() != nil {
			return outcome{Verdict: "timeout"}
		}
		status := 0
		if runErr != nil {
			status = -1
			if x, ok := runErr.(*exec.ExitError); ok {
				status = x.ExitCode()
			}
		}
		o := outcome{Verdict: "reads"}
		// Extractors restore the permissions stored in the archive, which may be none at all.
		filepath.WalkDir(scratch, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			} else if err == nil && d.Type().IsRegular() {
				os.Chmod(p, 0o644)
			}
			return nil
		})
		filepath.WalkDir(scratch, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			body, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			if !strings.HasPrefix(p, target+string(filepath.Separator)) {
				o.Escaped++
			}
			o.Sums = append(o.Sums, sum(body))
			return nil
		})
		sort.Strings(o.Sums)
		switch {
		case status == 0:
		case status == warn:
			o.Verdict, o.Detail = "reads with a warning", fmt.Sprint("exit status ", status)
		default:
			o.Verdict, o.Detail = "rejects", fmt.Sprint("exit status ", status)
			if len(o.Sums) > 0 {
				o.Detail += fmt.Sprintf(", %d files written", len(o.Sums))
			}
		}
		return o
	}
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

func candidates() []candidate {
	cs := []candidate{
		{"gozipgo", "this commit", runGozipgo},
		{"archive/zip", runtime.Version(), runStdlib},
	}
	if p, err := exec.LookPath("unzip"); err == nil {
		cs = append(cs, candidate{"Info-ZIP unzip", firstLine(p, "-v"), runExtractor(1, func(a, t string) []string {
			return []string{p, "-o", "-q", a, "-d", t}
		})})
	}
	for _, n := range []string{"7zz", "7z"} {
		if p, err := exec.LookPath(n); err == nil {
			cs = append(cs, candidate{"7-Zip", firstLine(p), runExtractor(1, func(a, t string) []string {
				return []string{p, "x", "-y", "-bd", "-o" + t, a}
			})})
			break
		}
	}
	if p, err := exec.LookPath("python3"); err == nil {
		cs = append(cs, candidate{"Python zipfile", firstLine(p, "-VV"), runPython(p)})
	}
	if p, err := exec.LookPath("bsdtar"); err == nil {
		cs = append(cs, candidate{"bsdtar (libarchive)", firstLine(p, "--version"), runExtractor(-2, func(a, t string) []string {
			return []string{p, "-x", "-f", a, "-C", t}
		})})
	}
	return cs
}

type interopManifest struct {
	Archives []struct {
		File    string `json:"file"`
		Program string `json:"program"`
		Entries []struct {
			Name    string `json:"name"`
			NameHex string `json:"name_hex"`
			SHA256  string `json:"sha256"`
		} `json:"entries"`
		Expect *struct {
			Code    string   `json:"code"`
			Options []string `json:"options"`
		} `json:"expect"`
	} `json:"archives"`
}

type hostileManifest struct {
	Archives []struct {
		ID     string `json:"id"`
		Family string `json:"family"`
		What   string `json:"what"`
		Code   string `json:"code"`
		AtRead bool   `json:"at_read"`
		SHA256 string `json:"sha256"`
	} `json:"archives"`
}

func readJSON(path string, v any) {
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, v)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(v ...any) {
	fmt.Fprintln(os.Stderr, v...)
	os.Exit(1)
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
	hostileDir := flag.String("hostile", "", "directory of the hostile archives (default: built into a temporary directory)")
	rawPath := flag.String("raw", "docs/results/compare.tsv", "raw result, relative to -root")
	docPath := flag.String("doc", "docs/comparison.md", "document whose tables are rewritten, relative to -root")
	flag.Parse()
	abs, err := filepath.Abs(*root)
	if err != nil {
		fatal(err)
	}

	var interop interopManifest
	readJSON(filepath.Join(abs, "testdata", "interop", "manifest.json"), &interop)
	var hostile hostileManifest
	readJSON(filepath.Join(abs, "testdata", "hostile.json"), &hostile)
	if *hostileDir == "" {
		dir, err := os.MkdirTemp("", "gozipgo-hostile-")
		if err != nil {
			fatal(err)
		}
		defer os.RemoveAll(dir)
		cmd := exec.Command("go", "test", "./zipreader", "-run", "TestHostileArchives", "-count=1", "-write-hostile", dir)
		cmd.Dir = abs
		if out, err := cmd.CombinedOutput(); err != nil {
			fatal("building the hostile corpus:", err, string(out))
		}
		*hostileDir = dir
	}

	cs := candidates()
	var raw, doc strings.Builder
	commit := firstLine("git", "-C", abs, "rev-parse", "--short", "HEAD")
	// Changes under docs/ do not count: the tools themselves rewrite files there.
	if firstLine("git", "-C", abs, "status", "--porcelain", "--", ".", ":!docs") != "" {
		commit += " with uncommitted changes"
	}
	machine := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOOS == "darwin" {
		machine += ", " + firstLine("sysctl", "-n", "machdep.cpu.brand_string") + ", macOS " + firstLine("sw_vers", "-productVersion")
	}
	date := time.Now().Format("2006-01-02")
	fmt.Fprintf(&raw, "# gozipgo tools/compare\n# date\t%s\n# commit\t%s\n# machine\t%s\n", date, commit, machine)
	for _, c := range cs {
		fmt.Fprintf(&raw, "# candidate\t%s\t%s\n", c.Name, c.Version)
	}
	raw.WriteString("corpus\tarchive\tcandidate\tverdict\tdetail\tfiles\tescaped\tcontent digest\tagrees\n")

	// Conformance corpus: every reader must give the content the manifest records.
	agree := make([]int, len(cs))
	fmt.Fprintf(&doc, "### Conformance corpus\n\n%d archives made by real programs (`testdata/interop`). \"same\" means the reader gives exactly the files\nthe manifest records (SHA-256 of every file; for readers that list names, the names too, where the archive stores\nthem as UTF-8 or ASCII).\n\n| Archive |", len(interop.Archives))
	for _, c := range cs {
		doc.WriteString(" " + c.Name + " |")
	}
	doc.WriteString("\n|---|" + strings.Repeat("---|", len(cs)) + "\n")
	for _, a := range interop.Archives {
		path := filepath.Join(abs, "testdata", "interop", a.File)
		data, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		opts := zipreader.Options{}
		note := ""
		if a.Expect != nil {
			for _, o := range a.Expect.Options {
				switch o {
				case "UnflaggedNames=UTF8":
					opts.UnflaggedNames = zipreader.UTF8
				case "UnflaggedNames=CodePage:CP852":
					var page [128]rune
					copy(page[:], []rune(cp852High))
					opts.UnflaggedNames, opts.CodePage = zipreader.CodePage, &page
				default:
					fatal("unknown option in the manifest:", o)
				}
				note = " with `" + o + "`"
			}
		}
		var want []string
		names := map[string]string{}
		for _, e := range a.Entries {
			if strings.HasSuffix(e.Name, "/") {
				continue
			}
			want = append(want, e.SHA256)
			if e.NameHex == "" {
				names[e.Name] = e.SHA256
			}
		}
		sort.Strings(want)
		fmt.Fprintf(&doc, "| `%s` |", a.File)
		for i, c := range cs {
			o := c.Run(path, data, opts)
			same := o.Verdict != "rejects" && o.Verdict != "timeout" && strings.Join(o.Sums, ",") == strings.Join(want, ",") && o.Escaped == 0
			if same && c.Name == "gozipgo" {
				for n, s := range names {
					if o.Files[n] != s {
						same = false
					}
				}
			}
			cell := "same"
			switch {
			case same && c.Name == "gozipgo":
				cell += note
			case same && o.Verdict != "reads":
				cell += " (" + o.Verdict[len("reads "):] + ")"
			case !same && (o.Verdict == "rejects" || o.Verdict == "timeout"):
				cell = o.Verdict + ": " + o.Detail
			case !same:
				cell = fmt.Sprintf("differs (%d files)", len(o.Sums))
			}
			if same {
				agree[i]++
			}
			fmt.Fprintf(&doc, " %s |", cell)
			fmt.Fprintf(&raw, "interop\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%v\n", a.File, c.Name, o.Verdict, o.Detail, len(o.Sums), o.Escaped, o.digest(), same)
		}
		doc.WriteString("\n")
	}
	doc.WriteString("| **same** |")
	for i := range cs {
		fmt.Fprintf(&doc, " **%d / %d** |", agree[i], len(interop.Archives))
	}
	doc.WriteString("\n\n")

	// Hostile corpus.
	fmt.Fprintf(&doc, "### Hostile corpus\n\n%d archives built by the tests of the repository (`testdata/hostile.json`). A cell says what the reader did:\n\"rejects\", or \"reads N\" with the number of files it produced and a letter for their content, so two readers with\ndifferent letters in one row read different content from the same file. \"escapes\" means files were written\noutside the directory the reader was told to extract into.\n\n| Archive | What it is |", len(hostile.Archives))
	for _, c := range cs {
		doc.WriteString(" " + c.Name + " |")
	}
	doc.WriteString("\n|---|---|" + strings.Repeat("---|", len(cs)) + "\n")
	reads := make([]int, len(cs))
	for _, h := range hostile.Archives {
		path := filepath.Join(*hostileDir, h.ID+".zip")
		data, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		if sum(data) != h.SHA256 {
			fatal(h.ID + ": the archive differs from testdata/hostile.json")
		}
		fmt.Fprintf(&doc, "| `%s` | %s |", h.ID, strings.ReplaceAll(h.What, "|", "\\|"))
		letters := map[string]string{}
		for i, c := range cs {
			o := c.Run(path, data, zipreader.Options{})
			cell := o.Verdict
			switch o.Verdict {
			case "rejects":
				if c.Name == "gozipgo" {
					cell = "rejects: `" + o.Detail + "`"
					if o.Detail != h.Code {
						fatal(h.ID+": gozipgo gives", o.Detail, "the manifest says", h.Code)
					}
				} else if len(o.Sums) > 0 {
					cell = fmt.Sprintf("rejects after writing %d", len(o.Sums))
				}
			case "reads", "reads with a warning":
				reads[i]++
				d := o.digest()
				if letters[d] == "" {
					letters[d] = string(rune('A' + len(letters)))
				}
				cell = fmt.Sprintf("reads %d (%s)", len(o.Sums), letters[d])
				if o.Verdict != "reads" {
					cell += ", warning"
				}
			}
			if o.Escaped > 0 {
				cell += fmt.Sprintf(", **escapes** (%d)", o.Escaped)
			}
			fmt.Fprintf(&doc, " %s |", cell)
			fmt.Fprintf(&raw, "hostile\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t\n", h.ID, c.Name, o.Verdict, o.Detail, len(o.Sums), o.Escaped, o.digest())
		}
		doc.WriteString("\n")
	}
	doc.WriteString("| **reads** | |")
	for i := range cs {
		fmt.Fprintf(&doc, " **%d / %d** |", reads[i], len(hostile.Archives))
	}
	doc.WriteString("\n")

	// Archives written by gozipgo: every reader must give back the content that went in.
	ws := writtenArchives()
	scratch, err := os.MkdirTemp("", "gozipgo-written-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(scratch)
	fmt.Fprintf(&doc, "\n### Archives written by gozipgo\n\n%d archives written by `zipwriter` from data the tool generates. \"same\" means the reader gives back exactly\nthe files that were written (SHA-256 of every file; for readers that list names, the names too). gozipgo reads\nthem in its strict mode, with its default limits except the one on the number of entries.\n\n| Archive | What it is |", len(ws))
	for _, c := range cs {
		doc.WriteString(" " + c.Name + " |")
	}
	doc.WriteString("\n|---|---|" + strings.Repeat("---|", len(cs)) + "\n")
	back := make([]int, len(cs))
	for _, a := range ws {
		path := filepath.Join(scratch, a.ID+".zip")
		if err := os.WriteFile(path, a.Data, 0o644); err != nil {
			fatal(err)
		}
		var want []string
		for _, s := range a.Files {
			want = append(want, s)
		}
		sort.Strings(want)
		fmt.Fprintf(&doc, "| `%s` | %s |", a.ID, a.What)
		for i, c := range cs {
			o := c.Run(path, a.Data, zipreader.Options{MaxEntries: -1})
			same := o.Verdict != "rejects" && o.Verdict != "timeout" && strings.Join(o.Sums, ",") == strings.Join(want, ",") && o.Escaped == 0
			if same && o.Files != nil {
				for n, s := range a.Files {
					if o.Files[n] != s {
						same = false
					}
				}
			}
			cell := "same"
			switch {
			case same && o.Verdict != "reads":
				cell += " (" + o.Verdict[len("reads "):] + ")"
			case !same && (o.Verdict == "rejects" || o.Verdict == "timeout"):
				cell = o.Verdict + ": " + o.Detail
			case !same:
				cell = fmt.Sprintf("differs (%d files)", len(o.Sums))
			}
			if same {
				back[i]++
			}
			fmt.Fprintf(&doc, " %s |", cell)
			fmt.Fprintf(&raw, "written\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%v\n", a.ID, c.Name, o.Verdict, o.Detail, len(o.Sums), o.Escaped, o.digest(), same)
		}
		fmt.Fprintf(&raw, "written\t%s\t(archive)\t%d bytes\t%s\t%d\t0\t\t\n", a.ID, len(a.Data), sum(a.Data), len(a.Files))
		doc.WriteString("\n")
	}
	doc.WriteString("| **same** | |")
	for i := range cs {
		fmt.Fprintf(&doc, " **%d / %d** |", back[i], len(ws))
	}
	doc.WriteString("\n")

	header := fmt.Sprintf("Measured on %s at commit `%s` on %s. Readers:\n\n", date, commit, machine)
	for _, c := range cs {
		header += fmt.Sprintf("- %s: %s\n", c.Name, c.Version)
	}
	header += "\n"
	if err := os.WriteFile(resolve(abs, *rawPath), []byte(raw.String()), 0o644); err != nil {
		fatal(err)
	}
	docFile := resolve(abs, *docPath)
	old, err := os.ReadFile(docFile)
	if err != nil {
		fatal(err)
	}
	const begin, end = "<!-- compare:begin -->\n", "<!-- compare:end -->\n"
	i, j := strings.Index(string(old), begin), strings.Index(string(old), end)
	if i < 0 || j < i {
		fatal(*docPath + " has no compare:begin and compare:end markers")
	}
	out := string(old[:i]) + begin + header + doc.String() + string(old[j:])
	if err := os.WriteFile(docFile, []byte(out), 0o644); err != nil {
		fatal(err)
	}
	for i, c := range cs {
		fmt.Printf("%-22s conformance %d/%d, reads %d/%d hostile archives, reads back %d/%d written archives\n", c.Name, agree[i], len(interop.Archives), reads[i], len(hostile.Archives), back[i], len(ws))
	}
}
