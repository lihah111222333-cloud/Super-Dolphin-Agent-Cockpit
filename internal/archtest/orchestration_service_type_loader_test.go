package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

type orchestrationServiceCheckedPackage struct {
	pkgPath   string
	fset      *token.FileSet
	syntax    []*ast.File
	types     *types.Package
	typesInfo *types.Info
}

func isOrchestrationServiceTypeGuardProductionPackagePath(pkgPath string) bool {
	if pkgPath == "" {
		return false
	}
	if strings.HasPrefix(pkgPath, superAgentModulePath+"/internal/archtest") {
		return false
	}
	return strings.HasPrefix(pkgPath, superAgentModulePath+"/cmd/") ||
		strings.HasPrefix(pkgPath, superAgentModulePath+"/internal/")
}

func newOrchestrationServiceTypesInfo() *types.Info {
	return &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
}

func parseOrchestrationServiceTypeGuardFixture(t *testing.T, files map[string]string) (*token.FileSet, []*ast.File) {
	t.Helper()

	fset := token.NewFileSet()
	relPaths := make([]string, 0, len(files))
	for relPath := range files {
		relPaths = append(relPaths, relPath)
	}
	sort.Strings(relPaths)

	syntax := make([]*ast.File, 0, len(relPaths))
	for _, relPath := range relPaths {
		file, err := parser.ParseFile(fset, relPath, files[relPath], parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse fixture %s: %v", relPath, err)
		}
		syntax = append(syntax, file)
	}
	return fset, syntax
}
