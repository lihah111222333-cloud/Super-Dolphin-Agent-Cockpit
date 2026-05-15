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
		violations = append(violations, fxLifecycleViolationsInFile(root, absPath)...)
	}

	failIfViolations(t, violations)
}

func fxLifecycleViolationsInFile(root, absPath string) []string {
	relPath, _ := filepath.Rel(root, absPath)
	if strings.HasSuffix(relPath, "_test.go") {
		return nil
	}
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}

	var violations []string
	ast.Inspect(fileNode, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok || !isFxHookCompositeLit(cl) {
			return true
		}
		violations = append(violations, fxOnStartBlockingLoopViolations(relPath, fset, cl)...)
		return true
	})
	return violations
}

func isFxHookCompositeLit(cl *ast.CompositeLit) bool {
	sel, ok := cl.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Hook" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "fx"
}

func fxOnStartBlockingLoopViolations(relPath string, fset *token.FileSet, cl *ast.CompositeLit) []string {
	var violations []string
	for _, elt := range cl.Elts {
		fnLit, ok := fxOnStartFuncLit(elt)
		if !ok {
			continue
		}
		violations = append(violations, blockingLoopViolations(relPath, fset, fnLit)...)
	}
	return violations
}

func fxOnStartFuncLit(elt ast.Expr) (*ast.FuncLit, bool) {
	kv, ok := elt.(*ast.KeyValueExpr)
	if !ok {
		return nil, false
	}
	keyIdent, ok := kv.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "OnStart" {
		return nil, false
	}
	fnLit, ok := kv.Value.(*ast.FuncLit)
	return fnLit, ok
}

func blockingLoopViolations(relPath string, fset *token.FileSet, fnLit *ast.FuncLit) []string {
	var violations []string
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
	return violations
}
