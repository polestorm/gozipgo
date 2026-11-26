package zipreader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

// open czyta archiwum z bajtów.
func open(data []byte, opts Options) (*Reader, error) {
	return New(bytes.NewReader(data), int64(len(data)), opts)
}

// openDir czyta koniec katalogu i katalog centralny, bez nagłówków lokalnych: testy etapu 1 budują archiwa,
// w których zmieniony jest tylko katalog. Pełne New sprawdzają testy układu, korpusy i fuzzer.
func openDir(data []byte, opts Options) (*Reader, error) {
	return newReader(bytes.NewReader(data), int64(len(data)), opts, false)
}

// wantCode sprawdza, że archiwum daje podany kod (pusty: jest przyjmowane), i zwraca czytnik oraz błąd.
func wantCode(t *testing.T, label string, data []byte, opts Options, code string) (*Reader, *Error) {
	t.Helper()
	r, err := openDir(data, opts)
	if got := codeOf(err); got != code {
		t.Errorf("%s: got %q (%v), want %q", label, got, err, code)
	}
	e, _ := err.(*Error)
	return r, e
}

func files(names ...string) []testkit.File {
	var fs []testkit.File
	for _, n := range names {
		fs = append(fs, testkit.File{Name: n})
	}
	return fs
}

// TestDirectoryEnd: szukanie rekordu końca katalogu i reguła jednego kandydata.
// REQ: DIR-001 DIR-002
func TestDirectoryEnd(t *testing.T) {
	valid := base(nil)
	for n := 0; n < format.EndSize; n++ {
		wantCode(t, fmt.Sprintf("%d bytes", n), valid[len(valid)-n:], Options{}, CodeNoDirectoryEnd)
	}
	if _, err := New(bytes.NewReader(nil), -1, Options{}); codeOf(err) != CodeNoDirectoryEnd {
		t.Errorf("negative size: %v", err)
	}
	wantCode(t, "random bytes", bytes.Repeat([]byte("PK\x03\x04 not an archive "), 10), Options{}, CodeNoDirectoryEnd)

	// Najdłuższy komentarz: rekord leży dokładnie na początku okna 22 + 65 535 bajtów.
	long := base(func(a *testkit.Archive) { a.End.SetComment(bytes.Repeat([]byte{'c'}, 65535)) })
	if r, _ := wantCode(t, "comment of 65535 bytes", long, Options{}, ""); r != nil && len(r.Comment()) != 65535 {
		t.Errorf("comment length %d", len(r.Comment()))
	}
	// Rekord poza oknem: po archiwum jest więcej bajtów, niż może mieć komentarz.
	wantCode(t, "end record outside the window", append(base(nil), make([]byte, 65536)...), Options{}, CodeNoDirectoryEnd)

	// Dane po komentarzu.
	_, e := wantCode(t, "trailing data", base(func(a *testkit.Archive) { a.Trailer = []byte("xyz") }), Options{}, CodeTrailingData)
	if e != nil && e.Offset != int64(len(valid)) {
		t.Errorf("trailing data reported at %d, want %d", e.Offset, len(valid))
	}
	// Komentarz zadeklarowany dłuższy niż plik: żaden rekord nie kończy się równo z plikiem.
	wantCode(t, "comment longer than the file", base(func(a *testkit.Archive) { a.End.CommentLength = 5 }), Options{}, CodeNoDirectoryEnd)

	// Sygnatura w komentarzu, która nie jest rekordem kończącym się równo z plikiem, nie jest drugą interpretacją.
	wantCode(t, "signature in the comment", base(func(a *testkit.Archive) {
		a.End.SetComment([]byte("PK\x05\x06 is how an end record starts, and this comment is longer than one"))
	}), Options{}, "")
	// Druga interpretacja: w komentarzu jest cały rekord, który też kończy się równo z plikiem.
	_, e = wantCode(t, "second record in the comment", base(func(a *testkit.Archive) {
		inner := testkit.End{Signature: format.SigEnd}
		a.End.SetComment(append([]byte("note"), inner.Bytes()...))
	}), Options{}, CodeMultipleDirectoryEnds)
	if e != nil && e.Offset != int64(len(valid)-format.EndSize) {
		// zgłaszany jest wcześniejszy z dwóch rekordów, znaleziony jako drugi przy szukaniu od końca
		t.Errorf("multiple ends reported at %d", e.Offset)
	}

	// Archiwum w archiwum (Stored): rekord końca wewnętrznego leży w danych, przed katalogiem zewnętrznego.
	nested := testkit.Zip(testkit.File{Name: "inner.zip", Data: valid})
	wantCode(t, "stored archive inside an archive", nested.Bytes(), Options{}, "")
	// Archiwum doklejone do archiwum: katalog drugiego nie leży tam, gdzie mówi jego koniec.
	wantCode(t, "two archives concatenated", append(base(nil), valid...), Options{}, CodeDirectoryOutOfBounds)
}

// TestMultiVolume: pola opisujące archiwum wieloczęściowe.
// REQ: DIR-003
func TestMultiVolume(t *testing.T) {
	for label, mut := range map[string]func(a *testkit.Archive){
		"this disk":            func(a *testkit.Archive) { a.End.Disk = 2 },
		"directory disk":       func(a *testkit.Archive) { a.End.DirDisk = 1 },
		"entries on this disk": func(a *testkit.Archive) { a.End.EntriesOnDisk = 0 },
		"entry disk start":     func(a *testkit.Archive) { a.Central[0].DiskStart = 3 },
	} {
		wantCode(t, label, base(mut), Options{}, CodeUnsupportedMultiVolume)
	}
}

// TestDirectoryBounds: katalog musi kończyć się dokładnie tam, gdzie zaczynają się rekordy końca.
// REQ: DIR-004
func TestDirectoryBounds(t *testing.T) {
	for label, data := range map[string][]byte{
		// dane przed archiwum (np. program samorozpakowujący): przesunięcia liczone od początku archiwum
		"prefix": append([]byte("#!/bin/sh\n"), base(nil)...),
		"gap between the directory and the end record": base(func(a *testkit.Archive) { a.AfterCentral = []byte{0, 0, 0, 0} }),
		"directory past the end record":                base(func(a *testkit.Archive) { a.End.DirSize += 30 }),
		"offset past the file":                         base(func(a *testkit.Archive) { a.End.DirOffset = 1 << 30 }),
		"zip64 offset and size overflow": zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) {
			a.Zip64End.DirOffset, a.Zip64End.DirSize = 1<<63, 1<<63
		}),
		"zip64 directory does not end at the zip64 record": zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) {
			a.AfterCentral = []byte{0}
			a.Zip64Locator.Offset++
		}),
	} {
		wantCode(t, label, data, Options{}, CodeDirectoryOutOfBounds)
	}
}

