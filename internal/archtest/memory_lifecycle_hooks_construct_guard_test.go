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

// TestNoBareMemoryLifecycleHooksInTests forbids bare `&MemoryLifecycleHooks{...}`
// or `MemoryLifecycleHooks{...}` literals in *_test.go files under
// internal/module/memory/. Tests must construct via newTestHooks(...) from
// helpers_test.go (which is the sole whitelisted file).
//
// Coverage scope: this guard scans white-box tests inside package memory
// (the *_test.go files in internal/module/memory/). Black-box tests in
// package memory_test or any cross-package _test.go cannot bare-construct
// MemoryLifecycleHooks anyway because all of its struct fields are
// unexported — a Go compiler error blocks them at build time. So this
// AST-level scan is the residual line of defense for same-package tests,
// where unexported fields are reachable.
//
// Why this guard exists:
//   - service.go memoryCoordinator() used to do double-checked locking with
//     a no-sync read of h.locks, racing with the Once.Do write path.
//   - Production was unaffected because module.go's factory pre-sets locks,
//     so the lazy-init branch was dead in production.
//   - But test fixtures bare-constructed &MemoryLifecycleHooks{} with locks
//     left as nil, which exercised the racy lazy-init branch under -race.
//   - The L3 fix retired memoryCoordinator() to a pure getter and made
//     locks a required-by-constructor field. To keep the invariant from
//     being silently re-violated by a future test that types
//     `&MemoryLifecycleHooks{logger: l}` again, this guard locks the
//     construction surface to the helpers_test.go factory.
//
// If this test fails with "bare MemoryLifecycleHooks literal forbidden",
// rewrite the test to call newTestHooks(...) with the appropriate
// withXxx options. Add a new option in helpers_test.go if needed.
func TestNoBareMemoryLifecycleHooksInTests(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	memoryDir := filepath.Join(root, "internal", "module", "memory")
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		t.Fatalf("read %s: %v", memoryDir, err)
	}
	var violations []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "helpers_test.go" {
			continue
		}
		path := filepath.Join(memoryDir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if !isMemoryLifecycleHooksType(lit.Type) {
				return true
			}
			p := fset.Position(lit.Pos())
			violations = append(violations,
				fmt.Sprintf("internal/module/memory/%s:%d: bare MemoryLifecycleHooks literal forbidden — use newTestHooks(...)",
					name, p.Line))
			return true
		})
	}
	if len(violations) > 0 {
		t.Fatalf("memory lifecycle hooks bare construction in tests:\n%s\n\nFix: replace `&MemoryLifecycleHooks{...}` with `newTestHooks(withXxx(...), ...)` from helpers_test.go.",
			strings.Join(violations, "\n"))
	}
}

// isMemoryLifecycleHooksType reports whether a composite-literal type
// expression refers to MemoryLifecycleHooks, in either of the forms a
// caller in *_test.go could write:
//   - same-package bare type: ast.Ident{Name: "MemoryLifecycleHooks"}
//   - selector form: ast.SelectorExpr{X: ast.Ident{...}, Sel: ast.Ident{Name: "MemoryLifecycleHooks"}}
//
// The selector form (e.g. `memory.MemoryLifecycleHooks{}`) cannot actually
// compile in cross-package tests because the struct fields are unexported,
// but matching it future-proofs the guard against an alias-import inside
// the same package or any future test that uses a dot-import or shim.
func isMemoryLifecycleHooksType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "MemoryLifecycleHooks"
	case *ast.SelectorExpr:
		return t.Sel != nil && t.Sel.Name == "MemoryLifecycleHooks"
	}
	return false
}
