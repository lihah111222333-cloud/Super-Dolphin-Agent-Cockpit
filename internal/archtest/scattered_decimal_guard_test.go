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

func TestScatteredDecimalGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	scanRoots := []string{"cmd", "internal", "pkg"}
	skipDirs := DefaultSkipDirs()

	var violations []string

	for _, sr := range scanRoots {
		if err := collectScatteredDecimalViolations(root, sr, skipDirs, &violations); err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Scattered Decimal violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}

func collectScatteredDecimalViolations(root, scanRoot string, skipDirs map[string]bool, violations *[]string) error {
	abs := filepath.Join(root, scanRoot)
	return filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if shouldSkipScatteredDecimalWalkEntry(info, skipDirs) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileViolations, err := scatteredDecimalViolationsForFile(root, path)
		if err != nil {
			return err
		}
		*violations = append(*violations, fileViolations...)
		return nil
	})
}

func shouldSkipScatteredDecimalWalkEntry(info os.FileInfo, skipDirs map[string]bool) bool {
	if !info.IsDir() {
		return false
	}
	return skipDirs[info.Name()]
}

func scatteredDecimalViolationsForFile(root, path string) ([]string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	fset := token.NewFileSet()
	fileNode, parseErr := parser.ParseFile(fset, path, nil, 0)
	if parseErr != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, parseErr)
	}
	var violations []string
	for _, decl := range fileNode.Decls {
		violations = append(violations, scatteredDecimalViolationsForDecl(fset, rel, decl)...)
	}
	return violations, nil
}

func scatteredDecimalViolationsForDecl(fset *token.FileSet, rel string, decl ast.Decl) []string {
	gd, ok := decl.(*ast.GenDecl)
	if !ok || gd.Tok != token.VAR {
		return nil
	}
	var violations []string
	for _, spec := range gd.Specs {
		vspec, ok := spec.(*ast.ValueSpec)
		if !ok || !isDecimalValueSpec(vspec) {
			continue
		}
		violations = append(violations, scatteredDecimalViolationsForNames(fset, rel, vspec.Names)...)
	}
	return violations
}

func scatteredDecimalViolationsForNames(fset *token.FileSet, rel string, names []*ast.Ident) []string {
	var violations []string
	for _, name := range names {
		if name.Name != "_" {
			violations = append(violations, fmt.Sprintf("%s:%d: 禁止散乱的全局 Decimal 变量 %q，请合并到单一 struct 容器或彻底消除状态", rel, fset.Position(name.Pos()).Line, name.Name))
		}
	}
	return violations
}

func isDecimalValueSpec(vspec *ast.ValueSpec) bool {
	if isDecimalSelector(vspec.Type, "Decimal") {
		return true
	}
	for _, val := range vspec.Values {
		if isDecimalInitializer(val) {
			return true
		}
	}
	return false
}

func isDecimalInitializer(expr ast.Expr) bool {
	if call, ok := expr.(*ast.CallExpr); ok {
		return isDecimalSelector(call.Fun, "")
	}
	return isDecimalSelector(expr, "")
}

func isDecimalSelector(expr ast.Expr, selectorName string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "decimal" {
		return false
	}
	return selectorName == "" || sel.Sel.Name == selectorName
}
