package zipreader

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/polestorm/gozipgo/internal/format"
	"github.com/polestorm/gozipgo/internal/testkit"
)

// cp852High to znaki IBM Code Page 852 (Europa Środkowa, strona OEM polskiego Windows) dla bajtów 0x80–0xFF,
// w kolejności bajtów: wynik kodeka „cp852” Pythona 3.9, zgodny z tablicą Unicode Consortium
// MAPPINGS/VENDORS/MICSFT/PC/CP852.TXT. Tablica należy do testów: biblioteka nie zawiera stron kodowych poza
// CP437, podaje je wywołujący (Options.CodePage).
const cp852High = "ÇüéâäůćçłëŐőîŹÄĆÉĹĺôöĽľŚśÖÜŤťŁ×čáíóúĄąŽžĘę¬źČş«»░▒▓│┤ÁÂĚŞ╣║╗╝Żż┐└┴┬├─┼Ăă╚╔╩╦╠═╬¤đĐĎËďŇÍÎě┘┌█▄ŢŮ▀ÓßÔŃńňŠšŔÚŕŰýÝţ´­˝˛ˇ˘§÷¸°¨˙űŘř■ "

// cp852SHA256 to SHA-256 tekstu UTF-8 tablicy, policzone z wyniku kodeka Pythona.
const cp852SHA256 = "3ee7c6ae05c3663f3846abd2215d1ae0be97641cc626f71c31f8928ecb9cc613"

func cp852(t testing.TB) *[128]rune {
	if sum := sha256.Sum256([]byte(cp852High)); hex.EncodeToString(sum[:]) != cp852SHA256 {
		t.Fatal("the CP852 table of the tests changed")
	}
	var page [128]rune
	i := 0
	for _, r := range cp852High {
		page[i] = r
		i++
	}
	if i != 128 {
		t.Fatalf("the CP852 table has %d characters", i)
	}
	return &page
}

// TestCodePage: nazwy bez bitu 11 spoza ASCII czytane według tablicy wywołującego; tablica nie może zamienić
// bajtu na znak ASCII ani na wartość, która nie jest znakiem; bez tablicy opcja działa jak ASCIIOnly.
// REQ: NAME-002
func TestCodePage(t *testing.T) {
	archive := func(raw string) []byte {
		a := testkit.Zip(testkit.File{Name: "plain.txt"}, testkit.File{Name: "x"})
		a.Central[1].SetName([]byte(raw))
		a.Central[1].Flags &^= format.FlagUTF8
		a.FixDirectory()
		return a.Bytes()
	}
	page := cp852(t)
	// „zażółć gęślą jaźń” w CP852.
	data := archive("za\xbe\xa2\x88\x86 g\xa9\x98l\xa5 ja\xab\xe4")
	wantCode(t, "strict", data, Options{}, CodeNameEncoding)
	wantCode(t, "no table", data, Options{UnflaggedNames: CodePage}, CodeNameEncoding)
	wantCode(t, "as UTF-8", data, Options{UnflaggedNames: UTF8}, CodeNameEncoding)
	r, _ := wantCode(t, "CP852", data, Options{UnflaggedNames: CodePage, CodePage: page}, "")
	if r != nil && (r.Entries()[1].Name != "zażółć gęślą jaźń" || len(r.Relaxed()) != 1 || r.Relaxed()[0] != (Relaxation{"UnflaggedNames", 1})) {
		t.Errorf("CP852: name %q, relaxed %v", r.Entries()[1].Name, r.Relaxed())
	}
	// Ta sama nazwa jako CP437 też się czyta, ale daje inne znaki: dlatego wybór należy do wywołującego.
	r, _ = wantCode(t, "CP437", data, Options{UnflaggedNames: CP437}, "")
	if r != nil && r.Entries()[1].Name == "zażółć gęślą jaźń" {
		t.Error("CP437 gives the CP852 reading")
	}
	// Tablica, która zamienia bajt na ukośnik, znak sterujący, zero albo wartość spoza Unicode.
	for _, bad := range []rune{'/', '.', 0, 0x1F, 0xD800, 0x110000, -1} {
		p := *page
		p[0xbe-0x80] = bad
		wantCode(t, "bad table", data, Options{UnflaggedNames: CodePage, CodePage: &p}, CodeNameEncoding)
	}
	// Tablica bez opcji nic nie zmienia; opcja nie zostawia śladu dla nazw w ASCII i z bitem 11.
	wantCode(t, "table without the option", data, Options{CodePage: page}, CodeNameEncoding)
	r, _ = wantCode(t, "flagged names", testkit.Zip(files("a", "zażółć")...).Bytes(), Options{UnflaggedNames: CodePage, CodePage: page}, "")
	if r != nil && len(r.Relaxed()) != 0 {
		t.Errorf("relaxed %v for an archive that needs no relaxation", r.Relaxed())
	}
	// Reguły nazw obowiązują po zdekodowaniu, a Unicode Path musi dać tę samą nazwę.
	wantCode(t, "traversal", archive("\xa5/../x"), Options{UnflaggedNames: CodePage, CodePage: page}, CodeNameTraversal)
}

// TestSameFolderTwoWriters: ten sam folder spakowany na polskim Windows 11 Eksploratorem (nazwy w UTF-8 z bitem
// 11) i 7-Zipem (nazwy w CP852 bez bitu 11). Archiwum 7-Zipa czytane z tablicą CP852 ma dać te same nazwy,
// rozmiary i CRC-32 co archiwum Eksploratora, czytane ściśle: to niezależne sprawdzenie dekodowania, bo nazwy
// wzorcowe nie przechodzą przez tablicę. Kolejność wpisów jest w obu programach inna, więc porównujemy zbiory.
// REQ: NAME-002
func TestSameFolderTwoWriters(t *testing.T) {
	list := func(file string, opts Options) []string {
		data, err := os.ReadFile(filepath.Join("..", "testdata", "interop", file))
		if err != nil {
			t.Fatal(err)
		}
		r, err := New(bytes.NewReader(data), int64(len(data)), opts)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var out []string
		for _, e := range r.Entries() {
			sum := sha256.Sum256([]byte(e.Name))
			out = append(out, e.Name+"|"+hex.EncodeToString(sum[:4])+"|"+hex.EncodeToString(testkit.U64(e.UncompressedSize))+"|"+hex.EncodeToString(testkit.U32(e.CRC32)))
		}
		sort.Strings(out)
		return out
	}
	explorer := list("windows11-explorer.zip", Options{})
	sevenZip := list("windows11-7zip.zip", Options{UnflaggedNames: CodePage, CodePage: cp852(t)})
	if len(explorer) != len(sevenZip) {
		t.Fatalf("%d and %d entries", len(explorer), len(sevenZip))
	}
	for i := range explorer {
		if explorer[i] != sevenZip[i] {
			t.Errorf("entry %d: Explorer %q, 7-Zip %q", i, explorer[i], sevenZip[i])
		}
	}
}
