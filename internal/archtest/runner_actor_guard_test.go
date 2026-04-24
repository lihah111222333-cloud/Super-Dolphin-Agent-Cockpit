package archtest

import "testing"

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
func TestRunnerActorGuard(t *testing.T) {
	t.Parallel()

	t.Run("forbidden_token_catalogue_is_locked", func(t *testing.T) {
		t.Parallel()
		want := []string{
			"go ",                  // bare go-statement inside Run(ctx)
			"runtimesafe.SafeGo(",  // SafeGo inside actor loop
			"time.NewTicker(",      // ticker without owner-managed stop
			"time.AfterFunc(",      // fire-and-forget timer
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

	matcherCases := []struct {
		name        string
		owningSlice string
	}{
		{
			name:        "actor_run_ctx_must_not_fire_waiter_goroutine",
			owningSlice: "P3 (Finding 8, orchestration/process_lifecycle)",
		},
		{
			name:        "actor_run_ctx_auxiliary_goroutines_must_join_on_stop",
			owningSlice: "P1c (codexapp session runtime reader/health)",
		},
	}

	for _, tc := range matcherCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Skipf("matcher skeleton only; owning slice will flip red→green: %s", tc.owningSlice)
		})
	}
}
