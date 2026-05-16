package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestDeepMapProbeGuard 检测单函数内过多 map[string]any 类型断言。
// 函数内 ≥ 阈值次 .(map[string]any) 断言表明应提取为强类型 struct。
// P3: 借鉴自 wjboot-v2 pattern/deep-map-probe 规则。
func TestDeepMapProbeGuard(t *testing.T) {
	repoRoot := findRepoRoot(t)
	violations := scanForDeepMapProbe(t, repoRoot)
	for _, v := range violations {
		t.Errorf("深层 map 探测: %s", v)
	}
}

const deepMapProbeThreshold = 4

// deepMapProbeExemptKeywords 名称含这些关键词的函数豁免（解析器/提取器本身合法）。
var deepMapProbeExemptKeywords = []string{"parse", "extract", "convert", "unmarshal", "decode", "merge", "flatten", "transform"}

func scanForDeepMapProbe(t *testing.T, repoRoot string) []string {
	t.Helper()
	var violations []string
	scanGoFiles(t, repoRoot, func(relPath, absPath string) {
		if strings.HasSuffix(relPath, "_test.go") {
			return
		}
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
		if err != nil {
			return
		}

		for _, decl := range node.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			funcName := fn.Name.Name
			if isDeepMapProbeExemptFunc(funcName) {
				continue
			}
			count := countMapStringAnyAssertions(fn.Body)
			if count >= deepMapProbeThreshold {
				line := fset.Position(fn.Pos()).Line
				violations = append(violations,
					fmt.Sprintf("%s:%d — 函数 %s 包含 %d 次 map[string]any 类型断言（上限 %d），应提取为强类型 struct",
						relPath, line, funcName, count, deepMapProbeThreshold))
			}
		}
	})
	return violations
}

// countMapStringAnyAssertions 统计函数体内 .(map[string]any) 或 .(map[string]interface{}) 类型断言的次数。
func countMapStringAnyAssertions(body *ast.BlockStmt) int {
	count := 0
	ast.Inspect(body, func(n ast.Node) bool {
		ta, ok := n.(*ast.TypeAssertExpr)
		if !ok {
			return true
		}
		if isMapStringAnyType(ta.Type) {
			count++
		}
		return true
	})
	return count
}

// isMapStringAnyType 判断类型表达式是否为 map[string]any 或 map[string]interface{}。
func isMapStringAnyType(expr ast.Expr) bool {
	mt, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	keyIdent, ok := mt.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "string" {
		return false
	}
	switch v := mt.Value.(type) {
	case *ast.Ident:
		return v.Name == "any"
	case *ast.InterfaceType:
		return v.Methods == nil || len(v.Methods.List) == 0
	}
	return false
}

// isDeepMapProbeExemptFunc 判断函数名是否含解析器/提取器关键词。
func isDeepMapProbeExemptFunc(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range deepMapProbeExemptKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
