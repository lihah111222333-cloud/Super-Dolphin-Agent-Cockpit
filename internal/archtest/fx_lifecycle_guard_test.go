package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestFxLifecycleGuard(t *testing.T) {
	root := repoRoot(t)
	var violations []string

	for _, absPath := range walkGoFiles(t, root, "internal", "cmd") {
		relPath, _ := filepath.Rel(root, absPath)
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		fileNode, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}

		ast.Inspect(fileNode, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := cl.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Hook" {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); !ok || ident.Name != "fx" {
				return true
			}

			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				keyIdent, ok := kv.Key.(*ast.Ident)
				if !ok || keyIdent.Name != "OnStart" {
					continue
				}

				fnLit, ok := kv.Value.(*ast.FuncLit)
				if !ok {
					continue
				}

				ast.Inspect(fnLit.Body, func(bodyNode ast.Node) bool {
					switch stmt := bodyNode.(type) {
					case *ast.ForStmt:
						if stmt.Cond == nil {
							violations = append(violations, fmt.Sprintf("%s:%d has bare for{} in fx.OnStart", filepath.ToSlash(relPath), fset.Position(stmt.Pos()).Line))
						}
					case *ast.SelectStmt:
						if len(stmt.Body.List) == 0 {
							violations = append(violations, fmt.Sprintf("%s:%d has bare select{} in fx.OnStart", filepath.ToSlash(relPath), fset.Position(stmt.Pos()).Line))
						}
					}
					return true
				})
			}
			return true
		})
	}

	failIfViolations(t, violations)
}
