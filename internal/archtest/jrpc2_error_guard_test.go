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

func TestJRPC2ErrorGuard(t *testing.T) {
	root := repoRoot(t)
	var violations []string

	for _, absPath := range walkGoFiles(t, root, "internal/platform/rpc") {
		relPath, _ := filepath.Rel(root, absPath)
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}

		fset := token.NewFileSet()
		fileNode, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}

		ast.Inspect(fileNode, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "fmt" && sel.Sel.Name == "Errorf" {
						violations = append(violations, fmt.Sprintf("%s:%d uses fmt.Errorf instead of jrpc2.Errorf", filepath.ToSlash(relPath), fset.Position(call.Pos()).Line))
					}
				}
			}
			return true
		})
	}

	failIfViolations(t, violations)
}
