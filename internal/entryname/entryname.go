// Package entryname trzyma reguły nazw wpisów wspólne dla czytnika i pisarza (NAME-002…004): to samo, co czytnik
// odrzuca, pisarz odmawia zapisać. Kody są tymi samymi napisami co stałe Code* obu pakietów publicznych.
package entryname

import "strings"

// Kody błędów nazw; pakiety publiczne eksportują je pod własnymi stałymi.
const (
	Control   = "name-control-character"
	Backslash = "name-backslash"
	Absolute  = "name-absolute"
	Traversal = "name-traversal"
)

// Check sprawdza niepustą nazwę w UTF-8 bez bajtu zerowego. Kolejność sprawdzeń jest stała: znak sterujący,
// odwrotny ukośnik, ścieżka bezwzględna, segmenty. Przy błędzie code jest niepusty.
func Check(name string) (code, message string) {
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c == 0x7F {
			return Control, "a control character in the name"
		}
	}
	if strings.IndexByte(name, '\\') >= 0 {
		return Backslash, "a backslash in the name"
	}
	if name[0] == '/' {
		return Absolute, "the name starts with a slash"
	}
	if len(name) >= 2 && name[1] == ':' && (name[0]|0x20 >= 'a' && name[0]|0x20 <= 'z') {
		return Absolute, "the name starts with a drive letter"
	}
	// Segmenty: jeden ukośnik na końcu oznacza katalog; poza tym żaden segment nie jest pusty, „.” ani „..”.
	path := strings.TrimSuffix(name, "/")
	for start := 0; start <= len(path); {
		end := strings.IndexByte(path[start:], '/')
		if end < 0 {
			end = len(path)
		} else {
			end += start
		}
		switch path[start:end] {
		case "", ".", "..":
			return Traversal, "an empty, \".\" or \"..\" path segment in the name"
		}
		start = end + 1
	}
	return "", ""
}