// TestZip64: rekordy ZIP64 końca katalogu, także dla archiwum o więcej niż 65 535 wpisach.
// REQ: DIR-005
func TestZip64(t *testing.T) {
	for _, mode := range []testkit.Zip64Mode{testkit.Zip64Records, testkit.Zip64Sentinels} {
		r, _ := wantCode(t, "zip64", zip64Base(mode, nil), Options{}, "")
		if r != nil && (len(r.Entries()) != 1 || r.Entries()[0].Name != "a.txt") {
			t.Errorf("zip64 mode %d: %+v", mode, r.Entries())
		}
	}
	// Każde pole zwykłego rekordu z osobna jako wartość graniczna.
	for i := 0; i < 6; i++ {
		data := zip64Base(testkit.Zip64Records, func(a *testkit.Archive) {
			switch i {
			case 0:
				a.End.Disk = format.Sentinel16
			case 1:
				a.End.DirDisk = format.Sentinel16
			case 2:
				a.End.EntriesOnDisk = format.Sentinel16
			case 3:
				a.End.Entries = format.Sentinel16
			case 4:
				a.End.DirSize = format.Sentinel32
			case 5:
				a.End.DirOffset = format.Sentinel32
			}
		})
		wantCode(t, fmt.Sprintf("sentinel in field %d", i), data, Options{}, "")
	}
	// Sygnatura lokalizatora bez spójnych rekordów ZIP64 to druga interpretacja, także bez wartości granicznych.
	wantCode(t, "locator signature in the last header", base(func(a *testkit.Archive) {
		a.Central[0].SetComment(append(testkit.U32(format.SigZip64Locator), make([]byte, 16)...))
		a.FixDirectory()
	}), Options{}, CodeUnsupportedMultiVolume) // lokalizator z liczbą dysków 0
	wantCode(t, "locator in the last header pointing nowhere", base(func(a *testkit.Archive) {
		loc := testkit.Zip64Locator{Signature: format.SigZip64Locator, Disks: 1}
		a.Central[0].SetComment(loc.Bytes())
		a.FixDirectory()
	}), Options{}, CodeZip64Inconsistent)

	// 70 000 wpisów: więcej, niż mieści zwykły rekord.
	many := make([]testkit.File, 70000)
	for i := range many {
		many[i].Name = fmt.Sprintf("f%05d", i)
	}
	data := testkit.Build(testkit.Zip64Sentinels, many...).Bytes()
	wantCode(t, "70000 entries, default limit", data, Options{}, CodeLimitEntries)
	r, _ := wantCode(t, "70000 entries, no limit", data, Options{MaxEntries: -1}, "")
	if r != nil && (len(r.Entries()) != 70000 || r.Entries()[69999].Name != "f69999" || r.Entries()[69999].Index != 69999) {
		t.Errorf("70000 entries: got %d", len(r.Entries()))
	}
}

// TestLimits: limity są włączone przy zerowych opcjach, sprawdzane przed alokacją i wyłączane wartością ujemną.
// REQ: DIR-006 NAME-007 SAFE-002
func TestLimits(t *testing.T) {
	three := testkit.Zip(files("a", "b", "c")...).Bytes()
	wantCode(t, "entries over the limit", three, Options{MaxEntries: 2}, CodeLimitEntries)
	wantCode(t, "entries at the limit", three, Options{MaxEntries: 3}, "")
	wantCode(t, "directory over the limit", three, Options{MaxDirectorySize: 140}, CodeLimitDirectorySize)
	wantCode(t, "directory at the limit", three, Options{MaxDirectorySize: 141}, "")

	long := strings.Repeat("n", DefaultMaxNameLength+1)
	withName := testkit.Zip(testkit.File{Name: long}).Bytes()
	wantCode(t, "name over the default limit", withName, Options{}, CodeLimitNameLength)
	wantCode(t, "name, limit off", withName, Options{MaxNameLength: -1}, "")
	wantCode(t, "name at the default limit", testkit.Zip(testkit.File{Name: long[1:]}).Bytes(), Options{}, "")

	commented := testkit.Zip(testkit.File{Name: "a", Comment: "12345", Extra: testkit.Extra(0x5455, []byte{1, 2})}).Bytes()
	wantCode(t, "entry comment over the limit", commented, Options{MaxCommentLength: 4}, CodeLimitCommentLength)
	wantCode(t, "extra over the limit", commented, Options{MaxExtraLength: 5}, CodeLimitExtraLength)
	wantCode(t, "comment and extra at the limit", commented, Options{MaxCommentLength: 5, MaxExtraLength: 6}, "")
	archiveComment := base(func(a *testkit.Archive) { a.End.SetComment([]byte("12345")) })
	wantCode(t, "archive comment over the limit", archiveComment, Options{MaxCommentLength: 4}, CodeLimitCommentLength)

	// Liczba wpisów sprzeczna z rozmiarem katalogu jest odrzucana bez czytania katalogu i bez alokacji zależnej
	// od zadeklarowanej liczby (SAFE-006): mierzymy pamięć przydzieloną w czasie New.
	huge := zip64Base(testkit.Zip64Sentinels, func(a *testkit.Archive) {
		a.Zip64End.Entries, a.Zip64End.EntriesOnDisk = 1<<62, 1<<62
	})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	wantCode(t, "2^62 entries declared", huge, Options{MaxEntries: -1}, CodeDirectoryCountMismatch)
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 1<<20 {
		t.Errorf("rejecting a declared count of 2^62 allocated %d bytes", n)
	}
	wantCode(t, "65535 entries declared in 51 bytes", base(func(a *testkit.Archive) {
		a.End.Entries, a.End.EntriesOnDisk = 0xFFFE, 0xFFFE
	}), Options{}, CodeDirectoryCountMismatch)
}

