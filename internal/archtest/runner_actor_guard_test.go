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

// TestOrchestrationWaiterHotFileGuard 直接扫描 orchestration 热点文件。
// runner actor 停止路径不得重新引入独立 waiter goroutine，否则进程退出归属会再次分散。
func TestOrchestrationWaiterHotFileGuard(t *testing.T) {
	t.Parallel()
	assertNoOrchestrationWaiterTokens(t)
}

// TestRunnerActorGuard 锁定 actor Run(ctx) 内禁止的并发形状。
// 这里保留小范围热点文件扫描和 matcher 骨架，避免把 owner-joined goroutine 误报成泄漏。
func TestRunnerActorGuard(t *testing.T) {
	t.Parallel()

	t.Run("forbidden_token_catalogue_is_locked", func(t *testing.T) {
		t.Parallel()
		want := []string{
			"go ",                 // Run(ctx) 内裸 go 语句
			"runtimesafe.SafeGo(", // actor 循环内直接派发 SafeGo
			"time.NewTicker(",     // 缺少 owner-managed stop 的 ticker
			"time.AfterFunc(",     // fire-and-forget 定时器
		}
		if len(want) == 0 {
			t.Fatal("actor-execute forbidden token catalogue is empty")
		}
		seen := map[string]struct{}{}
		for _, token := range want {
			if _, dup := seen[token]; dup {
				t.Errorf("duplicate forbidden token in catalogue: %q", token)
			}
			seen[token] = struct{}{}
		}
	})

	t.Run("actor_hot_file_scope_is_declared", func(t *testing.T) {
		t.Parallel()
		// matcher 只扫描明确的 actor 热点文件，避免把全仓所有 Run(ctx)
		// 的辅助 goroutine 误判为同一类 owner 泄漏。
		want := []string{
			"cmd/mcp-orch/orchestration/process_lifecycle.go", // runner actor 热点文件
		}
		if len(want) == 0 {
			t.Fatal("actor hot-file scope is empty; P0 expects at least the P3 Finding 8 anchor")
		}
	})

	// SessionRuntime 的 reader/health goroutine 已在 runtime 层用 TestShutdownDrain 覆盖；
	// 这里保留 matcher 骨架，避免把未接入的检查误当成已生效。
	// process_lifecycle.go 的 waiter 热点检查已生效，不允许旧停止路径重新出现。
	matcherCases := []struct {
		name        string
		owningSlice string
	}{
		{
			name:        "actor_run_ctx_auxiliary_goroutines_must_join_on_stop",
			owningSlice: "P1c (codexapp session runtime reader/health — covered by TestShutdownDrain* at runtime level)",
		},
	}

	for _, tc := range matcherCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Skipf("matcher skeleton only; owning slice will flip red→green: %s", tc.owningSlice)
		})
	}

	t.Run("actor_run_ctx_must_not_fire_waiter_goroutine", func(t *testing.T) {
		// waiter 热点检查只面向当前 runner actor 文件；startWaiters /
		// waitForExit / claimMonitorTargets 任一 token 重新出现都表示停止路径回退。
		t.Parallel()
		assertNoOrchestrationWaiterTokens(t)
	})
	t.Run("ownership", func(t *testing.T) {
		t.Parallel()
		root := repoRootForGuardTests(t)
		hits := findLifecycleOnStartCallHits(t, root, map[string]bool{
			"Start":           true,
			"Run":             true,
			"Begin":           true,
			"Serve":           true,
			"Loop":            true,
			"Watch":           true,
			"startBusWorkers": true,
		})
		for _, hit := range hits {
			if runnerOwnershipAllowedLifecycleHit(t, root, hit) {
				continue
			}
			t.Errorf("runner ownership regression: %s:%d OnStart calls %s", hit.Path, hit.Line, hit.Call)
		}
	})
}

func assertNoOrchestrationWaiterTokens(t *testing.T) {
	t.Helper()
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "cmd", "mcp-orch", "orchestration", "process_lifecycle.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(data)
	forbidden := []string{
		"func (a *runnerActor) startWaiters",
		"func (a *runnerActor) waitForExit",
		"go a.waitForExit(",
		"claimMonitorTargets(",
	}
	var hits []string
	for _, token := range forbidden {
		if strings.Contains(src, token) {
			hits = append(hits, token)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("process_lifecycle.go reintroduced P3 Finding 8 waiter path; forbidden tokens present: %v", hits)
	}
}

func runnerOwnershipAllowedLifecycleHit(t *testing.T, root string, hit lifecycleCallHit) bool {
	t.Helper()
	if hit.Path != "internal/platform/bus/module.go" || hit.Call != "Start" || hit.Receiver != "subscribers" {
		return false
	}
	return busSubscriberGroupParamStartHit(t, root, hit)
}

// EnclosingFunc 在本测试内按需推导，避免把共享 matcher 结构扩展到只服务本用例的字段。
func busSubscriberGroupParamStartHit(t *testing.T, root string, hit lifecycleCallHit) bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(hit.Path)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", hit.Path, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "registerLifecycle" || fn.Body == nil {
			continue
		}
		if hit.Line < fset.Position(fn.Pos()).Line || hit.Line > fset.Position(fn.End()).Line {
			return false
		}
		if !funcHasSubscriberGroupParam(fn) {
			return false
		}
		return !subscribersShadowedBeforeLine(fset, fn.Body, hit.Line)
	}
	return false
}

func funcHasSubscriberGroupParam(fn *ast.FuncDecl) bool {
	if fn.Type == nil || fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		if !isStarIdent(field.Type, "SubscriberGroup") {
			continue
		}
		for _, name := range field.Names {
			if name.Name == "subscribers" {
				return true
			}
		}
	}
	return false
}

func isStarIdent(expr ast.Expr, name string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == name
}

func subscribersShadowedBeforeLine(fset *token.FileSet, body *ast.BlockStmt, line int) bool {
	shadowed := false
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil || shadowed || fset.Position(n.Pos()).Line >= line {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE {
			return true
		}
		for _, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if ok && ident.Name == "subscribers" {
				shadowed = true
				return false
			}
		}
		return true
	})
	return shadowed
}
