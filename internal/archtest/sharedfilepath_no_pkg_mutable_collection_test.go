package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedfilePathNoPackageLevelMutableCollection(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	pkgDir := filepath.Join(root, "internal", "platform", "sharedfilepath")
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read %s: %v", pkgDir, err)
	}
	var violations []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		violations = append(violations, sharedfilePathMutableCollectionViolations(t, pkgDir, entry.Name())...)
	}
	if len(violations) > 0 {
		t.Fatalf("sharedfilepath package-level mutable collections:\n%s\n\nFix: create a fresh collection in a private function.", strings.Join(violations, "\n"))
	}
}

func sharedfilePathMutableCollectionViolations(t *testing.T, pkgDir, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(pkgDir, name), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var violations []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || !isMutableCollectionValue(vs) {
				continue
			}
			for _, ident := range vs.Names {
				p := fset.Position(ident.Pos())
				violations = append(violations, fmt.Sprintf("internal/platform/sharedfilepath/%s:%d: package-level mutable collection %q forbidden", name, p.Line, ident.Name))
			}
		}
	}
	return violations
}

func isMutableCollectionValue(spec *ast.ValueSpec) bool {
	if isMutableCollectionType(spec.Type) {
		return true
	}
	for _, value := range spec.Values {
		if literal, ok := value.(*ast.CompositeLit); ok && isMutableCollectionType(literal.Type) {
			return true
		}
		if call, ok := value.(*ast.CallExpr); ok && isMakeMutableCollectionCall(call) {
			return true
		}
	}
	return false
}

func isMakeMutableCollectionCall(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "make" && len(call.Args) > 0 && isMutableCollectionType(call.Args[0])
}

func isMutableCollectionType(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.ArrayType, *ast.MapType:
		return true
	default:
		return false
	}
}
