package zipreader

import (
	"archive/zip"
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

// stdlibContent zwraca treść pierwszego wpisu według archive/zip (czarna skrzynka).
func stdlibContent(t *testing.T, data []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("archive/zip: %v", err)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("archive/zip: %v", err)
	}
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("archive/zip: %v", err)
	}
	return string(b)
}

// TestAllowVersionHostByte: „version needed to extract” z numerem systemu w starszym bajcie (788 = Unix, 2.0;
// tak zapisane są dwa dokumenty z danych testowych LibreOffice w korpusie godocxgo). W trybie ścisłym wpis jest
// poza zakresem; z opcją liczy się młodszy bajt, treść jest ta sama co w archive/zip, a Relaxed() to pokazuje.
// REQ: FMT-006
func TestAllowVersionHostByte(t *testing.T) {
	with := func(v uint16) []byte {
		return member(plain, func(a *testkit.Archive, m *testkit.Member) {
			m.Header.VersionNeeded, a.Central[0].VersionNeeded = v, v
		})
	}
	data := with(3<<8 | 20)
	r, _ := wantFull(t, "strict", data, Options{}, "")
	if r == nil {
		return
	}
	if r.Entries()[0].Unsupported != CodeUnsupportedVersion || len(r.Relaxed()) != 0 {
		t.Errorf("strict: Unsupported %q, relaxed %v", r.Entries()[0].Unsupported, r.Relaxed())
	}
	if _, err := r.Open(r.Entries()[0]); err == nil || err.(*Error).Code != CodeUnsupportedVersion {
		t.Errorf("strict: Open gives %v", err)
	}
	r, _ = wantFull(t, "with the option", data, Options{AllowVersionHostByte: true}, "")
	if r != nil {
		if got := content(t, r, 0); got != "hello" || got != stdlibContent(t, data) {
			t.Errorf("with the option: content %q", got)
		}
		if e := r.Entries()[0]; e.Unsupported != "" || e.VersionNeeded != 788 {
			t.Errorf("with the option: %+v", e)
		}
		if want := []Relaxation{{Option: "AllowVersionHostByte", Entry: 0}}; !reflect.DeepEqual(r.Relaxed(), want) {
			t.Errorf("with the option: relaxed %v", r.Relaxed())
		}
	}
	// Opcja nie przyjmuje wersji, której młodszy bajt jest ponad 4.5, i nie zostawia śladu, gdy nie była potrzebna.
	for _, v := range []uint16{46, 63, 3<<8 | 46, 0xFFFF} {
		if r, _ := wantFull(t, "too new", with(v), Options{AllowVersionHostByte: true}, ""); r != nil &&
			(r.Entries()[0].Unsupported != CodeUnsupportedVersion || len(r.Relaxed()) != 0) {
			t.Errorf("version %d with the option: Unsupported %q, relaxed %v", v, r.Entries()[0].Unsupported, r.Relaxed())
		}
	}
	if r, _ := wantFull(t, "not needed", with(20), Options{AllowVersionHostByte: true}, ""); r != nil && len(r.Relaxed()) != 0 {
		t.Errorf("version 20 with the option: relaxed %v", r.Relaxed())
	}
}

// TestAllowUnusedZip64: nagłówek lokalny z polem ZIP64 z samych zer obok prawdziwych rozmiarów (miejsce
// zarezerwowane i niewypełnione; tak zapisane są dwa dokumenty z danych testowych LibreOffice w korpusie
// godocxgo). W trybie ścisłym archiwum jest odrzucane; z opcją obowiązują rozmiary nagłówka. Pole o innych
// wartościach niż nagłówek opcja nadal odrzuca.
// REQ: ENT-002
func TestAllowUnusedZip64(t *testing.T) {
	with := func(usize, csize uint64, mut func(m *testkit.Member)) []byte {
		return member(plain, func(a *testkit.Archive, m *testkit.Member) {
			m.Header.Extra = testkit.Extra(format.ExtraZip64, testkit.U64(usize), testkit.U64(csize))
			m.Header.ExtraLength = uint16(len(m.Header.Extra))
			if mut != nil {
				mut(m)
			}
		})
	}
	data := with(0, 0, nil)
	wantFull(t, "strict", data, Options{}, CodeZip64Inconsistent)
	r, _ := wantFull(t, "with the option", data, Options{AllowUnusedZip64: true}, "")
	if r != nil {
		if got := content(t, r, 0); got != "hello" || got != stdlibContent(t, data) {
			t.Errorf("with the option: content %q", got)
		}
		if want := []Relaxation{{Option: "AllowUnusedZip64", Entry: 0}}; !reflect.DeepEqual(r.Relaxed(), want) {
			t.Errorf("with the option: relaxed %v", r.Relaxed())
		}
		if e := r.Entries()[0]; e.UncompressedSize != 5 || e.DataOffset != 30+5+20 {
			t.Errorf("with the option: %+v", e)
		}
	}
	opts := Options{AllowUnusedZip64: true}
	wantFull(t, "one size zero, the other wrong", with(0, 7, nil), opts, CodeZip64Inconsistent)
	wantFull(t, "both sizes wrong", with(6, 6, nil), opts, CodeZip64Inconsistent)
	// Wartość graniczna odsyła do pola: zera nie są wtedy pustym miejscem, tylko rozmiarem sprzecznym z drugim polem.
	wantFull(t, "marker with zeros", with(0, 0, func(m *testkit.Member) { m.Header.UncompressedSize = format.Sentinel32 }), opts, CodeZip64Inconsistent)
	// Pole zgodne z nagłówkiem nie potrzebuje opcji i nie zostawia śladu.
	if r, _ := wantFull(t, "equal values", with(5, 5, nil), opts, ""); r != nil && len(r.Relaxed()) != 0 {
		t.Errorf("equal values: relaxed %v", r.Relaxed())
	}
	// Dwa wpisy z pustym polem: lista poluzowań w kolejności katalogu.
	a := testkit.Zip(testkit.File{Name: "a.txt", Data: []byte("one")}, testkit.File{Name: "b.txt", Data: []byte("two")})
	for i := range a.Members {
		a.Members[i].Header.Extra = testkit.Extra(format.ExtraZip64, testkit.U64(0), testkit.U64(0))
		a.Members[i].Header.ExtraLength = 20
	}
	a.Relayout()
	if r, _ := wantFull(t, "two entries", a.Bytes(), opts, ""); r != nil {
		if want := []Relaxation{{Option: "AllowUnusedZip64", Entry: 0}, {Option: "AllowUnusedZip64", Entry: 1}}; !reflect.DeepEqual(r.Relaxed(), want) {
			t.Errorf("two entries: relaxed %v", r.Relaxed())
		}
	}
}
