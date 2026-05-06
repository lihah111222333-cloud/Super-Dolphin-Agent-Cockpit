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
	var violations []string
	for _, absPath := range walkGoFiles(t, root, "internal", "cmd", "scripts") {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		if allowedTimeoutFile(relPath) {
			continue
		}
		fset := token.NewFileSet()
		fileNode, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", relPath, err)
		}
		ast.Inspect(fileNode, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "WithTimeout" {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if ok && ident.Name == "context" {
				violations = append(violations, fmt.Sprintf("%s:%d uses context.WithTimeout outside platform/config/timeouts.go", relPath, fset.Position(call.Pos()).Line))
			}
			return true
		})
	}
	failIfViolations(t, violations)
}

func allowedTimeoutFile(relPath string) bool {
	return relPath == "internal/platform/config/timeouts.go" ||
		relPath == "internal/util/ctxutil/ctxutil.go" ||
		strings.HasPrefix(relPath, "internal/transport/retry/")
}