// TestDirectoryHeaders: liczba nagłówków i ich łączny rozmiar równają się wartościom z końca katalogu.
// REQ: DIR-007
func TestDirectoryHeaders(t *testing.T) {
	three := func(mut func(a *testkit.Archive)) []byte {
		a := testkit.Zip(files("a", "b", "c")...)
		mut(a)
		return a.Bytes()
	}
	_, e := wantCode(t, "fewer headers declared than present", three(func(a *testkit.Archive) {
		a.End.Entries, a.End.EntriesOnDisk = 2, 2
	}), Options{}, CodeDirectoryCountMismatch)
	if e != nil && (e.Entry != -1 || e.Offset != 3*31+2*47) {
		t.Errorf("count mismatch reported for entry %d at %d", e.Entry, e.Offset)
	}
	_, e = wantCode(t, "bad signature of the second header", three(func(a *testkit.Archive) {
		a.Central[1].Signature = format.SigLocalHeader
	}), Options{}, CodeDirectorySignature)
	if e != nil && (e.Entry != 1 || e.Offset != 3*31+47) {
		t.Errorf("bad signature reported for entry %d at %d", e.Entry, e.Offset)
	}
	// Długość nazwy przesuwa granicę nagłówka: następny „nagłówek” nie ma sygnatury.
	wantCode(t, "name length one too small", three(func(a *testkit.Archive) { a.Central[0].NameLength = 0 }),
		Options{}, CodeNameEmpty)
	wantCode(t, "comment length swallows the next header", three(func(a *testkit.Archive) { a.Central[0].CommentLength = 47 }),
		Options{}, CodeDirectoryCountMismatch)
	wantCode(t, "comment length past the directory", three(func(a *testkit.Archive) { a.Central[0].CommentLength = 9999 }),
		Options{}, CodeDirectoryTruncated)
	// Ostatni nagłówek urwany po części stałej: katalog jest dość duży na trzy nagłówki, ale nazwa się nie mieści.
	wantCode(t, "last header cut short", func() []byte {
		a := testkit.Zip(files("a", "b", "a-name-of-twenty-char")...)
		cut := a.Central[2].Bytes()[:50]
		a.Central = a.Central[:2]
		a.AfterCentral = cut
		a.End.DirSize = 2*47 + 50
		return a.Bytes()
	}(), Options{}, CodeDirectoryTruncated)
	wantCode(t, "directory ends inside a signature", three(func(a *testkit.Archive) {
		a.Central = a.Central[:2]
		a.AfterCentral = []byte{'P', 'K'}
		a.End.DirSize = 2*47 + 2
		a.End.Entries, a.End.EntriesOnDisk = 2, 2
	}), Options{}, CodeDirectoryCountMismatch)
}

// TestZip64Extra: pole ZIP64 nagłówka zawiera dokładnie pola, do których odsyłają wartości graniczne, w ustalonej
// kolejności. Sprawdzamy wszystkie 16 kombinacji.
// REQ: DIR-008
func TestZip64Extra(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		var parts [][]byte
		data := resized(func(h *testkit.CentralHeader) {
			h.UncompressedSize, h.CompressedSize, h.LocalOffset = 1001, 1002, 1003
			if mask&1 != 0 {
				h.UncompressedSize = format.Sentinel32
				parts = append(parts, testkit.U64(1<<40+1))
			}
			if mask&2 != 0 {
				h.CompressedSize = format.Sentinel32
				parts = append(parts, testkit.U64(1<<40+2))
			}
			if mask&4 != 0 {
				h.LocalOffset = format.Sentinel32
				parts = append(parts, testkit.U64(1<<40+3))
			}
			if mask&8 != 0 {
				h.DiskStart = format.Sentinel16
				parts = append(parts, testkit.U32(0))
			}
			if mask != 0 {
				h.SetExtra(append(testkit.Extra(0x5455, []byte{9}), testkit.Extra(format.ExtraZip64, parts...)...))
			}
		})
		r, _ := wantCode(t, fmt.Sprintf("mask %04b", mask), data, Options{}, "")
		if r == nil {
			continue
		}
		e := r.Entries()[0]
		want := [3]uint64{1001, 1002, 1003}
		for i := range want {
			if mask&(1<<i) != 0 {
				want[i] = 1<<40 + uint64(i) + 1
			}
		}
		if got := [3]uint64{e.UncompressedSize, e.CompressedSize, e.LocalHeaderOffset}; got != want {
			t.Errorf("mask %04b: got %v, want %v", mask, got, want)
		}
	}
}

// TestZip64ExtraFullForm: pole ZIP64 z polami, do których nagłówek nie odsyła (postać pełna, jak w archiwum
// test/success/zip64.zip z zestawu yauzl), jest przyjmowane tylko wtedy, gdy każde odczytanie daje te same wartości.
// REQ: DIR-008
func TestZip64ExtraFullForm(t *testing.T) {
	type h = testkit.CentralHeader
	full := func(us, cs, off uint64, disk ...uint32) []byte {
		parts := [][]byte{testkit.U64(us), testkit.U64(cs), testkit.U64(off)}
		for _, d := range disk {
			parts = append(parts, testkit.U32(d))
		}
		return testkit.Extra(format.ExtraZip64, parts...)
	}
	for _, c := range []struct {
		label string
		mut   func(x *h)
		code  string
	}{
		{"sizes refer, offset repeated and equal", func(x *h) {
			x.UncompressedSize, x.CompressedSize, x.LocalOffset = format.Sentinel32, format.Sentinel32, 7
			x.SetExtra(full(3, 5, 7))
		}, ""},
		{"sizes refer, offset and disk repeated and equal", func(x *h) {
			x.UncompressedSize, x.CompressedSize, x.LocalOffset = format.Sentinel32, format.Sentinel32, 7
			x.SetExtra(full(3, 5, 7, 0))
		}, ""},
		{"sizes refer, offset repeated and different", func(x *h) {
			x.UncompressedSize, x.CompressedSize, x.LocalOffset = format.Sentinel32, format.Sentinel32, 7
			x.SetExtra(full(3, 5, 8))
		}, CodeZip64Inconsistent},
		{"only uncompressed size refers, compressed size repeated and equal", func(x *h) {
			x.UncompressedSize, x.CompressedSize = format.Sentinel32, 5
			x.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(3), testkit.U64(5)))
		}, ""},
		// Nagłówek odsyła tylko do rozmiaru skompresowanego: czytanie po kolei wzięłoby pierwsze 8 bajtów,
		// czytanie pozycyjne drugie. Zgodne są tylko wtedy, gdy oba rozmiary są równe.
		{"only compressed size refers, both readings agree", func(x *h) {
			x.UncompressedSize, x.CompressedSize = 5, format.Sentinel32
			x.SetExtra(full(5, 5, uint64(x.LocalOffset)))
		}, ""},
		{"only compressed size refers, the readings disagree", func(x *h) {
			x.UncompressedSize, x.CompressedSize = 3, format.Sentinel32
			x.SetExtra(full(3, 5, uint64(x.LocalOffset)))
		}, CodeZip64Inconsistent},
		{"disk repeated and non-zero", func(x *h) {
			x.UncompressedSize, x.CompressedSize = format.Sentinel32, format.Sentinel32
			x.SetExtra(full(3, 5, uint64(x.LocalOffset), 1))
		}, CodeZip64Inconsistent},
		{"length that is no whole number of fields", func(x *h) {
			x.UncompressedSize = format.Sentinel32
			x.SetExtra(testkit.Extra(format.ExtraZip64, testkit.U64(3), []byte{0, 0, 0, 0}))
		}, CodeZip64Inconsistent},
		{"no field refers", func(x *h) {
			x.SetExtra(full(uint64(x.UncompressedSize), uint64(x.CompressedSize), uint64(x.LocalOffset)))
		},
			CodeZip64Inconsistent},
	} {
		r, _ := wantCode(t, c.label, resized(c.mut), Options{}, c.code)
		if r != nil && c.label == "sizes refer, offset repeated and equal" {
			if e := r.Entries()[0]; e.UncompressedSize != 3 || e.CompressedSize != 5 || e.LocalHeaderOffset != 7 {
				t.Errorf("%s: got sizes %d, %d and offset %d", c.label, e.UncompressedSize, e.CompressedSize, e.LocalHeaderOffset)
			}
		}
	}
}

