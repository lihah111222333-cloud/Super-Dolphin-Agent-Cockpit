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

	files := walkGoFiles(t, root, "internal")

	fset := token.NewFileSet()
	var violations []string

	for _, absPath := range files {
		// skip tests and vendor
		if strings.HasSuffix(absPath, "_test.go") || strings.Contains(absPath, "vendor") {
			continue
		}

		fileNode, err := parser.ParseFile(fset, absPath, nil, 0)
		if err != nil {
			t.Fatalf("failed to parse %s: %v", absPath, err)
		}

		relPath, _ := filepath.Rel(root, absPath)
		relPath = filepath.ToSlash(relPath)

		ast.Inspect(fileNode, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.ExprStmt:
				// Example: sm.FireCtx(...)
				// This means the return value is completely ignored.
				if call, ok := node.X.(*ast.CallExpr); ok {
					if name := getCallName(call); isCriticalFunc(name) {
						pos := fset.Position(node.Pos())
						violations = append(violations, fmt.Sprintf("%s:%d: return value of %s must not be ignored (bare expression)", relPath, pos.Line, name))
					}
				}
			case *ast.AssignStmt:
				// Example: _ = bus.Subscribe(...)
				for _, rhs := range node.Rhs {
					if call, ok := rhs.(*ast.CallExpr); ok {
						if name := getCallName(call); isCriticalFunc(name) {
							// Check if LHS ignores the result (using blank identifier)
							for _, lhs := range node.Lhs {
								if ident, ok := lhs.(*ast.Ident); ok && ident.Name == "_" {
									pos := fset.Position(node.Pos())
									violations = append(violations, fmt.Sprintf("%s:%d: return value of %s must not be assigned to _", relPath, pos.Line, name))
									break
								}
							}
						}
					}
				}
			}
			return true
		})
	}

	failIfViolations(t, violations)
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
