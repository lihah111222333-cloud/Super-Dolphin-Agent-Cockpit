package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrorStringMatchGuard 禁止 strings.Contains(err.Error(), ...) 模式。
// 对 error 类型应使用 errors.Is / errors.As 进行类型安全的错误匹配，
// 而非依赖错误消息字符串（重构时会静默破坏）。
func TestErrorStringMatchGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	scanRoots := []string{"internal", "cmd"}
	skipDirs := DefaultSkipDirs()

	var violations []string
	for _, sr := range scanRoots {
		abs := filepath.Join(root, sr)
		err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if _, skip := skipDirs[info.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			node, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return nil
			}
			count := countErrorStringMatches(node)
			if count > 0 {
				violations = append(violations,
					rel+": 发现 "+itoa(count)+" 处 err.Error() 字符串匹配 — 应使用 errors.Is/errors.As")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Error string match guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// countErrorStringMatches 计算 strings.Contains(xxx.Error(), ...) 调用数量。
func countErrorStringMatches(node *ast.File) int {
	count := 0
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "strings" {
			return true
		}
		if sel.Sel.Name != "Contains" || len(call.Args) < 1 {
			return true
		}
		// 检查第一个参数是否为 xxx.Error() 调用
		if isErrorMethodCall(call.Args[0]) {
			count++
		}
		return true
	})
	return count
}

// isErrorMethodCall 检查表达式是否为 xxx.Error() 方法调用。
func isErrorMethodCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "Error" && len(call.Args) == 0
}