// TestEntryOrder: lista jest w kolejności katalogu centralnego, także gdy dane leżą w pliku w innej kolejności.
// REQ: DIR-009
func TestEntryOrder(t *testing.T) {
	a := testkit.Zip(files("first", "second", "third")...)
	a.Central[0], a.Central[2] = a.Central[2], a.Central[0]
	r, _ := wantCode(t, "reordered directory", a.Bytes(), Options{}, "")
	if r == nil {
		return
	}
	var got []string
	for i, e := range r.Entries() {
		got = append(got, e.Name)
		if e.Index != i {
			t.Errorf("entry %q has index %d, want %d", e.Name, e.Index, i)
		}
	}
	if strings.Join(got, " ") != "third second first" {
		t.Errorf("order %v", got)
	}
	if e := r.Entries()[0]; e.LocalHeaderOffset != 2*(30+5)+1 || e.DirectoryOffset != int64(a.DirOffset()) {
		t.Errorf("offsets of the first directory entry: local %d, directory %d", e.LocalHeaderOffset, e.DirectoryOffset)
	}
}

// TestTimestamp: data i czas są wydawane tak, jak zapisano, bez strefy czasowej; wartości spoza kalendarza
// dostają znacznik.
// REQ: DIR-010 DET-003
func TestTimestamp(t *testing.T) {
	for _, c := range []struct {
		date, time uint16
		want       Timestamp
	}{
		{testkit.DOSDate(1980, 1, 1), 0, Timestamp{1980, 1, 1, 0, 0, 0, true}},
		{testkit.DOSDate(2026, 11, 26), testkit.DOSTime(23, 59, 58), Timestamp{2026, 11, 26, 23, 59, 58, true}},
		{testkit.DOSDate(2024, 2, 29), 0, Timestamp{2024, 2, 29, 0, 0, 0, true}},
		{testkit.DOSDate(2025, 2, 29), 0, Timestamp{2025, 2, 29, 0, 0, 0, false}},
		{testkit.DOSDate(2000, 2, 29), 0, Timestamp{2000, 2, 29, 0, 0, 0, true}},
		{testkit.DOSDate(2100, 2, 29), 0, Timestamp{2100, 2, 29, 0, 0, 0, false}},
		{testkit.DOSDate(2107, 12, 31), 0, Timestamp{2107, 12, 31, 0, 0, 0, true}},
		{testkit.DOSDate(2026, 4, 31), 0, Timestamp{2026, 4, 31, 0, 0, 0, false}},
		{testkit.DOSDate(2026, 13, 1), 0, Timestamp{2026, 13, 1, 0, 0, 0, false}},
		{testkit.DOSDate(2026, 1, 0), 0, Timestamp{2026, 1, 0, 0, 0, 0, false}},
		{0, 0, Timestamp{1980, 0, 0, 0, 0, 0, false}},
		{testkit.DOSDate(2026, 1, 1), 24 << 11, Timestamp{2026, 1, 1, 24, 0, 0, false}},
		{testkit.DOSDate(2026, 1, 1), 60 << 5, Timestamp{2026, 1, 1, 0, 60, 0, false}},
		{testkit.DOSDate(2026, 1, 1), 30, Timestamp{2026, 1, 1, 0, 0, 60, false}},
		{0xFFFF, 0xFFFF, Timestamp{2107, 15, 31, 31, 63, 62, false}},
	} {
		// Wynik nie może zależeć od strefy czasowej procesu.
		for _, tz := range []string{"UTC", "Pacific/Kiritimati"} {
			t.Setenv("TZ", tz)
			r, _ := wantCode(t, "timestamp", header(func(h *testkit.CentralHeader) { h.ModDate, h.ModTime = c.date, c.time }), Options{}, "")
			if r != nil && r.Entries()[0].Modified != c.want {
				t.Errorf("date %#04x time %#04x (TZ=%s): got %+v, want %+v", c.date, c.time, tz, r.Entries()[0].Modified, c.want)
			}
		}
	}
}

// TestDirectoryEntries: wpis katalogu ma rozmiary zero.
// REQ: DIR-011
func TestDirectoryEntries(t *testing.T) {
	r, _ := wantCode(t, "directories", testkit.Zip(files("d/", "d/e/", "d/e/f.txt")...).Bytes(), Options{}, "")
	if r != nil && (!r.Entries()[0].IsDir || !r.Entries()[1].IsDir || r.Entries()[2].IsDir) {
		t.Errorf("IsDir: %+v", r.Entries())
	}
	_, e := wantCode(t, "directory with data", testkit.Zip(testkit.File{Name: "ok"}, testkit.File{Name: "d/", Data: []byte("x")}).Bytes(),
		Options{}, CodeDirectoryEntrySize)
	if e != nil && (e.Entry != 1 || e.Name != "d/") {
		t.Errorf("directory with data reported as entry %d %q", e.Entry, e.Name)
	}
}

