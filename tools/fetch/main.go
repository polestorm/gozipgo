// Command fetch pobiera dane testowe opisane w testdata/sources.json: publiczne zestawy archiwów testowych
// z przypiętym adresem, sumą i liczbą plików (narzędzie przeniesione z goxmlgo; format manifestu ten sam).
//
// Pobrane pliki trafiają do testdata/fetched/ i nie należą do repozytorium. Każdy plik i każde rozpakowane drzewo
// jest sprawdzane sumą SHA-256 z manifestu. Narzędzie jest osobnym modułem, żeby kod sieciowy nie należał do
// biblioteki. Pusta lista zestawów jest poprawna: narzędzie mówi, że nie ma czego pobierać, i kończy się kodem 0.
//
// Użycie (z katalogu głównego repozytorium):
//
//	go -C tools/fetch run . -root ../..             # pobiera wszystko, czego brakuje
//	go -C tools/fetch run . -root ../.. -set NAME   # jeden zestaw
//	go -C tools/fetch run . -root ../.. -verify     # tylko sprawdza to, co już pobrane
//	go -C tools/fetch run . -root ../.. -from DIR   # brakujące pliki zestawów "files" kopiuje z DIR
//	go -C tools/fetch run . -root ../.. -record     # dopisuje do manifestu brakujące sumy (nowe wpisy)
//
// Zestaw rodzaju "archive" to archiwum tar.gz rozpakowywane do katalogu "dest" (pola: url, strip, sha256,
// tree_sha256, file_count; opcjonalnie include: początki ścieżek, które bierzemy, reszta archiwum nie jest
// zapisywana na dysk); zestaw rodzaju "files" to lista pojedynczych plików (path, urls, sha256). Katalog "dest"
// musi leżeć w testdata/fetched/.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxDownload ogranicza rozmiar jednego pobrania.
const maxDownload = 256 << 20

type manifest struct {
	Note string     `json:"note,omitempty"`
	Sets []*dataset `json:"sets"`
}

type dataset struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // "archive" albo "files"
	Dest  string `json:"dest"`
	Note  string `json:"note,omitempty"`
	URL   string `json:"url,omitempty"`
	Strip int    `json:"strip,omitempty"`
	// Include ogranicza zestaw "archive" do ścieżek (po Strip) zaczynających się od jednego z podanych początków.
	// Służy do brania z cudzego repozytorium samych danych testowych, bez kodu (docs/provenance.md).
	Include    []string `json:"include,omitempty"`
	SHA256     *string  `json:"sha256,omitempty"`
	TreeSHA256 string   `json:"tree_sha256,omitempty"`
	FileCount  int      `json:"file_count,omitempty"`
	Files      []*file  `json:"files,omitempty"`
}

type file struct {
	Path      string   `json:"path"`
	URLs      []string `json:"urls"`
	SHA256    string   `json:"sha256"`
	Namespace string   `json:"namespace,omitempty"`
	Imports   []string `json:"imports,omitempty"`
	Note      string   `json:"note,omitempty"`
	// LocalOnly oznacza plik bez adresu w sieci: bierzemy go tylko z -from, a bez -from pomijamy.
	LocalOnly bool `json:"local_only,omitempty"`
}

type options struct {
	root, from     string
	verify, record bool
	client         *http.Client
	out            io.Writer
}

