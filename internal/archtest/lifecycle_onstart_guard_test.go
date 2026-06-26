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

// TestLifecycleOnStartGuard 固定 fx.Lifecycle.OnStart 的运行时归属守卫。
// 它复用 rootBridgeAllowlist 验证根桥接豁免，并确保普通 OnStart 目标不会被误判成 bridge。
// 子测试同时描述一跳 helper 解析边界：OnStart 包一层 helper 后启动 goroutine 或 ticker 也必须被识别。
func TestLifecycleOnStartGuard(t *testing.T) {
	t.Parallel()

	t.Run("shared_root_bridge_allowlist_is_consumable", assertRootBridgeAllowlistConsumable)
	t.Run("root_bridge_entries_exempted_by_call_site_and_symbol", assertRootBridgeEntries)
	runLifecycleMatcherSkeletonSubtests(t)
	t.Run("onstart_must_not_start_ticker_goroutine", assertNoOnStartTickerGoroutine)
	t.Run("onstart_must_not_fire_and_forget_serve_proxy", assertNoFireAndForgetServeProxy)
}

func assertRootBridgeAllowlistConsumable(t *testing.T) {
	t.Parallel()
	if len(rootBridgeAllowlist) == 0 {
		t.Fatal("rootBridgeAllowlist is empty; TestLifecycleOnStartGuard must " +
			"share the same seed as TestFXInvokeGuard")
	}
}

func assertRootBridgeEntries(t *testing.T) {
	t.Parallel()
	for _, e := range rootBridgeExemptCases() {
		if !isRootBridgeException(e.path, e.symbol) {
			t.Errorf("expected root-bridge exemption for %s#%s", e.path, e.symbol)
		}
	}
	for _, e := range rootBridgeNegativeCases() {
		if isRootBridgeException(e.path, e.symbol) {
			t.Errorf("unexpected root-bridge exemption for %s#%s", e.path, e.symbol)
		}
	}
}

func rootBridgeExemptCases() []struct{ path, symbol string } {
	return []struct{ path, symbol string }{
		{"internal/app/app.go", "BindRuntime"},
		{"cmd/mcp-orch/fx.go", "bindRuntime"},
		{"cmd/mcp-lsp/fx.go", "bindRuntime"},
		{"cmd/mcp-ida/fx.go", "bindRuntime"},
	}
}

func rootBridgeNegativeCases() []struct{ path, symbol string } {
	return []struct{ path, symbol string }{
		{"internal/platform/mcpcontrol/module.go", "registerSweeper"},
		{"internal/platform/rpc/module.go", "registerApprovalCleanup"},
		{"internal/platform/toolbridge/module.go", "startProxy"},
		{"internal/module/memory/module.go", "startTeamSync"},
	}
}

func runLifecycleMatcherSkeletonSubtests(t *testing.T) {
	matcherCases := []struct {
		name        string
		owningSlice string
	}{
		{
			name:        "onstart_must_not_start_watcher_goroutine",
			owningSlice: "P2 (Finding 6, memory team_sync_watcher)",
		},
		{
			name:        "onstart_one_hop_helper_resolution_wired",
			owningSlice: "P0 contract / first owning slice to land the AST walker",
		},
	}

	for _, tc := range matcherCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Skipf("matcher skeleton only; owning slice will flip red→green: %s", tc.owningSlice)
		})
	}
}

func assertNoOnStartTickerGoroutine(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	targets := []struct {
		path      string
		forbidden []string
	}{
		{
			path: filepath.Join(root, "internal", "platform", "mcpcontrol", "module.go"),
			forbidden: []string{
				"func registerSweeperLifecycle",
				"go sweeper.Run(",
			},
		},
		{
			path: filepath.Join(root, "internal", "platform", "rpc", "module.go"),
			forbidden: []string{
				"func startApprovalLifecycle",
				"go startApprovalCleanupLoop(",
			},
		},
		{
			path: filepath.Join(root, "internal", "platform", "rpc", "approval_lifecycle.go"),
			forbidden: []string{
				"func startApprovalCleanupLoop(",
			},
		},
	}
	for _, target := range targets {
		assertFileExcludesTokens(t, target.path, target.forbidden,
			"reintroduced pre-P1b OnStart ticker path")
	}
}