// TestNames: reguły nazw i kolejność ich sprawdzania.
// REQ: NAME-001 NAME-002 NAME-003 NAME-004
func TestNames(t *testing.T) {
	for _, c := range []struct {
		name string
		utf8 bool
		code string
	}{
		{"a", false, ""},
		{"a b/c d.txt", false, ""},
		{"dir/", false, ""},
		{"~!@#$%^&()_+-={}[];',.txt", false, ""},
		{"...", false, ""},
		{"a/..b/c..", false, ""},
		{"zażółć gęślą jaźń.txt", true, ""},
		{"\U0001F600.txt", true, ""},
		{"", false, CodeNameEmpty},
		{"a\x00b", false, CodeNameNUL},
		{"\x00", true, CodeNameNUL},
		{"za\xc5\xbc", false, CodeNameEncoding},  // UTF-8 bez bitu 11
		{"za\xbf", false, CodeNameEncoding},      // inna strona kodowa
		{"za\xc5", true, CodeNameEncoding},       // urwany UTF-8 z bitem 11
		{"\xed\xa0\x80", true, CodeNameEncoding}, // surogat
		{"\xc0\xaf", true, CodeNameEncoding},     // nadmiarowe kodowanie „/”
		{"a\nb", false, CodeNameControl},
		{"a\x1b[31m", true, CodeNameControl},
		{"a\x7f", false, CodeNameControl},
		{`a\b`, false, CodeNameBackslash},
		{`..\..\x`, false, CodeNameBackslash},
		{"/a", false, CodeNameAbsolute},
		{"/", false, CodeNameAbsolute},
		{"C:/a", false, CodeNameAbsolute},
		{"c:a", false, CodeNameAbsolute},
		{"Z:", false, CodeNameAbsolute},
		{"1:a", false, ""}, // cyfra nie jest literą dysku
		{"ab:c", false, ""},
		{"..", false, CodeNameTraversal},
		{"../a", false, CodeNameTraversal},
		{"a/../b", false, CodeNameTraversal},
		{"a/..", false, CodeNameTraversal},
		{"a/../", false, CodeNameTraversal},
		{".", false, CodeNameTraversal},
		{"./a", false, CodeNameTraversal},
		{"a/./b", false, CodeNameTraversal},
		{"a//b", false, CodeNameTraversal},
		{"a//", false, CodeNameTraversal},
		// kolejność: bajt zerowy przed kodowaniem, kodowanie przed ukośnikiem, ukośnik przed ścieżką
		{"\\\x00\xff", false, CodeNameNUL},
		{"/\\\xff", false, CodeNameEncoding},
		{"/..\\", false, CodeNameBackslash},
		{"/..", false, CodeNameAbsolute},
	} {
		data := resized(func(h *testkit.CentralHeader) {
			h.SetName([]byte(c.name))
			if c.utf8 {
				h.Flags |= format.FlagUTF8
			}
			if strings.HasSuffix(c.name, "/") {
				h.CompressedSize, h.UncompressedSize = 0, 0 // wpis katalogu nie ma danych (DIR-011)
			}
		})
		r, e := wantCode(t, fmt.Sprintf("name %q", c.name), data, Options{}, c.code)
		if r != nil && r.Entries()[0].Name != c.name {
			t.Errorf("name %q read as %q", c.name, r.Entries()[0].Name)
		}
		if e != nil && (e.Entry != 0 || e.Name != c.name) {
			t.Errorf("name %q: error names entry %d %q", c.name, e.Entry, e.Name)
		}
	}
}

// TestUnflaggedNames: nazwy spoza ASCII bez bitu 11 są przyjmowane tylko na mocy opcji, a jej użycie jest
// widoczne w wyniku.
// REQ: NAME-002
func TestUnflaggedNames(t *testing.T) {
	archive := func(raw string) []byte {
		a := testkit.Zip(testkit.File{Name: "plain.txt"}, testkit.File{Name: "x"})
		a.Central[1].SetName([]byte(raw))
		a.Central[1].Flags &^= format.FlagUTF8
		a.FixDirectory()
		return a.Bytes()
	}
	// „zażółć” w UTF-8: jako CP437 to inne znaki, jako UTF-8 to nazwa, którą zapisał autor.
	utf := archive("za\xc5\xbc\xc3\xb3\xc5\x82\xc4\x87")
	wantCode(t, "strict", utf, Options{}, CodeNameEncoding)
	r, _ := wantCode(t, "UTF8", utf, Options{UnflaggedNames: UTF8}, "")
	if r != nil && (r.Entries()[1].Name != "zażółć" || !reflect.DeepEqual(r.Relaxed(), []Relaxation{{"UnflaggedNames", 1}})) {
		t.Errorf("UTF8: name %q, relaxed %v", r.Entries()[1].Name, r.Relaxed())
	}
	r, _ = wantCode(t, "CP437", utf, Options{UnflaggedNames: CP437}, "")
	if r != nil && (r.Entries()[1].Name != "za┼╝├│┼é─ç" || len(r.Relaxed()) != 1) {
		t.Errorf("CP437: name %q, relaxed %v", r.Entries()[1].Name, r.Relaxed())
	}
	// „Ærøskøbing” nie istnieje w CP437, ale „Æble” tak: bajt 0x92.
	cp := archive("\x92ble \x9b1.txt")
	wantCode(t, "CP437 bytes as UTF8", cp, Options{UnflaggedNames: UTF8}, CodeNameEncoding)
	r, _ = wantCode(t, "CP437 bytes as CP437", cp, Options{UnflaggedNames: CP437}, "")
	if r != nil && r.Entries()[1].Name != "Æble ¢1.txt" {
		t.Errorf("CP437: name %q", r.Entries()[1].Name)
	}
	// Opcja nie zmienia niczego dla nazw w ASCII ani dla nazw z bitem 11 i nie zostawia wtedy śladu.
	for _, enc := range []NameEncoding{CP437, UTF8} {
		r, _ := wantCode(t, "ASCII names with the option", testkit.Zip(files("a", "zażółć")...).Bytes(), Options{UnflaggedNames: enc}, "")
		if r != nil && len(r.Relaxed()) != 0 {
			t.Errorf("encoding %d: relaxed %v for an archive that needs no relaxation", enc, r.Relaxed())
		}
	}
	// Reguły nazw obowiązują także po zdekodowaniu.
	wantCode(t, "traversal in a CP437 name", archive("\x92/../x"), Options{UnflaggedNames: CP437}, CodeNameTraversal)
}

// cp437SHA256 to SHA-256 tekstu UTF-8 znaków CP437 dla bajtów 0x80–0xFF, policzone z wyniku kodeka „cp437”
// Pythona 3.9.6 (bytes(range(128, 256)).decode("cp437")), czyli z niezależnego źródła tej samej tablicy.
const cp437SHA256 = "6395cab87eae09bf6758684afcf3c8ed89423d4805a965c1ff438bbdd71f5816"

// TestCP437Table: tablica CP437 ma 128 różnych znaków i sumę kontrolną tekstu porównanego ze źródłem.
// REQ: NAME-002
func TestCP437Table(t *testing.T) {
	seen := map[rune]bool{}
	for _, r := range cp437Runes {
		if r < 0x80 || seen[r] {
			t.Errorf("CP437 table: character %U is ASCII or repeated", r)
		}
		seen[r] = true
	}
	sum := sha256.Sum256([]byte(cp437High))
	if got := hex.EncodeToString(sum[:]); got != cp437SHA256 {
		t.Errorf("CP437 table changed: SHA-256 of its UTF-8 text is %s", got)
	}
}

