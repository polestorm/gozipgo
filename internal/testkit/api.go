// Wzorzec publicznego API (TestAPISnapshot). Przeniesione z goxmlgo (internal/testkit/api.go) bez zmian w logice.

package testkit

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// APISnapshot zwraca tekstowy wzorzec publicznego API pakietu z katalogu dir: każda eksportowana deklaracja
// (stałe i zmienne grupami, tak jak w kodzie, bo wartości z iota mają sens tylko w grupie; typy z polami
// eksportowanymi; funkcje i metody bez ciał), bez komentarzy, w stałej kolejności: stałe, zmienne, typy, funkcje,
// metody, w każdej grupie według nazwy. Pliki testów są pomijane. Pierwszy wiersz to „package <importPath>”,
// więc dwa pakiety o tym samym API różnią się tylko nim.
//
// Wzorzec jest tekstem źródłowym, a nie wynikiem sprawdzania typów: zmiana zapisu deklaracji bez zmiany jej
// znaczenia też zmienia wzorzec. To zamierzone, bo każdą zmianę wzorca przegląda człowiek.
func APISnapshot(dir, importPath string) (string, error) {
	fset := token.NewFileSet()
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type entry struct {
		rank int
		name string
		text string
	}
	var entries []entry
	for _, de := range dirEntries { // os.ReadDir zwraca wpisy posortowane według nazwy
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return "", err
		}
		if !ast.FileExports(f) {
			continue
		}
		for _, d := range f.Decls {
			e := entry{}
			switch d := d.(type) {
			case *ast.FuncDecl:
				d.Body, d.Doc = nil, nil
				e.rank, e.name = 3, d.Name.Name
				if d.Recv != nil && len(d.Recv.List) > 0 {
					e.rank, e.name = 4, receiverName(d.Recv.List[0].Type)+"."+d.Name.Name
				}
			case *ast.GenDecl:
				if len(d.Specs) == 0 {
					continue
				}
				d.Doc = nil
				switch d.Tok {
				case token.CONST:
					e.rank = 0
				case token.VAR:
					e.rank = 1
				case token.TYPE:
					e.rank = 2
				default:
					continue // import
				}
				switch s := d.Specs[0].(type) {
				case *ast.ValueSpec:
					e.name = s.Names[0].Name
				case *ast.TypeSpec:
					e.name = s.Name.Name
				}
			default:
				continue
			}
			var b bytes.Buffer
			cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
			if err := cfg.Fprint(&b, fset, d); err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			e.text = b.String()
			entries = append(entries, e)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].rank != entries[j].rank {
			return entries[i].rank < entries[j].rank
		}
		return entries[i].name < entries[j].name
	})
	var out strings.Builder
	out.WriteString("package " + importPath + "\n")
	for _, e := range entries {
		out.WriteString("\n" + e.text + "\n")
	}
	return out.String(), nil
}

func receiverName(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.StarExpr:
		return receiverName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return receiverName(x.X)
	}
	return ""
}
