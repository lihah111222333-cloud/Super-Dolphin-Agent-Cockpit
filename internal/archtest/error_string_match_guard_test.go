package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
)

func collectErrorStringMatchViolationsFromSnapshot(snapshot *productionSourceSnapshot, scanRoots []string) []string {
	var violations []string
	for _, file := range snapshot.files {
		if !productionSourcePathInRoots(file.relPath, scanRoots) {
			continue
		}
		count := countErrorStringMatches(file.syntax)
		if count > 0 {
			violations = append(violations,
				file.relPath+": 发现 "+itoa(count)+" 处 err.Error() 字符串匹配 — 应使用 errors.Is/errors.As")
		}
	}
	return violations
}

func countErrorStringMatchesInFile(path string) (int, error) {
	fset := token.NewFileSet()
	node, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if parseErr != nil {
		return 0, parseErr
	}
	return countErrorStringMatches(node), nil
}

// countErrorStringMatches 计算 strings.Contains(errValue.Error(), ...) 调用数量。
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
		// 检查第一个参数是否为 errValue.Error() 调用
		if isErrorMethodCall(call.Args[0]) {
			count++
		}
		return true
	})
	return count
}

// isErrorMethodCall 检查表达式是否为 errValue.Error() 方法调用。
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