// TestDuplicates: wpisy, które nie mogą współistnieć w jednym drzewie.
// REQ: NAME-005
func TestDuplicates(t *testing.T) {
	for _, c := range []struct {
		names        []string
		fold         bool
		entry, other int // −1: brak konfliktu
	}{
		{[]string{"a", "b", "c"}, false, -1, -1},
		{[]string{"a", "b", "a"}, false, 2, 0},
		{[]string{"a", "a", "a"}, false, 1, 0},
		{[]string{"d/", "d/"}, false, 1, 0},
		{[]string{"a", "a/"}, false, 1, 0},
		{[]string{"a/", "a"}, false, 1, 0},
		{[]string{"a/b", "x", "a"}, false, 2, 0},
		{[]string{"a", "a-b", "a/b"}, false, 2, 0}, // „a-b” sortuje się bajtowo między „a” i „a/b”
		{[]string{"a", "a.b", "a0", "ab"}, false, -1, -1},
		{[]string{"a/", "a/b", "a/c/", "a/c/d"}, false, -1, -1},
		{[]string{"a/b/c", "a/b"}, false, 1, 0},
		{[]string{"a/b/c", "a"}, false, 1, 0},
		{[]string{"x", "a/b/c/d", "y", "a/b"}, false, 3, 1},
		{[]string{"A", "a"}, false, -1, -1},
		{[]string{"A", "a"}, true, 1, 0},
		{[]string{"Dir/File", "dir/file"}, true, 1, 0},
		{[]string{"Dir/x", "dir"}, true, 1, 0},
		{[]string{"é", "É"}, true, -1, -1}, // tylko litery ASCII
		{[]string{"b", "a", "b", "a"}, false, 2, 0},
	} {
		data := testkit.Zip(files(c.names...)...).Bytes()
		code := CodeNameDuplicate
		if c.entry < 0 {
			code = ""
		}
		_, e := wantCode(t, fmt.Sprint(c.names), data, Options{FoldCase: c.fold}, code)
		if e != nil && (e.Entry != c.entry || e.Other != c.other || e.Name != c.names[c.entry]) {
			t.Errorf("%v: conflict reported between %d and %d (%q), want %d and %d", c.names, e.Entry, e.Other, e.Name, c.entry, c.other)
		}
	}
}

// TestDuplicatesAgainstBruteForce: wykrywanie przez sortowanie daje to samo co porównanie każdej pary.
// REQ: NAME-009
func TestDuplicatesAgainstBruteForce(t *testing.T) {
	seed := uint32(1)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>16) % n
	}
	segments := []string{"a", "b", "A", "a-", "a.", "ab", "c"}
	for round := 0; round < 3000; round++ {
		fold := round%2 == 1
		var entries []Entry
		for i, n := 0, 1+next(7); i < n; i++ {
			name := segments[next(len(segments))]
			for d := next(3); d > 0; d-- {
				name += "/" + segments[next(len(segments))]
			}
			if next(4) == 0 {
				name += "/"
			}
			entries = append(entries, Entry{Index: i, Name: name, IsDir: strings.HasSuffix(name, "/")})
		}
		key := func(e Entry) string {
			k := strings.TrimSuffix(e.Name, "/")
			if fold {
				k = strings.ToLower(k)
			}
			return k
		}
		wantFirst, wantSecond := -1, -1
		for j := range entries {
			for i := 0; i < j && wantSecond < 0; i++ {
				ki, kj := key(entries[i]), key(entries[j])
				if ki == kj || !entries[i].IsDir && strings.HasPrefix(kj, ki+"/") || !entries[j].IsDir && strings.HasPrefix(ki, kj+"/") {
					wantFirst, wantSecond = i, j
				}
			}
		}
		err := checkDuplicates(entries, fold)
		gotFirst, gotSecond := -1, -1
		if e, ok := err.(*Error); ok {
			gotFirst, gotSecond = e.Other, e.Entry
		}
		// Która z kolidujących par jest zgłaszana, wyznacza porządek sortowania, a nie kolejność wpisów; sprawdzamy,
		// że konflikt jest wykryty wtedy i tylko wtedy, gdy istnieje, i że zgłoszona para naprawdę koliduje.
		if (gotSecond >= 0) != (wantSecond >= 0) || gotSecond >= 0 && gotFirst >= gotSecond {
			t.Fatalf("round %d fold %v %+v: got pair (%d, %d), brute force (%d, %d)", round, fold, names(entries), gotFirst, gotSecond, wantFirst, wantSecond)
		}
		if gotSecond >= 0 {
			ki, kj := key(entries[gotFirst]), key(entries[gotSecond])
			if !(ki == kj || !entries[gotFirst].IsDir && strings.HasPrefix(kj, ki+"/") || !entries[gotSecond].IsDir && strings.HasPrefix(ki, kj+"/")) {
				t.Fatalf("round %d %+v: reported pair (%d, %d) does not conflict", round, names(entries), gotFirst, gotSecond)
			}
		}
	}
}

func names(entries []Entry) []string {
	var s []string
	for _, e := range entries {
		s = append(s, e.Name)
	}
	return s
}

// TestUnicodePath: pole Unicode Path musi dawać tę samą nazwę co pole nazwy, także po zdekodowaniu z CP437.
// REQ: NAME-008
func TestUnicodePath(t *testing.T) {
	archive := func(raw, unicodeName string, flag bool) []byte {
		return resized(func(h *testkit.CentralHeader) {
			h.SetName([]byte(raw))
			if flag {
				h.Flags |= format.FlagUTF8
			}
			h.SetExtra(testkit.UnicodePath(unicodeName, []byte(raw)))
		})
	}
	wantCode(t, "same ASCII name", archive("a.txt", "a.txt", false), Options{}, "")
	wantCode(t, "same UTF-8 name", archive("zażółć", "zażółć", true), Options{}, "")
	wantCode(t, "different name", archive("a.txt", "b.txt", false), Options{}, CodeNameUnicodePath)
	wantCode(t, "name differing in case", archive("a.txt", "A.txt", false), Options{FoldCase: true}, CodeNameUnicodePath)
	// Nazwa w CP437 i jej zapis w UTF-8: zgodne po zdekodowaniu; bez opcji nazwa jest odrzucana wcześniej.
	wantCode(t, "CP437 name, strict", archive("\x92ble", "Æble", false), Options{}, CodeNameEncoding)
	wantCode(t, "CP437 name, decoded", archive("\x92ble", "Æble", false), Options{UnflaggedNames: CP437}, "")
	wantCode(t, "CP437 name, other Unicode name", archive("\x92ble", "Able", false), Options{UnflaggedNames: CP437}, CodeNameUnicodePath)
}

