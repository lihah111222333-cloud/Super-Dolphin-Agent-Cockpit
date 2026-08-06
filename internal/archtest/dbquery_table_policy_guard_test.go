package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestDBQueryTablePolicyHasNoPackageSharedMap 保证 dbquery 表策略不回退为包级可变 map。
func TestDBQueryTablePolicyHasNoPackageSharedMap(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRootForGuardTests(t), "internal", "store", "dbquery", "executor.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	violations := dbQueryTablePolicyViolations(file.Decls)
	if len(violations) > 0 {
		t.Fatalf("dbquery table policy must not use package shared maps:\n%s", strings.Join(violations, "\n"))
	}
}

func dbQueryTablePolicyViolations(decls []ast.Decl) []string {
	var violations []string
	hasPolicy := false
	for _, decl := range decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "isAllowedTable" {
			hasPolicy = true
			continue
		}
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if ok && dbQueryTablePolicyHasMap(valueSpec) {
				violations = append(violations, "internal/store/dbquery/executor.go: package-level map is forbidden for table policy")
			}
		}
	}
	if !hasPolicy {
		violations = append(violations, "internal/store/dbquery/executor.go: isAllowedTable policy function is required")
	}
	return violations
}

func dbQueryTablePolicyHasMap(spec *ast.ValueSpec) bool {
	if _, ok := spec.Type.(*ast.MapType); ok {
		return true
	}
	for _, value := range spec.Values {
		if literal, ok := value.(*ast.CompositeLit); ok {
			if _, ok := literal.Type.(*ast.MapType); ok {
				return true
			}
		}
		if call, ok := value.(*ast.CallExpr); ok && len(call.Args) > 0 {
			if makeIdent, ok := call.Fun.(*ast.Ident); ok && makeIdent.Name == "make" {
				if _, ok := call.Args[0].(*ast.MapType); ok {
					return true
				}
			}
		}
	}
	return false
}

func TestDBQueryTablePolicyGuardDetectsSharedMap(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "shared_map.go", `package dbquery
var allowedTables = make(map[string]struct{})
func isAllowedTable(string) bool { return false }
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse shared map fixture: %v", err)
	}
	if violations := dbQueryTablePolicyViolations(file.Decls); len(violations) == 0 {
		t.Fatal("dbqueryTablePolicyViolations() did not reject a package shared map")
	}
}