func assertNoFireAndForgetServeProxy(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal", "platform", "toolbridge", "module.go")
	assertFileExcludesTokens(t, path, []string{
		"go h.ServeProxy(",
		"go func(proxyListener",
	}, "reintroduced pre-P2 ServeProxy fire-and-forget path")
}

func assertFileExcludesTokens(t *testing.T, path string, forbidden []string, label string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	hits := containedTokens(string(data), forbidden)
	if len(hits) > 0 {
		t.Fatalf("%s %s; forbidden tokens present: %v", path, label, hits)
	}
}

func containedTokens(src string, tokens []string) []string {
	var hits []string
	for _, token := range tokens {
		if strings.Contains(src, token) {
			hits = append(hits, token)
		}
	}
	return hits
}

func TestShutdownOrdering(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal", "app", "runner.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	onStop := bindRuntimeOnStopBody(t, file)
	if len(onStop.List) < 3 {
		t.Fatalf("BindRuntime OnStop has %d statements, want at least 3", len(onStop.List))
	}
	checks := []struct {
		idx  int
		name string
		ok   func(ast.Stmt) bool
	}{
		{idx: 0, name: "cancel guard", ok: isCancelIfStmt},
		{idx: 1, name: "RunGroup wait", ok: isRuntimeDoneWaitStmt},
		{idx: 2, name: "runtime drain", ok: isDrainRuntimeBeforeStopStmt},
	}
	for _, check := range checks {
		if !check.ok(onStop.List[check.idx]) {
			t.Errorf("BindRuntime OnStop stmt %d = %T at %s, want %s", check.idx+1, onStop.List[check.idx], fset.Position(onStop.List[check.idx].Pos()), check.name)
		}
	}
}

func bindRuntimeOnStopBody(t *testing.T, file *ast.File) *ast.BlockStmt {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := bindRuntimeFunc(decl)
		if !ok {
			continue
		}
		if body := findFxOnStopBody(fn.Body); body != nil {
			return body
		}
	}
	t.Fatal("BindRuntime fx.Hook OnStop func literal not found")
	return nil
}

func bindRuntimeFunc(decl ast.Decl) (*ast.FuncDecl, bool) {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Name.Name != "BindRuntime" || fn.Body == nil {
		return nil, false
	}
	return fn, true
}

func findFxOnStopBody(body *ast.BlockStmt) *ast.BlockStmt {
	var onStop *ast.BlockStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if onStop != nil {
			return false
		}
		cl, ok := n.(*ast.CompositeLit)
		if !ok || !isFxHookComposite(cl) {
			return true
		}
		onStop = fxOnStopFuncBody(cl)
		return onStop == nil
	})
	return onStop
}

func fxOnStopFuncBody(cl *ast.CompositeLit) *ast.BlockStmt {
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok || keyName(kv.Key) != "OnStop" {
			continue
		}
		if lit, ok := kv.Value.(*ast.FuncLit); ok && lit.Body != nil {
			return lit.Body
		}
	}
	return nil
}

func isCancelIfStmt(stmt ast.Stmt) bool {
	ifStmt, ok := stmt.(*ast.IfStmt)
	if !ok || len(ifStmt.Body.List) == 0 {
		return false
	}
	return isCallExprStmt(ifStmt.Body.List[0], "cancel")
}

func isRuntimeDoneWaitStmt(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if len(s.Rhs) != 1 {
			return false
		}
		return isCallExpr(s.Rhs[0], "waitForRuntimeDone") || isReceiveExpr(s.Rhs[0], "done")
	case *ast.ExprStmt:
		return isReceiveExpr(s.X, "done") || isCallExpr(s.X, "waitForRuntimeDone")
	default:
		return false
	}
}

func isDrainRuntimeBeforeStopStmt(stmt ast.Stmt) bool {
	return isCallExprStmt(stmt, "drainRuntimeBeforeStop")
}

func isCallExprStmt(stmt ast.Stmt, name string) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	return ok && isCallExpr(expr.X, name)
}

func isCallExpr(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == name
}

func isReceiveExpr(expr ast.Expr, name string) bool {
	recv, ok := expr.(*ast.UnaryExpr)
	if !ok || recv.Op != token.ARROW {
		return false
	}
	ident, ok := recv.X.(*ast.Ident)
	return ok && ident.Name == name
}
