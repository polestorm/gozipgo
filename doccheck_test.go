package gozipgo

import (
	"go/ast"
	"go/token"
)

// undocumented zwraca eksportowane deklaracje pliku bez komentarza dokumentacyjnego. Stała albo zmienna w grupie
// jest udokumentowana, gdy komentarz ma grupa albo ona sama (także komentarz na końcu wiersza); pole struktury,
// gdy komentarz stoi nad nim, nad grupą pól w tym samym wierszu albo na końcu wiersza.
func undocumented(f *ast.File) []string {
	var out []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() && d.Doc == nil && receiverExported(d) {
				out = append(out, "func "+d.Name.Name)
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.IsExported() && d.Doc == nil && s.Doc == nil && s.Comment == nil {
							out = append(out, n.Name)
						}
					}
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					if d.Doc == nil && s.Doc == nil {
						out = append(out, "type "+s.Name.Name)
					}
					if st, ok := s.Type.(*ast.StructType); ok {
						var lastDoc *ast.CommentGroup
						for _, field := range st.Fields.List {
							if field.Doc != nil {
								lastDoc = field.Doc
							}
							for _, n := range field.Names {
								if n.IsExported() && field.Doc == nil && field.Comment == nil && lastDoc == nil {
									out = append(out, s.Name.Name+"."+n.Name)
								}
							}
						}
					}
				}
			}
		}
	}
	return out
}

func receiverExported(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return true
	}
	t := d.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	id, ok := t.(*ast.Ident)
	return ok && id.IsExported()
}
