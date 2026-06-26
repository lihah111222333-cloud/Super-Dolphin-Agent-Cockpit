package memory

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// 本组测试锁定 inject-path downgrade 旗标的兼容边界。
// 这些字面量来自外部 provider 兼容约定，不能因为内部字段改名而被放宽成 contains 或通配匹配。
//
// 本组测试锁定的两件事：
//
//  1. SessionFlag 字面量契约（tengu_moth_copse / tengu_paper_halyard 等）
//     与 Claude Code 兼容性绑定，拼写变动会静默破坏 overlay 模式。
//
//  2. SuppressForOverlay 必须覆盖 InjectPromptEntrypoint。
//     该字段与 InjectMemoryIndex 独立，未来可以只关闭 prompt 入口而保留底层 memory store。
//
// 现有覆盖（不重复）：
//   - TestResolveMemoryGateOverlaySuppressesIndexAndPrefetch
//     (config_test.go:418) - overlay short-circuit
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

// 未知 SessionFlag 不能触发降级。
// 这防止 gateFlagEnabled/gateFlagValue 被误改成 contains 或通配匹配。
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

// overlay 模式必须短路 prompt 入口。
// InjectPromptEntrypoint 现在跟随 InjectMemoryIndex，但仍是独立字段；claude_code overlay 下必须关闭。
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
