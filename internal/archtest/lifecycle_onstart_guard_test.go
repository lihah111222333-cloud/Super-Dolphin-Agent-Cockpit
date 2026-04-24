package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	// P22 P1b live matcher (Findings 3 + 4): after the sweeper and approval-
	// cleanup loops moved to platformrunner.Runner providers, neither module
	// file may re-introduce the pre-P1b OnStart ticker spawn. Scope is limited
	// to the two hot files so unrelated packages are not force-flipped.
	t.Run("onstart_must_not_start_ticker_goroutine", func(t *testing.T) {
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
			data, err := os.ReadFile(target.path)
			if err != nil {
				t.Fatalf("read %s: %v", target.path, err)
			}
			src := string(data)
			var hits []string
			for _, token := range target.forbidden {
				if strings.Contains(src, token) {
					hits = append(hits, token)
				}
			}
			if len(hits) > 0 {
				t.Errorf("%s reintroduced pre-P1b OnStart ticker path; forbidden tokens present: %v", target.path, hits)
			}
		}
	})

	// P22 P2 Finding 9 live matcher: ProxyRunner now owns the ServeProxy
	// blocking call. internal/platform/toolbridge/module.go must only wire
	// the listener + addr publish; it cannot re-introduce the pre-P2
	// `go h.ServeProxy(...)` pattern inside registerProxyLifecycle.
	t.Run("onstart_must_not_fire_and_forget_serve_proxy", func(t *testing.T) {
		t.Parallel()
		root := repoRootForGuardTests(t)
		path := filepath.Join(root, "internal", "platform", "toolbridge", "module.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		forbidden := []string{
			"go h.ServeProxy(",
			"go func(proxyListener",
		}
		var hits []string
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, token)
			}
		}
		if len(hits) > 0 {
			t.Fatalf("toolbridge/module.go reintroduced pre-P2 ServeProxy fire-and-forget path; forbidden tokens present: %v", hits)
		}
	})
}

func TestShutdownOrdering(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal", "app", "runner.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(data)
	drain := strings.Index(src, "DrainPendingExtraction")
	cancel := strings.Index(src, "cancel()")
	wait := strings.Index(src, "case <-done:")
	if drain >= 0 && cancel >= 0 && drain < cancel {
		t.Errorf("shutdown regression: DrainPendingExtraction appears before root cancel in internal/app/runner.go")
	}
	if cancel >= 0 && wait >= 0 && cancel > wait {
		t.Errorf("shutdown regression: cancel appears after RunGroup wait in internal/app/runner.go")
	}
}
