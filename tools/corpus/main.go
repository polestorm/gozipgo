// Command corpus builds the conformance corpus of gozipgo (testdata/interop): small archives with invented content,
// made by real programs, and the manifest that says for each archive which program, version and command made it
// and what it contains (name, size, CRC-32 and SHA-256 of every entry).
//
//	go run . -make            make the archives the programs on this machine can make, rewrite the manifest
//	go run . -add FILE -id ID -program P -version V -system S -command C
//	                          add an archive made elsewhere (Windows Explorer, Word, LibreOffice, Java)
//	go run . -check           verify that the archives match the manifest
//
// The archives are test data, not code. The contents listed in the manifest are read with archive/zip of the Go
// standard library, used as a black box, and checked against the source files this tool wrote: the manifest is
// therefore independent of gozipgo. Making the archives again does not give the same bytes (programs store user
// ids and access times), so -make replaces the corpus; the committed archives are the corpus.
//
// The tool is a separate module: it runs external programs and uses archive/zip, which the library does not.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Manifest to treść testdata/interop/manifest.json.
type Manifest struct {
	Note     string    `json:"note"`
	Sources  []Source  `json:"sources"`
	Archives []Archive `json:"archives"`
}

// Source to jeden plik źródłowy, z którego programy budują archiwa.
type Source struct {
	Name   string `json:"name"`
	Size   int    `json:"size"`
	SHA256 string `json:"sha256"`
}

// Archive to jedno archiwum korpusu.
type Archive struct {
	ID      string  `json:"id"`
	File    string  `json:"file"`
	Program string  `json:"program"`
	Version string  `json:"version"`
	System  string  `json:"system"`
	Command string  `json:"command"`
	Made    string  `json:"made"` // data utworzenia, RRRR-MM-DD
	SHA256  string  `json:"sha256"`
	Size    int     `json:"size"`
	Comment string  `json:"comment,omitempty"`
	Entries []Entry `json:"entries"`
	// Expect jest wpisywane ręcznie i zachowywane przy -make: kod, którym gozipgo w trybie ścisłym odrzuca
	// archiwum, z odsyłaczem do docs/differences.md. Puste oznacza, że archiwum jest przyjmowane.
	Expect *Expect `json:"expect,omitempty"`
}

// Expect opisuje wyjątek od kryterium K2.
type Expect struct {
	Code    string   `json:"code"`
	Options []string `json:"options,omitempty"` // opcje, z którymi archiwum jest przyjmowane
	Reason  string   `json:"reason"`
}

// Entry to jeden wpis archiwum w kolejności katalogu centralnego.
type Entry struct {
	Name string `json:"name"`
	// NameHex jest podawane, gdy nazwa zapisana w archiwum nie jest poprawnym UTF-8.
	NameHex  string `json:"name_hex,omitempty"`
	UTF8Flag bool   `json:"utf8_flag"`
	Method   uint16 `json:"method"`
	Size     uint64 `json:"size"`
	CRC32    string `json:"crc32"`
	SHA256   string `json:"sha256"`
}

// sourceTime to czas modyfikacji plików źródłowych; programy są uruchamiane z TZ=UTC.
var sourceTime = time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC)

// sourceFiles zwraca pliki źródłowe: treść wymyślona, plik pusty, plik binarny z generatora, podkatalogi, nazwa
// z polskimi literami i nazwa ze spacją (testdata/README.md).
func sourceFiles() map[string][]byte {
	var lorem bytes.Buffer
	for i := 1; i <= 120; i++ {
		fmt.Fprintf(&lorem, "Line %03d: the quick brown fox files report number %d and jumps over the lazy archive.\n", i, i*i)
	}
	// Liniowy generator kongruencyjny o stałych parametrach: dane, których Deflate nie zmniejszy.
	bin := make([]byte, 4096)
	x := uint32(20261126)
	for i := range bin {
		x = x*1664525 + 1013904223
		bin[i] = byte(x >> 24)
	}
	return map[string][]byte{
		"hello.txt":             []byte("Hello, gozipgo!\n"),
		"empty.txt":             {},
		"lorem.txt":             lorem.Bytes(),
		"data.bin":              bin,
		"sub/inner.txt":         []byte("A file in a subdirectory.\n"),
		"sub/deep/leaf.txt":     []byte("A file two levels down.\n"),
		"zażółć gęślą jaźń.txt": []byte("Zażółć gęślą jaźń.\n"),
		"with space.txt":        []byte("A name with a space.\n"),
	}
}

func writeSources(dir string) error {
	files := sourceFiles()
	var dirs []string
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "emptydir"), 0o755); err != nil {
		return err
	}
	// Czasy ustawiamy na końcu, od najgłębszych katalogów, żeby zapis plików ich nie zmienił.
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			dirs = append(dirs, p)
		}
		return err
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, p := range dirs {
		if err := os.Chtimes(p, sourceTime, sourceTime); err != nil {
			return err
		}
	}
	return nil
}