// TestExtraStructure: obszar pól dodatkowych musi być ciągiem bloków wypełniającym go dokładnie.
// REQ: FMT-005
func TestExtraStructure(t *testing.T) {
	for _, c := range []struct {
		extra []byte
		code  string
	}{
		{nil, ""},
		{testkit.Extra(0x5455), ""},
		{append(testkit.Extra(0x5455, []byte{1}), testkit.Extra(0xcafe)...), ""},
		{[]byte{1}, CodeExtraField},
		{[]byte{0x55, 0x54, 0}, CodeExtraField},
		{[]byte{0x55, 0x54, 1, 0}, CodeExtraField},
		{[]byte{0x55, 0x54, 0xFF, 0xFF, 0}, CodeExtraField},
		{append(testkit.Extra(0x5455, []byte{1}), 0), CodeExtraField},
	} {
		wantCode(t, fmt.Sprintf("extra %x", c.extra), resized(func(h *testkit.CentralHeader) { h.SetExtra(c.extra) }), Options{}, c.code)
	}
}

// TestUnsupportedOrder: gdy wpis używa kilku cech spoza zakresu, Unsupported podaje pierwszą w ustalonej
// kolejności: szyfrowanie, łatki, metoda, wersja.
// REQ: FMT-004 FMT-006
func TestUnsupportedOrder(t *testing.T) {
	for _, c := range []struct {
		flags, method, version uint16
		want                   string
	}{
		{0, 8, 20, ""},
		{0, 12, 46, CodeUnsupportedMethod},
		{0, 8, 46, CodeUnsupportedVersion},
		{format.FlagPatchData, 12, 46, CodeUnsupportedPatch},
		{format.FlagPatchData | format.FlagStrongEncryption, 12, 63, CodeUnsupportedEncryption},
		{format.FlagEncrypted, 99, 51, CodeUnsupportedEncryption},
	} {
		r, _ := wantCode(t, "unsupported", header(func(h *testkit.CentralHeader) {
			h.Flags, h.Method, h.VersionNeeded = c.flags, c.method, c.version
		}), Options{}, "")
		if r != nil && r.Entries()[0].Unsupported != c.want {
			t.Errorf("flags %#x method %d version %d: Unsupported %q, want %q", c.flags, c.method, c.version, r.Entries()[0].Unsupported, c.want)
		}
	}
}

// TestErrorOrder: gdy archiwum ma kilka wad, zgłaszana jest ta, którą wyznacza kolejność kroków z dokumentacji New.
// Każdy przypadek ma dwie wady z sąsiednich kroków; oczekiwany jest kod wcześniejszego.
// REQ: DET-005
func TestErrorOrder(t *testing.T) {
	type mut = func(a *testkit.Archive)
	steps := []struct {
		label string
		code  string
		mode  testkit.Zip64Mode
		mut   mut
	}{
		{"trailing data", CodeTrailingData, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Trailer = []byte{0} }},
		{"zip64 record", CodeZip64Inconsistent, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.Signature = 0 }},
		{"multi-volume", CodeUnsupportedMultiVolume, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.EntriesOnDisk += 7 }},
		{"bounds", CodeDirectoryOutOfBounds, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Zip64End.DirOffset-- }},
		{"count", CodeDirectoryCountMismatch, testkit.Zip64Sentinels, func(a *testkit.Archive) {
			a.Zip64End.Entries, a.Zip64End.EntriesOnDisk = a.Zip64End.Entries+100, a.Zip64End.EntriesOnDisk+100
		}},
		{"header signature", CodeDirectorySignature, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Central[0].Signature = 1 }},
		{"header truncated", CodeDirectoryTruncated, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Central[1].CommentLength = 9999 }},
		{"flag", CodeUnsupportedFlag, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Central[1].Flags |= 1 << 15 }},
		{"name", CodeNameTraversal, testkit.Zip64Sentinels, func(a *testkit.Archive) { copy(a.Central[1].Name, "..") }},
		{"extra", CodeExtraField, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Central[1].Extra[2] = 0xFF }},
		{"zip64 extra", CodeZip64Inconsistent, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Central[1].CompressedSize = format.Sentinel32 }},
		{"comment", CodeCommentEncoding, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Central[1].Comment[0] = 0xFF }},
		{"directory size", CodeDirectoryEntrySize, testkit.Zip64Sentinels, func(a *testkit.Archive) { a.Central[2].UncompressedSize = 1 }},
		{"duplicate", CodeNameDuplicate, testkit.Zip64Sentinels, func(a *testkit.Archive) { copy(a.Central[3].Name, "bb") }},
	}
	build := func(muts ...mut) []byte {
		a := testkit.Build(testkit.Zip64Sentinels,
			testkit.File{Name: "aa"},
			testkit.File{Name: "bb", Comment: "c", Extra: testkit.Extra(0x5455, []byte{1}), Flags: format.FlagUTF8},
			testkit.File{Name: "d/"},
			testkit.File{Name: "ee"})
		for _, m := range muts {
			m(a)
		}
		return a.Bytes()
	}
	for i, s := range steps {
		wantCode(t, s.label+" alone", build(s.mut), Options{}, s.code)
		for _, later := range steps[i+1:] {
			wantCode(t, s.label+" before "+later.label, build(s.mut, later.mut), Options{}, s.code)
		}
	}
	// Limity leżą między zgodnością liczby z rozmiarem a czytaniem nagłówków.
	wantCode(t, "count before limit", build(steps[4].mut), Options{MaxEntries: 1}, CodeDirectoryCountMismatch)
	wantCode(t, "limit before header", build(steps[5].mut), Options{MaxEntries: 1}, CodeLimitEntries)
}

// oneByteReader zwraca najwyżej jeden bajt na wywołanie, bez błędu.
type oneByteReader struct{ data []byte }

func (o oneByteReader) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(o.data)) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = o.data[off]
	return 1, nil
}

// sampleArchives zwraca archiwa do testów własności: korpus zgodności i archiwa z budowniczego, poprawne i nie.
func sampleArchives(t testing.TB) map[string][]byte {
	out := map[string][]byte{}
	paths, _ := filepath.Glob(filepath.Join("..", "testdata", "interop", "*.zip"))
	if len(paths) == 0 {
		t.Fatal("no archives in testdata/interop")
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(p)] = b
	}
	for key, c := range inventoryCases() {
		for i, b := range c.archives {
			out[fmt.Sprintf("%s[%d]", key, i)] = b
		}
	}
	return out
}

