package memory

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Phase B.1 baseline tests for the inject-path downgrade flag contract
// (mapping §10). p25 B-class queue analysis + 独立 reviewer 验证后
// 确认 mapping §10 已被现有 gate.go + nested/claudemd_filter.go +
// SuppressForOverlay 等价覆盖（详见 p25 文档 B-class 表的销账理由）。
//
// 本组测试锁定的两件事：
//
//  1. SessionFlag 字面量契约（tengu_moth_copse / tengu_paper_halyard 等）
//     与 Claude Code 兼容性绑定，误改字面量（"tengu_moth_corpse"）会
//     静默 break overlay 模式，需 baseline 防回归。grep 全仓现有测试
//     未覆盖这些字面量。
//
//  2. SuppressForOverlay 短路新增的 InjectPromptEntrypoint 字段
//     （gate.go:55-56 / :88，introduced after
//     TestResolveMemoryGateOverlaySuppressesIndexAndPrefetch was
//     written，原测试只 cover InjectMemoryIndex/InjectTeamMemIndex/
//     EnableRelevantPrefetch 三项）。
//
// 现有覆盖（不重复）：
//   - TestResolveMemoryGateOverlaySuppressesIndexAndPrefetch
//     (config_test.go:418) — 4 field overlay short-circuit
//   - TestMemoryRulesProviderSuppressedInOverlay (config_test.go:456)
//   - TestMemoryEntrypointProviderRespectsInjectPromptEntrypoint
//     (entrypoint_provider_test.go:257)
//   - agent/agent_test.go fakeGate.SuppressForOverlay regression

func TestPhaseB1_TenguMothCopseSessionFlagMapsToSkipIndex(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	cfg := &Config{Enabled: true, Features: MemoryFeatureFlags{TeamMemory: true}}
	for _, alias := range []string{"tengu_moth_copse", "skip_index", "skipIndex"} {
		t.Run(alias, func(t *testing.T) {
			buildCtx := contract.BuildCtx{
				SessionFlags: map[string]bool{alias: true},
			}
			gate := ResolveMemoryGate(buildCtx, cfg)
			if !gate.SkipIndex {
				t.Fatalf("SessionFlag %q did not set SkipIndex (mapping §10 alias contract violated)", alias)
			}
			if gate.InjectMemoryIndex {
				t.Fatalf("SkipIndex=true but InjectMemoryIndex=true (gate.go:86 short-circuit broken)")
			}
			if gate.InjectTeamMemIndex {
				t.Fatalf("SkipIndex=true but InjectTeamMemIndex=true (gate.go:87 short-circuit broken)")
			}
		})
	}
}

func TestPhaseB1_TenguPaperHalyardSessionFlagMapsToSkipProjectLocalClaudeMd(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	cfg := &Config{Enabled: true}
	for _, alias := range []string{"tengu_paper_halyard", "paper_halyard"} {
		t.Run(alias, func(t *testing.T) {
			buildCtx := contract.BuildCtx{
				SessionFlags: map[string]bool{alias: true},
			}
			gate := ResolveMemoryGate(buildCtx, cfg)
			if !gate.SkipProjectLocalClaudeMd {
				t.Fatalf("SessionFlag %q did not set SkipProjectLocalClaudeMd (mapping §10 alias contract violated)", alias)
			}
		})
	}
}

// Counter-baseline: unknown SessionFlags must NOT enable downgrade
// (defends against accidental wildcard / contains match in
// gateFlagEnabled / gateFlagValue).
func TestPhaseB1_UnknownSessionFlagDoesNotEnableDowngrade(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	cfg := &Config{Enabled: true, Features: MemoryFeatureFlags{TeamMemory: true}}
	buildCtx := contract.BuildCtx{
		SessionFlags: map[string]bool{
			"future_flag":           true,
			"tengu_unknown":         true,
			"tengu_moth_copse_typo": true,
			"":                      true,
		},
	}
	gate := ResolveMemoryGate(buildCtx, cfg)
	if gate.SkipIndex {
		t.Fatal("unknown SessionFlag wrongly enabled SkipIndex (literal match contract violated)")
	}
	if gate.SkipProjectLocalClaudeMd {
		t.Fatal("unknown SessionFlag wrongly enabled SkipProjectLocalClaudeMd")
	}
}

// TestPhaseB1_SuppressForOverlayCoversInjectPromptEntrypoint extends
// TestResolveMemoryGateOverlaySuppressesIndexAndPrefetch (config_test.go:418)
// with the InjectPromptEntrypoint field introduced after that test was
// written (gate.go:55-56 / :88). InjectPromptEntrypoint today tracks
// InjectMemoryIndex but is its own field so future overlay-light modes
// can disable just the prompt entrypoint while keeping the underlying
// memory store live; in claude_code overlay it must be false.
func TestPhaseB1_SuppressForOverlayCoversInjectPromptEntrypoint(t *testing.T) {
	t.Setenv(envHarnessKind, "claude_code")
	cfg := &Config{
		Enabled:  true,
		Features: MemoryFeatureFlags{TeamMemory: true},
	}
	gate := ResolveMemoryGate(contract.BuildCtx{}, cfg)
	if !gate.SuppressForOverlay() {
		t.Fatal("SuppressForOverlay()=false under claude_code harness; cfg fixture broken")
	}
	if gate.InjectPromptEntrypoint {
		t.Fatal("overlay InjectPromptEntrypoint=true; expected suppression (gate.go:88 wiring broken)")
	}
}