// recipe to przepis na jedno archiwum: program zewnętrzny albo funkcja w tym procesie.
type recipe struct {
	id      string
	program string
	version func() (string, error)
	command string                                // polecenie powłoki, uruchamiane w katalogu źródeł; $OUT to plik wyjściowy
	inproc  func(src, out string) (string, error) // zamiast polecenia; zwraca opis do manifestu
	xattr   bool                                  // źródła z atrybutem rozszerzonym na hello.txt (macOS)
}

func commandVersion(line int, name string, args ...string) func() (string, error) {
	return func() (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) <= line || (err != nil && len(out) == 0) {
			return "", fmt.Errorf("%s: %v", name, err)
		}
		return strings.TrimSpace(lines[line]), nil
	}
}

const pythonScript = `
import sys, zipfile, os
mode, out = sys.argv[1], sys.argv[2]
names = []
for root, dirs, files in os.walk('.'):
    dirs.sort()
    for d in dirs:
        if not os.listdir(os.path.join(root, d)):
            names.append(os.path.join(root, d)[2:] + '/')
    for f in sorted(files):
        names.append(os.path.join(root, f)[2:])
names.sort()
class Unseekable:
    def __init__(self, f): self.f = f
    def write(self, b): return self.f.write(b)
    def flush(self): self.f.flush()
comp = zipfile.ZIP_STORED if mode == 'stored' else zipfile.ZIP_DEFLATED
raw = open(out, 'wb')
target = Unseekable(raw) if mode == 'stream' else raw
with zipfile.ZipFile(target, 'w', comp) as z:
    for n in names:
        if n.endswith('/'):
            z.write(n, n)
        elif mode == 'zip64':
            info = zipfile.ZipInfo.from_file(n, n)
            info.compress_type = comp
            with z.open(info, 'w', force_zip64=True) as w, open(n, 'rb') as r:
                w.write(r.read())
        else:
            z.write(n, n)
raw.close()
`

func recipes() []recipe {
	zipV := commandVersion(1, "zip", "-v")
	sevenV := commandVersion(0, "7zz", "i")
	dittoV := func() (string, error) {
		out, err := exec.Command("sw_vers", "-productVersion").Output()
		return "ditto of macOS " + strings.TrimSpace(string(out)), err
	}
	pyV := commandVersion(0, "python3", "-V")
	goV := func() (string, error) { return runtime.Version(), nil }
	py := func(mode string) string {
		return "python3 -I -c \"$PYTHON_SCRIPT\" " + mode + " \"$OUT\"   # the script is pythonScript in tools/corpus/main.go"
	}
	return []recipe{
		{id: "infozip-default", program: "Info-ZIP zip", version: zipV, command: `zip -r "$OUT" .`},
		{id: "infozip-stored", program: "Info-ZIP zip", version: zipV, command: `zip -0 -r "$OUT" .`},
		{id: "infozip-best", program: "Info-ZIP zip", version: zipV, command: `zip -9 -r "$OUT" .`},
		{id: "infozip-comment", program: "Info-ZIP zip", version: zipV, command: `printf 'An archive comment.\n' | zip -z -r "$OUT" .`},
		{id: "infozip-stream", program: "Info-ZIP zip", version: zipV, command: `zip -r - . | cat > "$OUT"`},
		{id: "infozip-zip64", program: "Info-ZIP zip", version: zipV, command: `zip -fz -r "$OUT" .`},
		{id: "infozip-noextra", program: "Info-ZIP zip", version: zipV, command: `zip -X -r "$OUT" .`},
		{id: "7zip-default", program: "7-Zip", version: sevenV, command: `7zz a -tzip "$OUT" .`},
		{id: "7zip-fastest", program: "7-Zip", version: sevenV, command: `7zz a -tzip -mx=1 "$OUT" .`},
		{id: "7zip-ultra", program: "7-Zip", version: sevenV, command: `7zz a -tzip -mx=9 "$OUT" .`},
		{id: "7zip-stored", program: "7-Zip", version: sevenV, command: `7zz a -tzip -mx=0 "$OUT" .`},
		{id: "macos-ditto", program: "macOS ditto", version: dittoV, command: `ditto -c -k . "$OUT"`},
		{id: "macos-ditto-rsrc", program: "macOS ditto", version: dittoV, xattr: true,
			command: `ditto -c -k --sequesterRsrc . "$OUT"   # after: xattr -w com.example.note invented hello.txt`},
		{id: "python-stored", program: "Python zipfile", version: pyV, command: py("stored")},
		{id: "python-deflated", program: "Python zipfile", version: pyV, command: py("deflated")},
		{id: "python-stream", program: "Python zipfile", version: pyV, command: py("stream")},
		{id: "python-zip64", program: "Python zipfile", version: pyV, command: py("zip64")},
		{id: "go-deflate", program: "Go archive/zip", version: goV, inproc: goArchive(zip.Deflate, false)},
		{id: "go-stored", program: "Go archive/zip", version: goV, inproc: goArchive(zip.Store, false)},
		{id: "go-raw", program: "Go archive/zip", version: goV, inproc: goArchive(zip.Store, true)},
	}
}

