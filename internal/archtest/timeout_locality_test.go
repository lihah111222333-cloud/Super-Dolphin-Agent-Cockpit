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

func TestTimeoutLocality(t *testing.T) {
	root := repoRoot(t)
	violations := collectTimeoutLocalityViolations(t, root)
	failIfViolations(t, violations)
}

func collectTimeoutLocalityViolations(t *testing.T, root string) []string {
	t.Helper()
	var violations []string
	for _, absPath := range walkGoFiles(t, root, "internal", "cmd", "scripts") {
		relPath := timeoutLocalityRelPath(t, root, absPath)
		if allowedTimeoutFile(relPath) {
			continue
		}
		violations = append(violations, timeoutLocalityFileViolations(t, absPath, relPath)...)
	}
	return violations
}

func timeoutLocalityRelPath(t *testing.T, root, absPath string) string {
	t.Helper()
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		t.Fatalf("rel path for %s: %v", absPath, err)
	}
	return filepath.ToSlash(relPath)
}

func timeoutLocalityFileViolations(t *testing.T, absPath, relPath string) []string {
	t.Helper()
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	var violations []string
	ast.Inspect(fileNode, func(n ast.Node) bool {
		if timeoutCall, ok := contextWithTimeoutCall(n); ok {
			violations = append(violations, fmt.Sprintf("%s:%d uses context.WithTimeout outside platform/config/timeouts.go", relPath, fset.Position(timeoutCall.Pos()).Line))
		}
		return true
	})
	return violations
}

func contextWithTimeoutCall(n ast.Node) (*ast.CallExpr, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "WithTimeout" {
		return nil, false
	}
	ident, ok := sel.X.(*ast.Ident)
	return call, ok && ident.Name == "context"
}

func allowedTimeoutFile(relPath string) bool {
	return relPath == "internal/platform/config/timeouts.go" ||
		relPath == "internal/platform/toolbridge/schema/client.go" ||
		relPath == "internal/util/ctxutil/ctxutil.go" ||
		strings.HasPrefix(relPath, "internal/transport/retry/")
}
