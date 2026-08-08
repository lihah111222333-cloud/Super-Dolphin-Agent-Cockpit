package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
)

func collectScatteredDecimalViolationsFromSnapshot(snapshot *productionSourceSnapshot, scanRoots []string) []string {
	var violations []string
	for _, file := range snapshot.files {
		if !productionSourcePathInRoots(file.relPath, scanRoots) {
			continue
		}
		for _, decl := range file.syntax.Decls {
			violations = append(violations, scatteredDecimalViolationsForDecl(file.fset, file.relPath, decl)...)
		}
	}
	return violations
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
	return slices.ContainsFunc(vspec.Values, isDecimalInitializer)
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