// goArchive buduje archiwum pakietem archive/zip: Writer.CreateHeader (z deskryptorem danych) albo, dla raw,
// Writer.CreateRaw z gotowymi rozmiarami i CRC (bez deskryptora).
func goArchive(method uint16, raw bool) func(src, out string) (string, error) {
	return func(src, out string) (string, error) {
		files := sourceFiles()
		names := []string{"emptydir/"}
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		for _, n := range names {
			h := &zip.FileHeader{Name: n, Method: method, Modified: sourceTime}
			if strings.HasSuffix(n, "/") {
				h.Method = zip.Store
			}
			var fw io.Writer
			var err error
			if raw {
				// CreateRaw zapisuje nagłówek bez zmian, więc bit 11 dla nazwy spoza ASCII ustawiamy sami.
				if !isASCII(n) {
					h.Flags |= 0x800
				}
				h.CRC32 = crc32.ChecksumIEEE(files[n])
				h.CompressedSize64, h.UncompressedSize64 = uint64(len(files[n])), uint64(len(files[n]))
				fw, err = w.CreateRaw(h)
			} else {
				fw, err = w.CreateHeader(h)
			}
			if err != nil {
				return "", err
			}
			if _, err := fw.Write(files[n]); err != nil {
				return "", err
			}
		}
		if err := w.Close(); err != nil {
			return "", err
		}
		desc := "zip.Writer.CreateHeader, Method " + map[uint16]string{zip.Store: "Store", zip.Deflate: "Deflate"}[method]
		if raw {
			desc = "zip.Writer.CreateRaw, Method Store (no data descriptor)"
		}
		return desc + "; names sorted; see goArchive in tools/corpus/main.go", os.WriteFile(out, buf.Bytes(), 0o644)
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// describe czyta archiwum pakietem archive/zip (czarna skrzynka) i zwraca jego opis do manifestu. Wpis, którego
// nazwa odpowiada plikowi źródłowemu, musi mieć jego treść.
func describe(path string) (Archive, error) {
	var a Archive
	data, err := os.ReadFile(path)
	if err != nil {
		return a, err
	}
	sum := sha256.Sum256(data)
	a.SHA256, a.Size = hex.EncodeToString(sum[:]), len(data)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return a, fmt.Errorf("%s: archive/zip: %w", path, err)
	}
	a.Comment = zr.Comment
	sources := sourceFiles()
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return a, fmt.Errorf("%s: %s: %w", path, f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return a, fmt.Errorf("%s: %s: %w", path, f.Name, err)
		}
		h := sha256.Sum256(content)
		e := Entry{Name: f.Name, UTF8Flag: f.Flags&0x800 != 0, Method: f.Method, Size: uint64(len(content)),
			CRC32: fmt.Sprintf("%08x", crc32.ChecksumIEEE(content)), SHA256: hex.EncodeToString(h[:])}
		if !utf8.ValidString(f.Name) {
			e.Name, e.NameHex = strings.ToValidUTF8(f.Name, "�"), hex.EncodeToString([]byte(f.Name))
		}
		if want, ok := sources[strings.TrimPrefix(f.Name, "./")]; ok && !bytes.Equal(want, content) {
			return a, fmt.Errorf("%s: %s: content differs from the source file", path, f.Name)
		}
		a.Entries = append(a.Entries, e)
	}
	return a, nil
}

func readManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func writeManifest(path string, m Manifest) error {
	m.Note = "Conformance corpus of gozipgo: archives with invented content made by real programs. Written by tools/corpus; " +
		"the expect blocks are written by hand. See testdata/README.md."
	m.Sources = nil
	for name, data := range sourceFiles() {
		h := sha256.Sum256(data)
		m.Sources = append(m.Sources, Source{Name: name, Size: len(data), SHA256: hex.EncodeToString(h[:])})
	}
	sort.Slice(m.Sources, func(i, j int) bool { return m.Sources[i].Name < m.Sources[j].Name })
	sort.Slice(m.Archives, func(i, j int) bool { return m.Archives[i].ID < m.Archives[j].ID })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func system() string {
	if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
		return "macOS " + strings.TrimSpace(string(out)) + " " + runtime.GOARCH
	}
	return runtime.GOOS + " " + runtime.GOARCH
}

