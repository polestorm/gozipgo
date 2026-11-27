package zipreader

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/polestorm/gozipgo/internal/entryname"
)

// cp437High to znaki IBM Code Page 437 dla bajtów 0x80–0xFF, w kolejności bajtów. Źródło: tablica Unicode
// Consortium MAPPINGS/VENDORS/MICSFT/PC/CP437.TXT; porównana z wynikiem kodeka „cp437” Pythona 3.9
// (test TestCP437Table trzyma jej sumę kontrolną).
const cp437High = "ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥₧ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀" +
	"αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■ "

// cp437Runes to cp437High jako tablica indeksowana bajtem pomniejszonym o 0x80.
var cp437Runes = func() (t [128]rune) {
	i := 0
	for _, r := range cp437High {
		t[i] = r
		i++
	}
	return t
}()

// checkName sprawdza nazwę wpisu (NAME-001…004) i zwraca ją w UTF-8. Kolejność sprawdzeń jest stała: pusta,
// bajt zerowy, kodowanie, znak sterujący, odwrotny ukośnik, ścieżka bezwzględna, segmenty. relaxed mówi, że nazwa
// bez bitu UTF-8 miała bajty spoza ASCII i została przyjęta na mocy opcji. Przy błędzie code jest niepusty.
func checkName(raw []byte, utf8Flag bool, unflagged NameEncoding, page *[128]rune) (name string, relaxed bool, code, msg string) {
	if len(raw) == 0 {
		return "", false, CodeNameEmpty, "an entry without a name"
	}
	highBytes := false
	for _, c := range raw {
		if c == 0 {
			return "", false, CodeNameNUL, "a zero byte in the name"
		}
		highBytes = highBytes || c >= 0x80
	}
	switch {
	case utf8Flag:
		if !utf8.Valid(raw) {
			return "", false, CodeNameEncoding, "the name has the UTF-8 flag and is not valid UTF-8"
		}
		name = string(raw)
	case !highBytes:
		name = string(raw)
	case unflagged == UTF8:
		if !utf8.Valid(raw) {
			return "", false, CodeNameEncoding, "the name has no UTF-8 flag and is not valid UTF-8"
		}
		name, relaxed = string(raw), true
	case unflagged == CP437 || unflagged == CodePage && page != nil:
		table := &cp437Runes
		if unflagged == CodePage {
			table = page
		}
		var b strings.Builder
		for _, c := range raw {
			if c < 0x80 {
				b.WriteByte(c)
				continue
			}
			// Tablica wywołującego nie może zamienić bajtu na znak ASCII (ukośnik, kropkę) ani na coś, co
			// nie jest znakiem: nazwa miałaby wtedy inne odczytanie niż jej bajty.
			r := table[c-0x80]
			if r < 0x80 || !utf8.ValidRune(r) {
				return "", false, CodeNameEncoding, "the code page has no character for a byte of the name"
			}
			b.WriteRune(r)
		}
		name, relaxed = b.String(), true
	default:
		return "", false, CodeNameEncoding, "the name has bytes outside ASCII and no UTF-8 flag"
	}
	if code, msg = entryname.Check(name); code != "" {
		return "", false, code, msg
	}
	return name, relaxed, "", ""
}

// checkDuplicates wykrywa wpisy, które nie mogą współistnieć w jednym drzewie (NAME-005): dwie takie same nazwy,
// plik „a” i katalog „a/”, plik „a” i dowolny wpis „a/…”. Działa przez sortowanie, nie przez mapę (NAME-009):
// w porządku, w którym „/” jest mniejszy od każdego innego bajtu, wszystko, co leży pod ścieżką, następuje
// bezpośrednio po niej, więc konflikt istnieje wtedy i tylko wtedy, gdy kolidują jacyś sąsiedzi. Z par sąsiadów,
// które kolidują, zgłaszana jest ta o najmniejszym późniejszym indeksie (wynik zależy tylko od treści archiwum);
// błąd wskazuje późniejszy wpis pary (Entry) i wcześniejszy (Other).
func checkDuplicates(entries []Entry, foldCase bool) error {
	rank := func(c byte) int {
		if c == '/' {
			return 0
		}
		if foldCase && c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		return int(c) + 1
	}
	key := func(i int) string { return strings.TrimSuffix(entries[i].Name, "/") }
	compare := func(a, b string) int {
		n := min(len(a), len(b))
		i := 0
		for i < n && a[i] == b[i] { // równe bajty mają równą rangę: wspólny początek bez liczenia rang
			i++
		}
		for ; i < n; i++ {
			if ra, rb := rank(a[i]), rank(b[i]); ra != rb {
				return ra - rb
			}
		}
		return len(a) - len(b)
	}
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(x, y int) bool {
		if c := compare(key(order[x]), key(order[y])); c != 0 {
			return c < 0
		}
		return order[x] < order[y]
	})
	first, second := -1, -1
	for i := 1; i < len(order); i++ {
		p, q := order[i-1], order[i]
		kp, kq := key(p), key(q)
		conflict := compare(kp, kq) == 0
		if !conflict && !entries[p].IsDir && len(kq) > len(kp) && kq[len(kp)] == '/' && compare(kp, kq[:len(kp)]) == 0 {
			conflict = true // p jest plikiem, a q leży pod ścieżką p
		}
		if conflict {
			lo, hi := min(p, q), max(p, q)
			if second < 0 || hi < second || hi == second && lo < first {
				first, second = lo, hi
			}
		}
	}
	if second < 0 {
		return nil
	}
	e := entryError(CodeNameDuplicate, second, entries[second].Name, entries[second].DirectoryOffset,
		"the name conflicts with the name of an earlier entry")
	e.Other = first
	return e
}