// dump zapisuje wynik czytania jako tekst: listę wpisów albo błąd z kodem, wpisem i przesunięciem.
func dump(r *Reader, err error) string {
	if err != nil {
		if e, ok := err.(*Error); ok {
			return fmt.Sprintf("error %s entry=%d other=%d name=%q offset=%d", e.Code, e.Entry, e.Other, e.Name, e.Offset)
		}
		return "error " + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "comment=%q relaxed=%v\n", r.Comment(), r.Relaxed())
	for _, e := range r.Entries() {
		fmt.Fprintf(&b, "%+v\n", e)
	}
	return b.String()
}

// TestOneByteReads: wynik nie zależy od tego, jak io.ReaderAt dzieli odczyty.
// REQ: DET-004
func TestOneByteReads(t *testing.T) {
	for label, data := range sampleArchives(t) {
		for _, opts := range []Options{{}, {UnflaggedNames: UTF8}} {
			want := dump(open(data, opts))
			if got := dump(New(oneByteReader{data}, int64(len(data)), opts)); got != want {
				t.Errorf("%s: one byte per read gives\n%s\nwant\n%s", label, got, want)
			}
		}
	}
}

// TestDeterministic: dziesięć przebiegów daje te same listy wpisów i te same błędy (kod, wpis, przesunięcie).
// REQ: DET-001
func TestDeterministic(t *testing.T) {
	archives := sampleArchives(t)
	first := map[string]string{}
	for run := 0; run < 10; run++ {
		for label, data := range archives { // kolejność iteracji po mapie jest inna w każdym przebiegu
			got := dump(open(data, Options{UnflaggedNames: UTF8}))
			if run == 0 {
				first[label] = got
			} else if got != first[label] {
				t.Fatalf("%s: run %d differs from run 0", label, run)
			}
		}
	}
}

// failingReader zwraca błąd przy odczycie numer failAt (od 0); inne odczyty przepuszcza.
type failingReader struct {
	data   []byte
	failAt int
	calls  int
	err    error
}

func (f *failingReader) ReadAt(p []byte, off int64) (int, error) {
	f.calls++
	if f.calls-1 == f.failAt {
		return 0, f.err
	}
	return bytes.NewReader(f.data).ReadAt(p, off)
}

// TestReaderErrors: błąd io.ReaderAt jest zwracany bez zmiany, a żadne wejście nie powoduje paniki: źródło
// zawodzące przy każdym kolejnym odczycie, źródło krótsze niż podany rozmiar, plik obcięty w każdym miejscu
// i plik z każdym bajtem zmienionym.
// REQ: SAFE-007
func TestReaderErrors(t *testing.T) {
	boom := errors.New("boom")
	data := testkit.Build(testkit.Zip64Sentinels, files("a", "b/", "b/c")...).Bytes()
	for failAt := 0; ; failAt++ {
		f := &failingReader{data: data, failAt: failAt, err: boom}
		_, err := New(f, int64(len(data)), Options{})
		if f.calls <= failAt {
			if err != nil {
				t.Errorf("no read failed, but New returned %v", err)
			}
			if failAt < 3 {
				t.Errorf("only %d reads: the test does not cover the ZIP64 records and the directory", failAt)
			}
			break
		}
		if err != boom {
			t.Errorf("read %d failed with %v, New returned %v", failAt, boom, err)
		}
	}
	// Źródło krótsze niż podany rozmiar: koniec danych w środku odczytu.
	if _, err := New(bytes.NewReader(data[:len(data)-1]), int64(len(data)), Options{}); err != io.ErrUnexpectedEOF {
		t.Errorf("source shorter than size: %v", err)
	}
	// Źródło, które nie robi postępu i nie zgłasza błędu.
	if _, err := New(stuckReader{}, 100, Options{}); err != io.ErrNoProgress {
		t.Errorf("stuck source: %v", err)
	}
	for label, valid := range map[string][]byte{"zip64": data, "plain": testkit.Zip(files("a", "b/", "b/c")...).Bytes()} {
		for n := 0; n < len(valid); n++ {
			if _, err := openDir(valid[:n], Options{}); err == nil {
				t.Errorf("%s cut to %d of %d bytes was accepted", label, n, len(valid))
			} else if _, ok := err.(*Error); !ok {
				t.Errorf("%s cut to %d bytes: %v is not an *Error", label, n, err)
			}
		}
		for i := range valid {
			for _, x := range []byte{0x01, 0x80, 0xFF} {
				mutated := append([]byte(nil), valid...)
				mutated[i] ^= x
				if _, err := openDir(mutated, Options{}); err != nil {
					if _, ok := err.(*Error); !ok {
						t.Errorf("%s with byte %d changed: %v is not an *Error", label, i, err)
					}
				}
			}
		}
	}
}

type stuckReader struct{}

func (stuckReader) ReadAt(p []byte, off int64) (int, error) { return 0, nil }

// TestPathological: czas New rośnie najwyżej liniowo-logarytmicznie z liczbą wpisów i liniowo z długością nazw
// oraz liczbą pól dodatkowych: podwojenie wejścia zwiększa czas najwyżej 2,5 raza (z zapasem na szum: 3,5).
// REQ: NAME-009
func TestPathological(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	measure := func(data []byte) time.Duration {
		best := time.Duration(1 << 62)
		for i := 0; i < 7; i++ {
			start := time.Now()
			if _, err := openDir(data, Options{MaxEntries: -1, MaxNameLength: -1, MaxDirectorySize: -1}); err != nil {
				t.Fatal(err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	build := func(n, nameLen, extras int) []byte {
		// Nazwy o długim wspólnym początku: najgorszy przypadek dla porównań przy sortowaniu.
		prefix := strings.Repeat("d/", nameLen/2)
		var x []byte
		for i := 0; i < extras; i++ {
			x = append(x, testkit.Extra(0x5455)...)
		}
		fs := make([]testkit.File, n)
		for i := range fs {
			fs[i] = testkit.File{Name: fmt.Sprintf("%s%07d", prefix, (i*7919)%n), Extra: x}
		}
		return testkit.Build(testkit.Zip64Sentinels, fs...).Bytes()
	}
	for _, c := range []struct {
		label        string
		small, large []byte
	}{
		{"entries", build(20000, 40, 0), build(40000, 40, 0)},
		{"name length", build(3000, 1000, 0), build(3000, 2000, 0)},
		{"extra fields", build(3000, 20, 4000), build(3000, 20, 8000)},
	} {
		small, large := measure(c.small), measure(c.large)
		ratio := float64(large) / float64(small)
		t.Logf("%s: %v and %v, ratio %.2f", c.label, small, large, ratio)
		if ratio > 3.5 {
			t.Errorf("%s: doubling the input multiplied the time by %.2f", c.label, ratio)
		}
	}
}
