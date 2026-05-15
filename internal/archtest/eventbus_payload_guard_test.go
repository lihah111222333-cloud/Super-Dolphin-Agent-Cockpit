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
	failIfViolations(t, eventbusPayloadViolations(t, root))
}

func eventbusPayloadViolations(t *testing.T, root string) []string {
	t.Helper()
	var violations []string
	for _, absPath := range walkGoFiles(t, root, "internal", "cmd") {
		relPath, _ := filepath.Rel(root, absPath)
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}
		violations = append(violations, eventbusPayloadViolationsInFile(absPath, relPath)...)
	}
	return violations
}

func eventbusPayloadViolationsInFile(absPath, relPath string) []string {
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var violations []string
	ast.Inspect(fileNode, func(n ast.Node) bool {
		ts, st, ok := eventStructForPayloadGuard(n)
		if !ok {
			return true
		}
		violations = append(violations, forbiddenEventFields(fset, relPath, ts.Name.Name, st)...)
		return true
	})
	return violations
}

func eventStructForPayloadGuard(n ast.Node) (*ast.TypeSpec, *ast.StructType, bool) {
	ts, ok := n.(*ast.TypeSpec)
	if !ok {
		return nil, nil, false
	}
	if !strings.HasSuffix(ts.Name.Name, "Event") {
		return nil, nil, false
	}
	// Exception: RawProviderEvent inherently wraps 'any' from external drivers.
	if ts.Name.Name == "RawProviderEvent" {
		return nil, nil, false
	}
	st, ok := ts.Type.(*ast.StructType)
	return ts, st, ok
}

func forbiddenEventFields(fset *token.FileSet, relPath, typeName string, st *ast.StructType) []string {
	var violations []string
	for _, field := range st.Fields.List {
		typeStr := forbiddenEventFieldType(field.Type)
		if typeStr != "" {
			violations = append(violations, fmt.Sprintf("%s:%d struct %s has forbidden field type %s", filepath.ToSlash(relPath), fset.Position(field.Pos()).Line, typeName, typeStr))
		}
	}
	return violations
}

func forbiddenEventFieldType(expr ast.Expr) string {
	switch ft := expr.(type) {
	case *ast.MapType:
		k, ok1 := ft.Key.(*ast.Ident)
		v, ok2 := ft.Value.(*ast.Ident)
		if ok1 && ok2 && k.Name == "string" && v.Name == "any" {
			return "map[string]any"
		}
	case *ast.Ident:
		if ft.Name == "any" {
			return "any"
		}
	case *ast.InterfaceType:
		if len(ft.Methods.List) == 0 {
			return "interface{}"
		}
	}
	return ""
}
