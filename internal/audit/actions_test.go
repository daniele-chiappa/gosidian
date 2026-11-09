package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestActions_EveryConstantListed keeps Actions whole: the web UI names
// each action from it (IMP-220, F-25), so a constant left out would show
// as its code.
func TestActions_EveryConstantListed(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "audit.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var consts []Action
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			v := s.(*ast.ValueSpec)
			if id, ok := v.Type.(*ast.Ident); !ok || id.Name != "Action" {
				continue
			}
			for _, val := range v.Values {
				lit, ok := val.(*ast.BasicLit)
				if !ok {
					t.Fatalf("an Action constant is not a string literal: %T", val)
				}
				s, _ := strconv.Unquote(lit.Value)
				consts = append(consts, Action(s))
			}
		}
	}
	for _, c := range consts {
		if !slices.Contains(Actions, c) {
			t.Errorf("Action %q is not in Actions", c)
		}
	}
	for _, a := range Actions {
		if !slices.Contains(consts, a) {
			t.Errorf("Actions lists %q, which no constant declares", a)
		}
	}
	if len(Actions) != len(consts) {
		t.Errorf("Actions has %d entries, the constants are %d: one listed twice?", len(Actions), len(consts))
	}
}

// TestActions_NoLiteral wants every action written by its constant: an
// Entry{Action: "…"} or auditAccess(…, "…", …) outside this package is an
// action Actions does not know.
func TestActions_NoLiteral(t *testing.T) {
	root := filepath.Join("..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CompositeLit:
				if !isEntry(n.Type) {
					return true
				}
				for _, e := range n.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Action" {
						if lit, ok := kv.Value.(*ast.BasicLit); ok {
							t.Errorf("%s: action %s written as a literal", fset.Position(lit.Pos()), lit.Value)
						}
					}
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "auditAccess" && len(n.Args) > 1 {
					if lit, ok := n.Args[1].(*ast.BasicLit); ok {
						t.Errorf("%s: action %s written as a literal", fset.Position(lit.Pos()), lit.Value)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isEntry(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name == "Entry"
	case *ast.SelectorExpr:
		return e.Sel.Name == "Entry"
	}
	return false
}
