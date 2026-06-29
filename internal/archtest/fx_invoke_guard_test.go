package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestFXInvokeGuard 锁住 fx.Invoke 运行时所有权 guard 的共享 allowlist。
// AST matcher 的真实正反例在 fixture 测试中维护，避免 skeleton skip 继续出现在可信输出里。
func TestFXInvokeGuard(t *testing.T) {
	t.Parallel()

	t.Run("shared_root_bridge_allowlist_is_consumable", func(t *testing.T) {
		t.Parallel()
		if len(rootBridgeAllowlist) == 0 {
			t.Fatal("rootBridgeAllowlist is empty; TestFXInvokeGuard and " +
				"TestLifecycleOnStartGuard must share a non-empty seed")
		}
		for _, want := range knownRootBridgeCallSites() {
			if !isRootBridgeException(want.path, want.symbol) {
				t.Fatalf("allowlist missing root-bridge entry %s#%s", want.path, want.symbol)
			}
		}
	})

	t.Run("file_level_exemption_is_not_permitted", func(t *testing.T) {
		t.Parallel()
		// root bridge 文件里的其他符号不能顺带豁免；例外必须精确到 call-site 符号。
		if isRootBridgeException("cmd/mcp-lsp/fx.go", "newBootstrapRunner") {
			t.Error("file-level exemption leaked: newBootstrapRunner should not be treated as a root bridge")
		}
		if isRootBridgeException("internal/app/app.go", "newDesktopFXApp") {
			t.Error("file-level exemption leaked: newDesktopFXApp should not be treated as a root bridge")
		}
	})
}

// knownRootBridgeCallSites 返回必须留在 rootBridgeAllowlist 中的最小 root bridge 集合。
// 新 sidecar 增加 root bridge 时要同步这里和 allowlist，让漂移能在测试里失败。
func knownRootBridgeCallSites() []struct{ path, symbol string } {
	return []struct{ path, symbol string }{
		{"internal/app/app.go", "BindRuntime"},
		{"cmd/mcp-orch/fx.go", "bindRuntime"},
		{"cmd/mcp-lsp/fx.go", "bindRuntime"},
		{"cmd/mcp-ida/fx.go", "bindRuntime"},
	}
}

type fxInvokeGuardViolation struct {
	RelPath string
	Line    int
	Symbol  string
	Reason  string
}

func fxInvokeGuardViolationsInSource(relPath string, source []byte) ([]fxInvokeGuardViolation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relPath, err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Body != nil {
			funcs[fn.Name.Name] = fn
		}
	}

	var violations []fxInvokeGuardViolation
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isFXInvokeCall(call) {
			return true
		}
		for _, arg := range call.Args {
			violations = append(violations, fxInvokeTargetViolations(relPath, fset, funcs, arg)...)
		}
		return true
	})
	return violations, nil
}

func isFXInvokeCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Invoke" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "fx"
}

func fxInvokeTargetViolations(relPath string, fset *token.FileSet, funcs map[string]*ast.FuncDecl, expr ast.Expr) []fxInvokeGuardViolation {
	switch target := expr.(type) {
	case *ast.Ident:
		fn := funcs[target.Name]
		if fn == nil || isRootBridgeException(relPath, target.Name) {
			return nil
		}
		return fxInvokeForbiddenBodyViolations(relPath, fset, target.Name, fn.Body)
	case *ast.FuncLit:
		symbol := fmt.Sprintf("inline@%d", fset.Position(target.Pos()).Line)
		return fxInvokeForbiddenBodyViolations(relPath, fset, symbol, target.Body)
	default:
		return nil
	}
}

func fxInvokeForbiddenBodyViolations(relPath string, fset *token.FileSet, symbol string, body *ast.BlockStmt) []fxInvokeGuardViolation {
	var violations []fxInvokeGuardViolation
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.GoStmt:
			violations = append(violations, fxInvokeViolation(relPath, fset, symbol, node, "starts goroutine"))
		case *ast.CallExpr:
			if reason := fxInvokeForbiddenCallReason(node); reason != "" {
				violations = append(violations, fxInvokeViolation(relPath, fset, symbol, node, reason))
			}
		}
		return true
	})
	return violations
}

func fxInvokeForbiddenCallReason(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fxInvokeForbiddenSelectorReason(fun)
	case *ast.Ident:
		return fxInvokeForbiddenIdentReason(fun)
	}
	return ""
}

func fxInvokeForbiddenSelectorReason(fun *ast.SelectorExpr) string {
	receiver, _ := fun.X.(*ast.Ident)
	if receiver != nil && receiver.Name == "exec" && isAnyName(fun.Sel.Name, "Command", "CommandContext") {
		return "calls exec command"
	}
	if receiver != nil && receiver.Name == "time" && isAnyName(fun.Sel.Name, "Sleep", "After", "NewTicker", "Tick") {
		return "sleeps or retries"
	}
	if strings.HasPrefix(fun.Sel.Name, "Set") {
		return "mutates constructed object through setter"
	}
	return ""
}

func fxInvokeForbiddenIdentReason(fun *ast.Ident) string {
	if strings.Contains(strings.ToLower(fun.Name), "retry") {
		return "sleeps or retries"
	}
	return ""
}

func isAnyName(got string, wants ...string) bool {
	for _, want := range wants {
		if got == want {
			return true
		}
	}
	return false
}

func fxInvokeViolation(relPath string, fset *token.FileSet, symbol string, node ast.Node, reason string) fxInvokeGuardViolation {
	return fxInvokeGuardViolation{
		RelPath: relPath,
		Line:    fset.Position(node.Pos()).Line,
		Symbol:  symbol,
		Reason:  reason,
	}
}

func fxInvokeGuardViolationStrings(violations []fxInvokeGuardViolation) []string {
	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, fmt.Sprintf("%s:%d %s: %s", violation.RelPath, violation.Line, violation.Symbol, violation.Reason))
	}
	return lines
}
