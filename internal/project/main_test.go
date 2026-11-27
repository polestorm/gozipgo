// Package project trzyma testy całego repozytorium, a nie jednego pakietu: wzorce API, pokrycie wymagań, katalog
// błędów, importy biblioteki, zapisane wyniki pomiarów. Pakiet ma tylko pliki testów; ścieżki w testach są
// liczone od katalogu głównego repozytorium, do którego TestMain przechodzi.
package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// root to katalog główny repozytorium jako ścieżka bezwzględna.
var root string

func TestMain(m *testing.M) {
	var err error
	if root, err = filepath.Abs("../.."); err != nil {
		panic(err)
	}
	if err := os.Chdir(root); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// TestRepositoryFilesTracked nie sprawdza niczego: istnieje dla pamięci podręcznej `go test`. Ta zapamiętuje
// pliki, które test otworzył, pod nazwą, jaką podał; po zmianie katalogu w TestMain nazwy względne wskazują już
// inne miejsce, więc zmiana pliku repozytorium nie unieważniałaby zapisanego wyniku. Tu każdy plik, który testy
// mogą czytać, jest sprawdzany pod ścieżką bezwzględną i wynik zależy od jego rozmiaru i czasu zmiany.
func TestRepositoryFilesTracked(t *testing.T) {
	n := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			if rel == "out" || rel == "ref" || rel == filepath.Join("testdata", "fetched") ||
				strings.HasPrefix(d.Name(), ".") && rel != "." && d.Name() != ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if _, err := os.Stat(p); err == nil {
			n++
		}
		return nil
	})
	if n < 100 {
		t.Errorf("only %d files of the repository seen", n)
	}
}
