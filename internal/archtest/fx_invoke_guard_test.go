package archtest

import "testing"

// TestFXInvokeGuard is the P22 fx.Invoke runtime-ownership guard shell
// (P0 骨架; see docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md §守卫改动建议-1).
//
// What P0 delivers here:
//   - A subtest that ties this guard to the same rootBridgeAllowlist used
//     by TestLifecycleOnStartGuard, so future matcher work cannot fork the
//     exemption list.
//   - A negative sanity check that file-level exemption is not accidentally
//     permitted (isRootBridgeException is keyed on (path, symbol)).
//   - Matcher subtests declared as t.Skip, tagged by owning slice. Each
//     downstream PR replaces its tc.skipReason with a real matcher call and
//     flips the subtest red→green alongside the fix.
//
// Matchers owned by downstream slices (see README.md §Finding -> gate 速查):
//   - P1a (Finding 1): fx.Invoke(spawnToolbridgePeers) inside
//     internal/provider/codexapp/module.go
//   - P2 (Finding 9 wiring side): fx.Invoke(SetToolHandler/SetListTools) late
//     setter injection under internal/platform/toolbridge/module.go
//   - P2 (thread lane wiring): fx.Invoke(bindDispatcher) / fx.Invoke(bindPromptStore)
//     post-construction mutation under internal/module/thread
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
		// Picking a random extra symbol inside a root-bridge file MUST NOT
		// be exempt — P0 explicitly forbids "any call inside this file is
		// fine" carve-outs.
		if isRootBridgeException("cmd/mcp-lsp/fx.go", "newBootstrapRunner") {
			t.Error("file-level exemption leaked: newBootstrapRunner should not be treated as a root bridge")
		}
		if isRootBridgeException("internal/app/app.go", "newDesktopFXApp") {
			t.Error("file-level exemption leaked: newDesktopFXApp should not be treated as a root bridge")
		}
	})

	matcherCases := []struct {
		name        string
		owningSlice string
	}{
		{
			name:        "fx_invoke_target_must_not_start_long_running_goroutine",
			owningSlice: "P1a (Finding 1, codexapp peer supervisor)",
		},
		{
			name:        "fx_invoke_target_must_not_call_exec_command",
			owningSlice: "P1a / P1b",
		},
		{
			name:        "fx_invoke_target_must_not_post_construct_mutate_via_setter",
			owningSlice: "P2 (thread dispatcher/prompt-store + toolbridge late setter)",
		},
		{
			name:        "fx_invoke_target_must_not_sleep_or_retry",
			owningSlice: "P1a / P2",
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

// knownRootBridgeCallSites is the minimum expected set of (call_site_path,
// symbol) pairs that must remain in rootBridgeAllowlist. When a new cmd/*
// sidecar grows its own root bridge, add it both here and in
// rootBridgeAllowlist — the overlap is intentional so drift fails the suite.
func knownRootBridgeCallSites() []struct{ path, symbol string } {
	return []struct{ path, symbol string }{
		{"internal/app/app.go", "BindRuntime"},
		{"cmd/mcp-orch/fx.go", "bindRuntime"},
		{"cmd/mcp-lsp/fx.go", "bindRuntime"},
		{"cmd/mcp-ida/fx.go", "bindRuntime"},
	}
}
