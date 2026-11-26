package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractStripsAndHashes(t *testing.T) {
	dir := t.TempDir()
	data := tarGz(t, map[string]string{"top/a.xsd": "a", "top/sub/b.xml": "b"})
	if err := extractTarGz(bytes.NewReader(data), dir, 1, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sub", "b.xml")); err != nil || string(b) != "b" {
		t.Fatalf("sub/b.xml: %q, %v", b, err)
	}
	sum1, n, err := treeSum(dir)
	if err != nil || n != 2 {
		t.Fatalf("treeSum: %d files, %v", n, err)
	}
	// Ta sama zawartość spakowana inaczej (inna kolejność wpisów) daje tę samą sumę drzewa.
	dir2 := t.TempDir()
	extractTarGz(bytes.NewReader(tarGz(t, map[string]string{"x/sub/b.xml": "b", "x/a.xsd": "a"})), dir2, 1, nil)
	if sum2, _, _ := treeSum(dir2); sum2 != sum1 {
		t.Fatalf("tree sum depends on packing: %s vs %s", sum1, sum2)
	}
	// Ta sama wartość jest w testach narzędzia w goxmlgo i goxsdgo: suma drzewa liczy się w rodzinie tak samo,
	// więc wpis manifestu można przenieść między projektami bez zmiany.
	if sum1 != treeSumReference {
		t.Fatalf("tree sum %s, reference %s", sum1, treeSumReference)
	}
}

// Suma drzewa przez dowiązanie do katalogu jest taka sama jak bez niego.
func TestTreeSumFollowsSymlinkedDir(t *testing.T) {
	dir := t.TempDir()
	if err := extractTarGz(bytes.NewReader(tarGz(t, map[string]string{"top/a.xsd": "a", "top/sub/b.xml": "b"})), dir, 1, nil); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip("symlinks not available:", err)
	}
	a, _, err1 := treeSum(dir)
	b, _, err2 := treeSum(link)
	if err1 != nil || err2 != nil || a != b {
		t.Fatalf("treeSum via symlink: %s %v vs %s %v", b, err2, a, err1)
	}
}

func TestExtractRejectsEscapingPaths(t *testing.T) {
	for _, name := range []string{"top/../../evil", "/abs/evil", "c:/evil"} {
		dir := t.TempDir()
		err := extractTarGz(bytes.NewReader(tarGz(t, map[string]string{name: "x"})), dir, 0, nil)
		if err == nil {
			t.Errorf("%q: accepted", name)
		}
	}
}

func TestLooksLikeHTML(t *testing.T) {
	cases := map[string]bool{
		"\xef\xbb\xbf<?xml version=\"1.0\"?><a/>": false,
		"PK\x03\x04\x14\x00":                      false,
		"\n<!DOCTYPE html><html>":                 true,
		"<html><head><title>Not Found":            true,
	}
	for in, want := range cases {
		if got := looksLikeHTML([]byte(in)); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

const treeSumReference = "af2a37d086ae3007af6beb69df9cf0d1569699b41a4e2e0134a877a5db68afd0"

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Manifest bez zestawów (stan gozipgo do czasu wskazania pierwszego zestawu) to nie błąd: kod 0 i komunikat,
// także z -verify; nazwany zestaw, którego nie ma, jest błędem.
func TestEmptyManifest(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "testdata", "sources.json"), `{"note": "x", "sets": []}`)
	for _, extra := range [][]string{nil, {"-verify"}} {
		var out, errOut bytes.Buffer
		if code := run(append([]string{"-root", root}, extra...), &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", extra, code, errOut.String())
		}
		if !strings.Contains(out.String(), "nothing to fetch") {
			t.Errorf("%v: output %q", extra, out.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"-root", root, "-set", "missing"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), `no set named "missing"`) {
		t.Errorf("-set missing: exit %d, stderr %q", code, errOut.String())
	}
}

// Nieznane pole manifestu jest błędem: literówka w nazwie pola nie może po cichu wyłączyć sumy.
func TestManifestRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "testdata", "sources.json"), `{"sets": [{"name": "a", "sha265": "00"}]}`)
	var out, errOut bytes.Buffer
	if code := run([]string{"-root", root}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "sha265") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

