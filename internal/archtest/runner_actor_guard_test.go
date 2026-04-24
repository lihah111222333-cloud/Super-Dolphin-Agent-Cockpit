package archtest

import (
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
		root := repoRootForGuardTests(t)
		want := []ownershipHit{
			{"F-3", "internal/module/memory/module.go", "registerMemoryHooks", "Start"},
			{"F-4", "internal/module/thread/module.go", "registerSubscriptions", "startBusWorkers"},
			{"F-5", "internal/platform/cachekeepalive/module.go", "registerKeepaliveLifecycle", "startKeepaliveRelay"},
		}
		for _, hit := range want {
			line, ok := findCallInFunction(t, root, hit.Path, hit.Symbol, hit.Call)
			if !ok {
				t.Logf("[P22.1 WARN] runner no-new guard missing TODO-locked call: %+v", hit)
				t.Fail()
				continue
			}
			t.Logf("[P22.1 WARN] %s %s:%d %s", hit.Finding, hit.Path, line, hit.Symbol)
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