func put(m *Manifest, a Archive) {
	for i := range m.Archives {
		if m.Archives[i].ID == a.ID {
			a.Expect = m.Archives[i].Expect
			m.Archives[i] = a
			return
		}
	}
	m.Archives = append(m.Archives, a)
}

func run() error {
	dir := flag.String("dir", filepath.Join("..", "..", "testdata", "interop"), "directory of the corpus")
	doMake := flag.Bool("make", false, "make the archives this machine can make and rewrite the manifest")
	doCheck := flag.Bool("check", false, "verify the archives against the manifest")
	add := flag.String("add", "", "add an archive made elsewhere")
	id := flag.String("id", "", "with -add: identifier of the archive")
	program := flag.String("program", "", "with -add: the program that made it")
	version := flag.String("version", "", "with -add: its version")
	sys := flag.String("system", "", "with -add: the operating system it ran on")
	command := flag.String("command", "", "with -add: the command or the steps in the user interface")
	flag.Parse()
	manifestPath := filepath.Join(*dir, "manifest.json")
	m, err := readManifest(manifestPath)
	if err != nil {
		return err
	}
	switch {
	case *doCheck:
		for _, a := range m.Archives {
			got, err := describe(filepath.Join(*dir, a.File))
			if err != nil {
				return err
			}
			if got.SHA256 != a.SHA256 || fmt.Sprint(got.Entries) != fmt.Sprint(a.Entries) {
				return fmt.Errorf("%s differs from the manifest", a.File)
			}
		}
		fmt.Printf("%d archives match the manifest\n", len(m.Archives))
		return nil
	case *add != "":
		if *id == "" || *program == "" || *version == "" || *sys == "" || *command == "" {
			return errors.New("-add needs -id, -program, -version, -system and -command")
		}
		file := *id + filepath.Ext(*add)
		data, err := os.ReadFile(*add)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*dir, file), data, 0o644); err != nil {
			return err
		}
		a, err := describe(filepath.Join(*dir, file))
		if err != nil {
			return err
		}
		a.ID, a.File, a.Program, a.Version, a.System, a.Command = *id, file, *program, *version, *sys, *command
		a.Made = time.Now().UTC().Format("2006-01-02")
		put(&m, a)
		return writeManifest(manifestPath, m)
	case *doMake:
		tmp, err := os.MkdirTemp("", "gozipgo-corpus-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		plain, withAttr := filepath.Join(tmp, "plain"), filepath.Join(tmp, "xattr")
		for _, d := range []string{plain, withAttr} {
			if err := writeSources(d); err != nil {
				return err
			}
		}
		xattrOK := exec.Command("xattr", "-w", "com.example.note", "invented", filepath.Join(withAttr, "hello.txt")).Run() == nil
		if xattrOK {
			os.Chtimes(filepath.Join(withAttr, "hello.txt"), sourceTime, sourceTime)
		}
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			return err
		}
		for _, rc := range recipes() {
			ver, err := rc.version()
			if err != nil || (rc.xattr && !xattrOK) {
				fmt.Printf("skipped %-20s %s is not available here\n", rc.id, rc.program)
				continue
			}
			file := rc.id + ".zip"
			out, err := filepath.Abs(filepath.Join(*dir, file))
			if err != nil {
				return err
			}
			os.Remove(out)
			src := plain
			if rc.xattr {
				src = withAttr
			}
			cmdText := rc.command
			if rc.inproc != nil {
				if cmdText, err = rc.inproc(src, out); err != nil {
					return fmt.Errorf("%s: %w", rc.id, err)
				}
			} else {
				c := exec.Command("sh", "-c", rc.command)
				c.Dir = src
				c.Env = append(os.Environ(), "OUT="+out, "TZ=UTC", "LC_ALL=en_US.UTF-8", "PYTHON_SCRIPT="+pythonScript)
				if b, err := c.CombinedOutput(); err != nil {
					return fmt.Errorf("%s: %v\n%s", rc.id, err, b)
				}
				cmdText = "TZ=UTC LC_ALL=en_US.UTF-8 " + strings.ReplaceAll(rc.command, `"$OUT"`, file)
			}
			a, err := describe(out)
			if err != nil {
				return err
			}
			a.ID, a.File, a.Program, a.Version, a.System, a.Command = rc.id, file, rc.program, ver, system(), cmdText
			a.Made = time.Now().UTC().Format("2006-01-02")
			put(&m, a)
			fmt.Printf("made    %-20s %6d bytes, %d entries\n", rc.id, a.Size, len(a.Entries))
		}
		return writeManifest(manifestPath, m)
	}
	flag.Usage()
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
}
