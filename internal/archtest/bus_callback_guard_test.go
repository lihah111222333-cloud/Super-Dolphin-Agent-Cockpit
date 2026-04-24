package archtest

import "testing"

// TestBusCallbackGuard is the P22 bus-subscriber callback slow-path guard
// shell (P0 骨架; see docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md
// §守卫改动建议-3).
//
// What P0 delivers here:
//   - Declares the forbidden-shape catalogue that the bus_callback matcher
//     must eventually enforce. The catalogue is the authoritative list for
//     P1b/P2/P3 slice PRs — when a slice lands its fix, it wires the AST
//     matcher against these tokens and flips the relevant subtest.
//   - Matcher subtests are parked as t.Skip until the owning slice lands.
//   - Nothing here uses the root-bridge allowlist: bus callbacks never share
//     exemption with the root runtime bridge. P0 explicitly forbids
//     "subscriber wiring 旁边就算 wiring 豁免" carve-outs.
//
// Matchers owned by downstream slices:
//   - P2 (Finding 5, internal/module/memory/module.go):
//     TeamSync callback -> StartSession
//   - P2 (Finding 7, internal/module/memory/auto_dream_task.go):
//     auto-dream scheduling inside event callback
//   - P2 (Finding 10, internal/module/memory/...):
//     ToolCallEnd -> AddToolReadResult -> os.ReadFile synchronous I/O
//   - P2 (thread wiring, internal/module/thread):
//     fx.Invoke(registerSubscriptions) that re-enters setter injection
//   - P2 (internal/platform/hooks/event_relay.go):
//     fire-and-forget `go` in relay callback
func TestBusCallbackGuard(t *testing.T) {
	t.Parallel()

	t.Run("forbidden_token_catalogue_is_locked", func(t *testing.T) {
		t.Parallel()
		// Freezing the catalogue here prevents silent drift between P0 and
		// the owning-slice matchers. Any add/remove in the catalogue must
		// land in the same PR that flips a matcher red→green.
		want := []string{
			"go ",                                        // bare go-statement in callback body
			"runtimesafe.SafeGo(",                        // SafeGo dispatch from callback
			"time.Sleep(",                                // blocking sleep
			"exec.Command(", "exec.CommandContext(",      // process spawn
			"StartSession(", "StopSession(",              // session lifecycle driven from callback
			"NotifyConfigChanged(",                       // fan-out config reload
			"DispatchAfter(",                             // timer-backed slow-path
			"AddToolReadResult(",                         // NestedRuntime slow read (Finding 10)
			"os.ReadFile(", "os.WriteFile(",              // synchronous disk I/O
		}
		if len(want) == 0 {
			t.Fatal("bus-callback forbidden token catalogue is empty")
		}
		// Sanity: no duplicates — drift would mask a true regression later.
		seen := map[string]struct{}{}
		for _, token := range want {
			if _, dup := seen[token]; dup {
				t.Errorf("duplicate forbidden token in catalogue: %q", token)
			}
			seen[token] = struct{}{}
		}
	})

	matcherCases := []struct {
		name        string
		owningSlice string
	}{
		{
			name:        "bus_callback_must_not_start_session",
			owningSlice: "P2 (Finding 5, memory TeamSync)",
		},
		{
			name:        "bus_callback_must_not_schedule_auto_dream",
			owningSlice: "P2 (Finding 7, auto-dream scheduler)",
		},
		{
			name:        "bus_callback_must_not_do_synchronous_file_io",
			owningSlice: "P2 (Finding 10, NestedRuntime tool-read)",
		},
		{
			name:        "bus_callback_must_not_fire_and_forget_goroutine",
			owningSlice: "P2 (hooks/event_relay fanout)",
		},
		{
			name:        "bus_callback_must_not_register_late_setter",
			owningSlice: "P2 (thread registerSubscriptions -> bindDispatcher/bindPromptStore)",
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
