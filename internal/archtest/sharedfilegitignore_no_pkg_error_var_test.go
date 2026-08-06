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

// TestSharedfileGitignoreNoPackageLevelRuntimeState keeps Ensure stateless.
func TestSharedfileGitignoreNoPackageLevelRuntimeState(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	pkgDir := filepath.Join(root, "internal", "platform", "sharedfilegitignore")
	violations := sharedfileGitignoreRuntimeStateViolations(t, pkgDir)
	if len(violations) > 0 {
		t.Fatalf("sharedfilegitignore package-level runtime state:\n%s\n\nFix: keep Ensure stateless and rely on the durable .gitignore rule for idempotency.", strings.Join(violations, "\n"))
	}
}

func sharedfileGitignoreRuntimeStateViolations(t *testing.T, pkgDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read %s: %v", pkgDir, err)
	}
	var violations []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		violations = append(violations, sharedfileGitignoreRuntimeStateViolationsInFile(t, pkgDir, entry.Name())...)
	}
	return violations
}

func sharedfileGitignoreRuntimeStateViolationsInFile(t *testing.T, pkgDir, name string) []string {
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
			if !ok {
				continue
			}
			for _, ident := range vs.Names {
				p := fset.Position(ident.Pos())
				violations = append(violations, fmt.Sprintf("internal/platform/sharedfilegitignore/%s:%d: package-level variable %q forbidden", name, p.Line, ident.Name))
			}
		}
	}
	return violations
}