// fetchedRoot to jedyne miejsce, do którego narzędzie pisze i które wolno mu podmieniać (.gitignore).
const fetchedRoot = "testdata/fetched"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run wykonuje narzędzie i zwraca kod wyjścia: 0, gdy wszystkie wybrane zestawy są na miejscu z sumami z manifestu
// (także gdy manifest nie ma żadnego zestawu), 1 przy każdym problemie, 2 przy błędnych argumentach.
func run(args []string, stdout, stderr io.Writer) int {
	var o options
	var only, manifestPath string
	flags := flag.NewFlagSet("fetch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&o.root, "root", ".", "repository root")
	flags.StringVar(&manifestPath, "manifest", "testdata/sources.json", "manifest path, relative to -root")
	flags.StringVar(&only, "set", "", "fetch only this set")
	flags.StringVar(&o.from, "from", "", "directory with local copies for sets of kind \"files\" (checked by SHA-256)")
	flags.BoolVar(&o.verify, "verify", false, "do not download, only verify what is present")
	flags.BoolVar(&o.record, "record", false, "write missing checksums into the manifest")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	o.client = &http.Client{Timeout: 5 * time.Minute}
	o.out = stdout
	fail := func(err error) int {
		fmt.Fprintln(stderr, "fetch:", err)
		return 1
	}

	mpath := filepath.Join(o.root, filepath.FromSlash(manifestPath))
	m, err := readManifest(mpath)
	if err != nil {
		return fail(err)
	}
	if len(m.Sets) == 0 && only == "" {
		fmt.Fprintf(stdout, "nothing to fetch: %s lists no sets\n", manifestPath)
		return 0
	}
	var failed []string
	found := false
	for _, s := range m.Sets {
		if only != "" && s.Name != only {
			continue
		}
		found = true
		var errs []error
		switch err := checkDest(s.Dest); {
		case err != nil:
			errs = []error{err}
		case s.Kind == "archive":
			errs = o.archiveSet(s)
		case s.Kind == "files":
			errs = o.filesSet(s)
		default:
			errs = []error{fmt.Errorf("unknown kind %q", s.Kind)}
		}
		for _, e := range errs {
			failed = append(failed, s.Name+": "+e.Error())
		}
	}
	if !found {
		return fail(fmt.Errorf("no set named %q", only))
	}
	if o.record {
		if err := writeManifest(mpath, m); err != nil {
			return fail(err)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(stderr, "\n%d problem(s):\n", len(failed))
		for _, f := range failed {
			fmt.Fprintln(stderr, "  "+f)
		}
		return 1
	}
	return 0
}

// checkDest przyjmuje tylko katalog docelowy wewnątrz testdata/fetched/: zestaw rodzaju "archive" podmienia swój
// katalog w całości, więc błędny wpis manifestu nie może wskazać niczego, co należy do repozytorium.
func checkDest(dest string) error {
	if err := checkRelPath(dest); err != nil {
		return fmt.Errorf("dest: %w", err)
	}
	if !strings.HasPrefix(dest, fetchedRoot+"/") {
		return fmt.Errorf("dest %q is not inside %s/", dest, fetchedRoot)
	}
	return nil
}

func readManifest(p string) (*manifest, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &m, nil
}

func writeManifest(p string, m *manifest) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	return os.WriteFile(p, buf.Bytes(), 0o644)
}

// archiveSet pobiera archiwum tar.gz, rozpakowuje je do katalogu tymczasowego, sprawdza sumę drzewa i dopiero
// wtedy podmienia katalog docelowy.
func (o *options) archiveSet(s *dataset) []error {
	dest := filepath.Join(o.root, filepath.FromSlash(s.Dest))
	if sum, n, err := treeSum(dest); err == nil && s.TreeSHA256 != "" {
		if sum == s.TreeSHA256 && n == s.FileCount {
			fmt.Fprintf(o.out, "%s: ok (%d files, present)\n", s.Name, n)
			return nil
		}
		if o.verify {
			return []error{fmt.Errorf("tree checksum mismatch in %s: %d files, %s", s.Dest, n, sum)}
		}
	} else if o.verify {
		return []error{fmt.Errorf("%s missing or not pinned", s.Dest)}
	}

	fmt.Fprintf(o.out, "%s: downloading %s\n", s.Name, s.URL)
	body, err := o.download(s.URL)
	if err != nil {
		return []error{err}
	}
	got := hexSum(body)
	if s.SHA256 != nil && *s.SHA256 != "" && *s.SHA256 != got {
		return []error{fmt.Errorf("archive checksum %s, manifest %s", got, *s.SHA256)}
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".fetch-"+s.Name+"-")
	if err != nil {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return []error{err}
		}
		if tmp, err = os.MkdirTemp(filepath.Dir(dest), ".fetch-"+s.Name+"-"); err != nil {
			return []error{err}
		}
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGz(bytes.NewReader(body), tmp, s.Strip, s.Include); err != nil {
		return []error{err}
	}
	sum, n, err := treeSum(tmp)
	if err != nil {
		return []error{err}
	}
	if s.TreeSHA256 == "" || (s.SHA256 != nil && *s.SHA256 == "") {
		if !o.record {
			return []error{fmt.Errorf("not pinned (archive %s, tree %s, %d files); run with -record", got, sum, n)}
		}
		s.TreeSHA256, s.FileCount = sum, n
		if s.SHA256 != nil {
			*s.SHA256 = got
		}
		fmt.Fprintf(o.out, "%s: recorded tree %s, %d files\n", s.Name, sum, n)
	} else if sum != s.TreeSHA256 || n != s.FileCount {
		return []error{fmt.Errorf("tree checksum %s (%d files), manifest %s (%d files)", sum, n, s.TreeSHA256, s.FileCount)}
	}
	if err := os.RemoveAll(dest); err != nil {
		return []error{err}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return []error{err}
	}
	fmt.Fprintf(o.out, "%s: ok (%d files)\n", s.Name, n)
	return nil
}

// filesSet pobiera pojedyncze pliki: każdy adres po kolei, potem kopię z -from; przyjmuje tylko plik o sumie
// z manifestu.
func (o *options) filesSet(s *dataset) []error {
	dest := filepath.Join(o.root, filepath.FromSlash(s.Dest))
	type result struct {
		err    error
		source string
	}
	results := make([]result, len(s.Files))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, f := range s.Files {
		wg.Add(1)
		go func(i int, f *file) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			src, err := o.fetchFile(f, dest)
			results[i] = result{err, src}
		}(i, f)
	}
	wg.Wait()

	var errs []error
	counts := map[string]int{}
	for i, r := range results {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Files[i].Path, r.err))
			continue
		}
		counts[r.source]++
	}
	fmt.Fprintf(o.out, "%s: %d/%d ok (present %d, downloaded %d, local copy %d, skipped %d)\n", s.Name,
		len(s.Files)-len(errs)-counts["skipped"], len(s.Files), counts["present"], counts["download"], counts["local"],
		counts["skipped"])
	for i, r := range results {
		if r.source == "skipped" {
			fmt.Fprintf(o.out, "  skipped %s: local only, pass -from with a local copy\n", s.Files[i].Path)
		}
	}
	return errs
}

