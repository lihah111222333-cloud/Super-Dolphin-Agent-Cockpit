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

// TestSharedfileGitignoreNoPackageLevelErrorVar forbids package-level
// mutable `error`-typed variables in internal/platform/sharedfilegitignore/.
//
// Why this guard exists:
//   - Ensure(cwd, logger) is a per-cwd memoization helper. The previous
//     design stored the captured error in a package-level `ensureErr`
//     variable. Each cwd's sync.Once.Do() wrote it; every Ensure() caller
//     read it outside any synchronization.
//   - That produced two bugs at once: (a) a data race between writers in
//     different cwd's Once.Do bodies and concurrent readers; (b) a
//     semantic bug where cwd1's failure leaked into cwd2's return value
//     because both cwds shared the same global variable.
//   - The fix moves err into a per-cwd ensureState struct (stored in the
//     sync.Map / map keyed by cwd) so each cwd's success/failure signal
//     is isolated and synchronized by its own sync.Once.
//
// This guard prevents the regression by failing whenever a future change
// re-introduces a package-level mutable error variable in this package.
//
// Allowed: sentinel errors declared with `var ErrPackageRequired = errors.New(...)`
// (exported, value computed at init, never reassigned) — those are
// idiomatic Go. The guard requires exported names beginning with "Err"
// to opt out; everything else flagged.
func TestSharedfileGitignoreNoPackageLevelErrorVar(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	pkgDir := filepath.Join(root, "internal", "platform", "sharedfilegitignore")

	violations := sharedfileGitignoreErrorVarViolations(t, pkgDir)
	if len(violations) > 0 {
		t.Fatalf("sharedfilegitignore package-level error variables:\n%s\n\nFix: move the error into a per-cwd state struct (see ensureState in gitignore.go) so each cwd's failure signal stays isolated and synchronized by its own sync.Once.",
			strings.Join(violations, "\n"))
	}
}

func sharedfileGitignoreErrorVarViolations(t *testing.T, pkgDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read %s: %v", pkgDir, err)
	}
	var violations []string
	for _, entry := range entries {
		name, ok := sharedfileGitignoreSourceName(entry)
		if !ok {
			continue
		}
		violations = append(violations, sharedfileGitignoreErrorVarViolationsInFile(t, pkgDir, name)...)
	}
	return violations
}

func sharedfileGitignoreSourceName(entry os.DirEntry) (string, bool) {
	if entry.IsDir() {
		return "", false
	}
	name := entry.Name()
	return name, strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func sharedfileGitignoreErrorVarViolationsInFile(t *testing.T, pkgDir, name string) []string {
	t.Helper()
	path := filepath.Join(pkgDir, name)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var violations []string
	for _, decl := range file.Decls {
		violations = append(violations, errorVarViolationsInDecl(fset, name, decl)...)
	}
	return violations
}

func errorVarViolationsInDecl(fset *token.FileSet, filename string, decl ast.Decl) []string {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil
	}
	var violations []string
	for _, spec := range gen.Specs {
		violations = append(violations, errorVarViolationsInSpec(fset, filename, spec)...)
	}
	return violations
}

func errorVarViolationsInSpec(fset *token.FileSet, filename string, spec ast.Spec) []string {
	vs, ok := spec.(*ast.ValueSpec)
	if !ok || !isErrorTypeExpr(vs.Type) {
		return nil
	}
	var violations []string
	for _, ident := range vs.Names {
		if isExportedSentinelName(ident.Name) {
			continue
		}
		p := fset.Position(ident.Pos())
		violations = append(violations,
			fmt.Sprintf("internal/platform/sharedfilegitignore/%s:%d: package-level mutable error variable %q forbidden — store err inside per-cwd ensureState struct",
				filename, p.Line, ident.Name))
	}
	return violations
}

// isErrorTypeExpr returns true when the type expression refers to the
// builtin `error` type (the only form we need to catch in this package).
// Pointer / slice / map / channel of error are not idiomatic for sentinel
// or memoization storage and are also flagged via the underlying ident.
func isErrorTypeExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name == "error"
	}
	return false
}

// isExportedSentinelName allows `var ErrFoo = errors.New(...)` style
// idiomatic sentinels to coexist with the guard. Exported names starting
// with "Err" are by Go convention immutable sentinels; non-exported or
// non-Err-prefixed error vars are exactly the regression pattern we want
// to block.
func isExportedSentinelName(name string) bool {
	if !strings.HasPrefix(name, "Err") {
		return false
	}
	r := name[3:]
	if r == "" {
		return false
	}
	first := r[0]
	return first >= 'A' && first <= 'Z'
}
