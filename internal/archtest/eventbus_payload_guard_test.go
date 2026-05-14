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

func TestEventbusPayloadGuard(t *testing.T) {
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
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			if !strings.HasSuffix(ts.Name.Name, "Event") {
				return true
			}
			// Exception: RawProviderEvent inherently wraps 'any' from external drivers.
			if ts.Name.Name == "RawProviderEvent" {
				return true
			}

			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}

			for _, field := range st.Fields.List {
				typeStr := ""
				switch ft := field.Type.(type) {
				case *ast.MapType:
					k, ok1 := ft.Key.(*ast.Ident)
					v, ok2 := ft.Value.(*ast.Ident)
					if ok1 && ok2 && k.Name == "string" && v.Name == "any" {
						typeStr = "map[string]any"
					}
				case *ast.Ident:
					if ft.Name == "any" {
						typeStr = "any"
					}
				case *ast.InterfaceType:
					if len(ft.Methods.List) == 0 {
						typeStr = "interface{}"
					}
				}
				if typeStr != "" {
					violations = append(violations, fmt.Sprintf("%s:%d struct %s has forbidden field type %s", filepath.ToSlash(relPath), fset.Position(field.Pos()).Line, ts.Name.Name, typeStr))
				}
			}
			return true
		})
	}

	failIfViolations(t, violations)
}
