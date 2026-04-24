package archtest

import "testing"

// TestLifecycleOnStartGuard is the P22 fx.Lifecycle.OnStart runtime-ownership
// guard shell (P0 骨架; see docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md
// §守卫改动建议-2).
//
// What P0 delivers here:
//   - Reuses the shared rootBridgeAllowlist; no file-level carve-out is ever
//     introduced for OnStart.
//   - Asserts each known root-bridge entry is exempt via (call_site_path,
//     symbol), and that unrelated OnStart targets are NOT treated as bridges.
//   - Documents the one-hop helper resolution contract (`OnStart -> helper
//     -> go / SafeGo / NewTicker`) that every owning-slice matcher must
//     implement before flipping its subtest red→green. P0 ships the contract
//     as a skeleton; the AST walker is owned by the slice whose red-green
//     sample lands first (likely P1b Finding 3 / 4).
//
// Matchers owned by downstream slices:
//   - P1b (Finding 3, internal/platform/mcpcontrol/module.go): sweeper OnStart
//   - P1b (Finding 4, internal/platform/rpc/module.go): approval cleanup loop
//   - P2  (Finding 9, internal/platform/toolbridge/module.go): OnStart -> go ServeProxy
//   - P2  (Finding 6, internal/module/memory/team/...): watcher OnStart
func TestLifecycleOnStartGuard(t *testing.T) {
	t.Parallel()

	t.Run("shared_root_bridge_allowlist_is_consumable", func(t *testing.T) {
		t.Parallel()
		if len(rootBridgeAllowlist) == 0 {
			t.Fatal("rootBridgeAllowlist is empty; TestLifecycleOnStartGuard must " +
				"share the same seed as TestFXInvokeGuard")
		}
	})

	t.Run("root_bridge_entries_exempted_by_call_site_and_symbol", func(t *testing.T) {
		t.Parallel()
		exempt := []struct{ path, symbol string }{
			{"internal/app/app.go", "BindRuntime"},
			{"cmd/mcp-orch/fx.go", "bindRuntime"},
			{"cmd/mcp-lsp/fx.go", "bindRuntime"},
			{"cmd/mcp-ida/fx.go", "bindRuntime"},
		}
		for _, e := range exempt {
			if !isRootBridgeException(e.path, e.symbol) {
				t.Errorf("expected root-bridge exemption for %s#%s", e.path, e.symbol)
			}
		}

		// Negative: an OnStart inside a module package must NOT be exempt.
		notExempt := []struct{ path, symbol string }{
			{"internal/platform/mcpcontrol/module.go", "registerSweeper"},
			{"internal/platform/rpc/module.go", "registerApprovalCleanup"},
			{"internal/platform/toolbridge/module.go", "startProxy"},
			{"internal/module/memory/module.go", "startTeamSync"},
		}
		for _, e := range notExempt {
			if isRootBridgeException(e.path, e.symbol) {
				t.Errorf("unexpected root-bridge exemption for %s#%s", e.path, e.symbol)
			}
		}
	})

	matcherCases := []struct {
		name        string
		owningSlice string
	}{
		{
			name:        "onstart_must_not_start_ticker_goroutine",
			owningSlice: "P1b (Findings 3, 4)",
		},
		{
			name:        "onstart_must_not_start_watcher_goroutine",
			owningSlice: "P2 (Finding 6, memory team_sync_watcher)",
		},
		{
			name:        "onstart_must_not_fire_and_forget_serve_proxy",
			owningSlice: "P2 (Finding 9, toolbridge proxy)",
		},
		{
			name:        "onstart_one_hop_helper_resolution_wired",
			owningSlice: "P0 contract / first owning slice to land the AST walker",
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
