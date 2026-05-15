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

// TestIgnoredReturnGuard enforces that critical state machine and event bus
// operations (FireCtx, Fire, Subscribe) must not have their return values ignored.
//
// Source of truth:
// docs/架构/skeleton-stateless.md: "❌ 忽略 FireCtx 返回的 error | error 表示非法转移，必须记录日志"
// docs/契约/statemachine-event-convention.md: "订阅函数返回 context.CancelFunc，调用方必须保存并在生命周期结束时取消"
func TestIgnoredReturnGuard(t *testing.T) {
	root := repoRoot(t)
	// We only care about production code in internal/
	if !dirExists(root, "internal") {
		t.Skip("directory internal not yet created")
	}

	failIfViolations(t, collectIgnoredReturnViolations(t, root, walkGoFiles(t, root, "internal")))
}

func isCriticalFunc(name string) bool {
	return name == "FireCtx" || name == "Fire" || name == "Subscribe"
}

func getCallName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	default:
		return ""
	}
}

func collectIgnoredReturnViolations(t *testing.T, root string, files []string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var violations []string
	for _, absPath := range files {
		if skipIgnoredReturnFile(absPath) {
			continue
		}
		relPath := ignoredReturnRelPath(t, root, absPath)
		violations = append(violations, ignoredReturnFileViolations(t, fset, absPath, relPath)...)
	}
	return violations
}

func skipIgnoredReturnFile(absPath string) bool {
	return strings.HasSuffix(absPath, "_test.go") || strings.Contains(absPath, "vendor")
}

func ignoredReturnRelPath(t *testing.T, root, absPath string) string {
	t.Helper()
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q): %v", root, absPath, err)
	}
	return filepath.ToSlash(relPath)
}

func ignoredReturnFileViolations(t *testing.T, fset *token.FileSet, absPath, relPath string) []string {
	t.Helper()
	fileNode, err := parser.ParseFile(fset, absPath, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", absPath, err)
	}
	var violations []string
	ast.Inspect(fileNode, func(n ast.Node) bool {
		violations = append(violations, ignoredReturnNodeViolations(fset, relPath, n)...)
		return true
	})
	return violations
}

func ignoredReturnNodeViolations(fset *token.FileSet, relPath string, n ast.Node) []string {
	switch node := n.(type) {
	case *ast.ExprStmt:
		return ignoredReturnExprStmtViolations(fset, relPath, node)
	case *ast.AssignStmt:
		return ignoredReturnAssignStmtViolations(fset, relPath, node)
	default:
		return nil
	}
}

func ignoredReturnExprStmtViolations(fset *token.FileSet, relPath string, node *ast.ExprStmt) []string {
	name := criticalCallName(node.X)
	if name == "" {
		return nil
	}
	pos := fset.Position(node.Pos())
	return []string{fmt.Sprintf("%s:%d: return value of %s must not be ignored (bare expression)", relPath, pos.Line, name)}
}

func ignoredReturnAssignStmtViolations(fset *token.FileSet, relPath string, node *ast.AssignStmt) []string {
	if !assignUsesBlankIdentifier(node.Lhs) {
		return nil
	}
	var violations []string
	for _, rhs := range node.Rhs {
		if name := criticalCallName(rhs); name != "" {
			pos := fset.Position(node.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d: return value of %s must not be assigned to _", relPath, pos.Line, name))
		}
	}
	return violations
}

func criticalCallName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	name := getCallName(call)
	if !isCriticalFunc(name) {
		return ""
	}
	return name
}

func assignUsesBlankIdentifier(lhs []ast.Expr) bool {
	for _, expr := range lhs {
		ident, ok := expr.(*ast.Ident)
		if ok && ident.Name == "_" {
			return true
		}
	}
	return false
}
