package archtest

import "testing"

// TestFXInvokeGuard 锁住 fx.Invoke 运行时所有权 guard 的共享 allowlist。
// 这里先确认 root bridge 例外必须按“文件+符号”精确匹配，避免后续 matcher 把整文件放行。
// 暂未落地的 matcher 子测保持 t.Skip，让后续实现能在同一 guard 中补齐真实扫描。
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
