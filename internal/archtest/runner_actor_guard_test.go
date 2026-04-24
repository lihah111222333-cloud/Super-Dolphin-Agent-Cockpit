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

// TestRunnerActorGuard is the P22 run.Group actor-execute guard shell
// (P0 骨架; see docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md
// §守卫改动建议-4).
//
// What P0 delivers here:
//   - Declares the "forbidden inside Run(ctx)" shape catalogue — bare `go `,
//     SafeGo dispatch, NewTicker without owner-managed stop, waiter goroutine
//     for a per-object Done(). The catalogue freezes the expectations that
//     P3 (Finding 8) and P1c sessionRuntime Run paths must satisfy.
//   - Matcher subtests are t.Skip until the owning slice lands.
//   - P0 §收口口径 explicitly scopes this guard to actor hot-files / owning
//     slice, not a repo-wide sweep. The final matcher must take a list of
//     "actor files" and walk their Run(ctx) function bodies + one hop of
//     helper calls — same `一跳 helper 解析` contract as OnStart.
//
// Matchers owned by downstream slices:
//   - P3 (Finding 8, cmd/mcp-orch/orchestration/process_lifecycle.go:220-239):
//     actor re-spawning waiter goroutine inside Run
//   - P1c (codexapp session runtime Run paths): reader/health goroutines must
//     be owner-joined, not fire-and-forget
func TestOrchestrationWaiterHotFileGuard(t *testing.T) {
	t.Parallel()
	assertNoOrchestrationWaiterTokens(t)
}

func TestRunnerActorGuard(t *testing.T) {
	t.Parallel()

	t.Run("forbidden_token_catalogue_is_locked", func(t *testing.T) {
		t.Parallel()
		want := []string{
			"go ",                 // bare go-statement inside Run(ctx)
			"runtimesafe.SafeGo(", // SafeGo inside actor loop
			"time.NewTicker(",     // ticker without owner-managed stop
			"time.AfterFunc(",     // fire-and-forget timer
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
		// P0 keeps the matcher scoped to a small hot-file list so the
		// owning-slice PR has a specific sample to fix — repo-wide scanning
		// for `go ` inside every Run(ctx) is out of scope for P0 per
		// §TDD 与清理要求 "按 actor hot-file / owning slice 收窄推进".
		want := []string{
			"cmd/mcp-orch/orchestration/process_lifecycle.go", // Finding 8
		}
		if len(want) == 0 {
			t.Fatal("actor hot-file scope is empty; P0 expects at least the P3 Finding 8 anchor")
		}
	})

	// P22 P3 (commit 4dfed68 + follow-up): P1c's deferred matcher stays as a
	// skeleton because SessionRuntime goroutines are owner-joined via the
	// readerMu/readerDone pair (already covered by runtime TestShutdownDrain).
	// The P3 matcher below is now live: orchestration's process_lifecycle.go
	// must no longer contain the legacy waiter path.
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
		// P3 live matcher: scoped to orchestration/process_lifecycle.go.
		// After P3 (commit 4dfed68 + follow-up), the legacy waiter path
		// (startWaiters / waitForExit / claimMonitorTargets call) has been
		// fully removed. Reappearance of any of these tokens inside the hot
		// file is a regression.
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

// EnclosingFunc is intentionally derived here instead of added to lifecycleCallHit:
// this file's write-set cannot touch the shared bus callback matcher structs.
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