// Zestaw rodzaju "files" bez sieci: plik bierze się z -from, przyjmowany jest tylko z sumą z manifestu, a drugi
// przebieg i -verify zastają go na miejscu.
func TestFilesSetFromLocalCopy(t *testing.T) {
	root, from := t.TempDir(), t.TempDir()
	good, bad := "archive bytes", "other bytes"
	writeTestFile(t, filepath.Join(from, "sub", "good.zip"), good)
	writeTestFile(t, filepath.Join(from, "bad.zip"), bad)
	manifest := `{"sets": [{"name": "local", "kind": "files", "dest": "testdata/fetched/local", "files": [
		{"path": "sub/good.zip", "urls": [], "sha256": "` + hexSum([]byte(good)) + `", "local_only": true},
		{"path": "bad.zip", "urls": [], "sha256": "` + hexSum([]byte(good)) + `", "local_only": true}]}]}`
	writeTestFile(t, filepath.Join(root, "testdata", "sources.json"), manifest)

	var out, errOut bytes.Buffer
	if code := run([]string{"-root", root, "-from", from}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1 (bad.zip has another checksum); stdout %q", code, out.String())
	}
	if !strings.Contains(out.String(), "local: 1/2 ok") || !strings.Contains(errOut.String(), "bad.zip") {
		t.Errorf("stdout %q, stderr %q", out.String(), errOut.String())
	}
	if b, err := os.ReadFile(filepath.Join(root, "testdata", "fetched", "local", "sub", "good.zip")); err != nil || string(b) != good {
		t.Errorf("good.zip: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "testdata", "fetched", "local", "bad.zip")); err == nil {
		t.Error("bad.zip written despite the checksum mismatch")
	}
	// Bez -from pliki tylko lokalne są pomijane, a obecny i zgodny liczy się jako „present”.
	out.Reset()
	errOut.Reset()
	if code := run([]string{"-root", root, "-verify"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "present 1") {
		t.Errorf("-verify: exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// Katalog docelowy poza testdata/fetched/ jest odrzucany, zanim cokolwiek zostanie pobrane albo usunięte.
func TestDestMustBeInsideFetched(t *testing.T) {
	for dest, ok := range map[string]bool{
		"testdata/fetched/set":       true,
		"testdata/fetched":           false,
		"testdata/interop":           false,
		".":                          false,
		"":                           false,
		"testdata/fetched/../../x":   false,
		"/testdata/fetched/x":        false,
		`testdata\fetched\x`:         false,
		"testdata/fetched/c:/x":      false,
		"testdata/fetchedelsewhere/": false,
	} {
		if err := checkDest(dest); (err == nil) != ok {
			t.Errorf("%q: %v, want ok=%v", dest, err, ok)
		}
	}
	root := t.TempDir()
	keep := filepath.Join(root, "testdata", "interop", "keep.zip")
	writeTestFile(t, keep, "x")
	writeTestFile(t, filepath.Join(root, "testdata", "sources.json"),
		`{"sets": [{"name": "evil", "kind": "archive", "dest": "testdata/interop", "url": "http://127.0.0.1:1/x.tar.gz"}]}`)
	var out, errOut bytes.Buffer
	if code := run([]string{"-root", root}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "not inside testdata/fetched/") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("repository file touched: %v", err)
	}
}

// TestExtractInclude: z archiwum z polem include na dysk trafiają tylko pliki spod podanych ścieżek.
func TestExtractInclude(t *testing.T) {
	dir := t.TempDir()
	data := tarGz(t, map[string]string{"top/src/code.c": "c", "top/test/a.zip": "a", "top/test/sub/b.zip": "b", "top/tests.txt": "x"})
	if err := extractTarGz(bytes.NewReader(data), dir, 1, []string{"test/"}); err != nil {
		t.Fatal(err)
	}
	_, n, err := treeSum(dir)
	if err != nil || n != 2 {
		t.Fatalf("got %d files (%v), want 2", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src")); err == nil {
		t.Error("src was extracted")
	}
}