func (o *options) fetchFile(f *file, dest string) (string, error) {
	if err := checkRelPath(f.Path); err != nil {
		return "", err
	}
	target := filepath.Join(dest, filepath.FromSlash(f.Path))
	if b, err := os.ReadFile(target); err == nil && f.SHA256 != "" && hexSum(b) == f.SHA256 {
		return "present", nil
	}
	if f.LocalOnly && o.from == "" {
		return "skipped", nil
	}
	if o.verify {
		return "", errors.New("missing or checksum mismatch")
	}
	var tried []string
	accept := func(b []byte, where string) (bool, error) {
		if looksLikeHTML(b) {
			tried = append(tried, where+": HTML page instead of a file")
			return false, nil
		}
		got := hexSum(b)
		if f.SHA256 == "" {
			if !o.record {
				tried = append(tried, where+": no checksum in manifest, got "+got+" (run with -record)")
				return false, nil
			}
			f.SHA256 = got
		} else if got != f.SHA256 {
			tried = append(tried, where+": checksum "+got)
			return false, nil
		}
		return true, writeFile(target, b)
	}
	for _, u := range f.URLs {
		b, err := o.download(u)
		if err != nil {
			tried = append(tried, err.Error())
			continue
		}
		if ok, err := accept(b, u); ok || err != nil {
			return "download", err
		}
	}
	if o.from != "" {
		local := filepath.Join(o.from, filepath.FromSlash(f.Path))
		b, err := os.ReadFile(local)
		if err != nil {
			tried = append(tried, err.Error())
		} else if ok, err := accept(b, local); ok || err != nil {
			return "local", err
		}
	}
	if len(tried) == 0 {
		return "", errors.New("no source (no URL; use -from)")
	}
	return "", errors.New(strings.Join(tried, "; "))
}

func (o *options) download(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "testdata-fetch")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u, err)
	}
	if len(b) > maxDownload {
		return nil, fmt.Errorf("%s: larger than %d bytes", u, maxDownload)
	}
	return b, nil
}

// looksLikeHTML wykrywa „miękkie 404”: niektóre serwery zwracają stronę HTML zamiast brakującego pliku, czasem
// z kodem 200.
func looksLikeHTML(b []byte) bool {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	b = bytes.TrimLeft(b, " \t\r\n")
	head := strings.ToLower(string(b[:min(len(b), 64)]))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}

// extractTarGz rozpakowuje tylko zwykłe pliki i katalogi, odrzuca ścieżki wychodzące poza katalog docelowy;
// pierwsze strip składników ścieżki jest pomijane.
func extractTarGz(r io.Reader, dir string, strip int, include []string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		parts := strings.Split(name, "/")
		if len(parts) <= strip {
			continue
		}
		rel := strings.Join(parts[strip:], "/")
		if len(include) > 0 {
			keep := false
			for _, prefix := range include {
				keep = keep || strings.HasPrefix(rel, prefix)
			}
			if !keep || h.Typeflag != tar.TypeReg {
				continue
			}
		}
		if err := checkRelPath(rel); err != nil {
			return fmt.Errorf("archive entry %q: %w", h.Name, err)
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			b, err := io.ReadAll(io.LimitReader(tr, maxDownload+1))
			if err != nil {
				return err
			}
			if len(b) > maxDownload {
				return fmt.Errorf("archive entry %q too large", h.Name)
			}
			if err := writeFile(target, b); err != nil {
				return err
			}
		default:
			// dowiązania, urządzenia, nagłówki pax: pomijamy
		}
	}
}

// checkRelPath przyjmuje tylko ścieżkę względną w postaci kanonicznej, bez wyjścia poza katalog; dwukropek
// odrzuca, bo na Windows oznacza dysk albo strumień NTFS.
func checkRelPath(p string) error {
	if p == "" || !fs.ValidPath(p) || strings.ContainsAny(p, `\:`) {
		return fmt.Errorf("unsafe path %q", p)
	}
	return nil
}

func writeFile(target string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, b, 0o644)
}

// treeSum liczy sumę drzewa: SHA-256 z posortowanych wierszy „<sha256 pliku>  <ścieżka>\n”, jak w wyniku
// sha256sum. Nie zależy od sposobu spakowania archiwum.
func treeSum(dir string) (string, int, error) {
	// Katalog bywa dowiązaniem (np. dane z głównej kopii w git worktree): liczymy sumę drzewa, na które wskazuje.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
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
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		lines = append(lines, hexSum(b)+"  "+filepath.ToSlash(rel)+"\n")
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
