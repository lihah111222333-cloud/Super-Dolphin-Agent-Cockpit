package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestDuplicateSwitchKeysGuard 检测同文件内两个 switch 共享 ≥ 阈值个相同 case 字符串字面量。
// 这种模式表明两个 switch 在维护同一份键列表的副本，应提取为共享 map 或 const。
// P3: 借鉴自 wjboot-v2 pattern/duplicate-switch-keys 规则。
func TestDuplicateSwitchKeysGuard(t *testing.T) {
	// 已知冻结：这些文件的 switch 重复是有意设计（如状态归一化 + 显示文本映射各用一份 switch）。
	// 修复后删除对应条目。
	frozen := map[string]bool{
		"internal/module/uistate/sidebar_compat.go": true,
	}

	repoRoot := findRepoRoot(t)
	violations := scanForDuplicateSwitchKeys(t, repoRoot)
	var unfrozen []string
	for _, v := range violations {
		file, _, _ := strings.Cut(v, ":")
		if frozen[file] {
			continue
		}
		unfrozen = append(unfrozen, v)
	}
	for _, v := range unfrozen {
		t.Errorf("重复 switch 键: %s", v)
	}
}

const duplicateSwitchKeysThreshold = 8

func scanForDuplicateSwitchKeys(t *testing.T, repoRoot string) []string {
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

		type switchInfo struct {
			pos  token.Pos
			keys map[string]bool
		}
		var switches []switchInfo

		ast.Inspect(node, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			keys := extractCaseStringKeys(sw)
			if len(keys) >= duplicateSwitchKeysThreshold {
				switches = append(switches, switchInfo{pos: sw.Pos(), keys: keys})
			}
			return true
		})

		for i := range switches {
			for j := i + 1; j < len(switches); j++ {
				overlap := countKeyOverlap(switches[i].keys, switches[j].keys)
				if overlap >= duplicateSwitchKeysThreshold {
					line1 := fset.Position(switches[i].pos).Line
					line2 := fset.Position(switches[j].pos).Line
					violations = append(violations,
						fmt.Sprintf("%s:%d+%d — 两个 switch 共享 %d 个相同 case 键，应提取为共享 map 或 const", relPath, line1, line2, overlap))
				}
			}
		}
	})
	return violations
}

// extractCaseStringKeys 从 switch 语句中提取所有 case 子句的字符串字面量。
func extractCaseStringKeys(sw *ast.SwitchStmt) map[string]bool {
	keys := make(map[string]bool)
	if sw.Body == nil {
		return keys
	}
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok || cc.List == nil {
			continue
		}
		for _, expr := range cc.List {
			if text := staticStringValue(expr); text != "" {
				keys[text] = true
			}
		}
	}
	return keys
}

// staticStringValue 尝试从表达式提取静态字符串字面量值。
func staticStringValue(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	// 去除引号
	s := lit.Value
	if len(s) >= 2 && (s[0] == '"' || s[0] == '`') {
		return s[1 : len(s)-1]
	}
	return s
}

// countKeyOverlap 计算两个键集合的交集大小。
func countKeyOverlap(a, b map[string]bool) int {
	count := 0
	for k := range a {
		if b[k] {
			count++
		}
	}
	return count
}
