package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestIDParseIntGuard 检测 strconv.ParseInt / strconv.Atoi 作用于名称含 ID/Id 后缀的变量。
// 雪花 ID 超过 53-bit 时 ParseInt 会截断精度，应保持 string 或直接使用 int64。
// P2: 借鉴自 wjboot-v2 pattern/id-parse-int 规则。
func TestIDParseIntGuard(t *testing.T) {
	repoRoot := findRepoRoot(t)
	violations := scanForIDParseInt(t, repoRoot)
	for _, v := range violations {
		t.Errorf("ID 精度截断风险: %s", v)
	}
}

func scanForIDParseInt(t *testing.T, repoRoot string) []string {
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
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isStrconvParseCall(call) {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			if isIDNamedArg(call.Args[0]) {
				line := fset.Position(call.Pos()).Line
				violations = append(violations, relPath+":"+itoa(line)+
					" — ID 字段禁止用 strconv.ParseInt/Atoi 截断精度，应保持 string 或直接使用 int64")
			}
			return true
		})
	})
	return violations
}

// isStrconvParseCall 判断调用是否为 strconv.ParseInt 或 strconv.Atoi。
func isStrconvParseCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "strconv" {
		return false
	}
	return sel.Sel.Name == "ParseInt" || sel.Sel.Name == "Atoi"
}

// isIDNamedArg 判断表达式是否为名称含 ID/Id 后缀或前缀的标识符。
func isIDNamedArg(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return containsIDSuffix(v.Name)
	case *ast.SelectorExpr:
		return containsIDSuffix(v.Sel.Name)
	}
	return false
}

// containsIDSuffix 判断名称是否含 ID/Id 后缀或前缀。
func containsIDSuffix(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasSuffix(upper, "ID") {
		return true
	}
	if strings.HasPrefix(upper, "ID") && len(name) > 2 {
		return true
	}
	return false
}
