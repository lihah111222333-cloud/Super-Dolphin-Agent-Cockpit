package prompt

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestOutputStyleProviderRendersConfiguredPrompt(t *testing.T) {
	provider := OutputStyleProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{
		OutputStyleConfig: &contract.OutputStyleConfig{Name: "Explanatory", Prompt: "Explain the reasoning behind each change."},
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "# Output Style: Explanatory") || !strings.Contains(*text, "Explain the reasoning") {
		t.Fatalf("Resolve() = %v, want Claude-style output section", text)
	}
}

func TestOutputStyleProviderFallsBackToDescription(t *testing.T) {
	provider := OutputStyleProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{
		OutputStyleConfig: &contract.OutputStyleConfig{Name: "Terse", Description: "Answer with the final result first."},
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "Answer with the final result first.") {
		t.Fatalf("Resolve() = %v, want description fallback", text)
	}
}

func TestOutputStyleProviderSkipsNonRenderableConfig(t *testing.T) {
	provider := OutputStyleProvider{}
	keepCodingInstructions := false
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{
		OutputStyleConfig: &contract.OutputStyleConfig{
			Source:                 "user-config",
			KeepCodingInstructions: &keepCodingInstructions,
		},
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil for non-renderable config", *text)
	}
}

func TestScratchpadProviderIncludesSessionDirectory(t *testing.T) {
	provider := ScratchpadProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{ScratchpadDir: "/tmp/agent/scratchpad"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "/tmp/agent/scratchpad") || !strings.Contains(*text, "Only use `/tmp` if the user explicitly requests it.") {
		t.Fatalf("Resolve() = %v, want scratchpad guidance", text)
	}
}

func TestSummarizeToolResultsProviderReturnsConstantPrompt(t *testing.T) {
	provider := SummarizeToolResultsProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || *text != summarizeToolResultsSectionText {
		t.Fatalf("Resolve() = %v, want summarize_tool_results constant", text)
	}
}

func TestCacheByNameSectionKeysOutputStyleByConfig(t *testing.T) {
	section := PromptSection{Name: DynamicSectionOutputStyle, CachePolicy: CacheByName}
	keyA, ok := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{
		OutputStyleConfig: &contract.OutputStyleConfig{Name: "Explainer", Prompt: "Explain each step."},
	}})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyA2, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{
		OutputStyleConfig: &contract.OutputStyleConfig{Name: "Explainer", Prompt: "Explain each step."},
	}, Turn: &TurnInput{UserText: "different turn"}})
	keyB, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{
		OutputStyleConfig: &contract.OutputStyleConfig{Name: "Terse", Prompt: "Answer directly."},
	}})
	if keyA != keyA2 {
		t.Fatalf("same output style key mismatch: first=%q second=%q", keyA, keyA2)
	}
	if keyA == keyB {
		t.Fatalf("different output styles reused cache key: %q", keyA)
	}
}

func TestCacheByNameSectionKeysScratchpadByDirectory(t *testing.T) {
	section := PromptSection{Name: DynamicSectionScratchpad, CachePolicy: CacheByName}
	keyA, ok := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{ScratchpadDir: "/tmp/a"}})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyA2, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{ScratchpadDir: "/tmp/a"}, Turn: &TurnInput{UserText: "ignore noise"}})
	keyB, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{ScratchpadDir: "/tmp/b"}})
	if keyA != keyA2 {
		t.Fatalf("same scratchpad key mismatch: first=%q second=%q", keyA, keyA2)
	}
	if keyA == keyB {
		t.Fatalf("different scratchpads reused cache key: %q", keyA)
	}
}

func TestInputScopedSectionKeysMemoryByGateInputs(t *testing.T) {
	section := PromptSection{Name: DynamicSectionMemory, CachePolicy: InputScoped}
	base := SectionContext{
		Start: &StartInput{},
		BuildCtx: BuildCtx{
			CWD:                          "/repo",
			GitRoot:                      "/repo",
			SessionFlags:                 map[string]bool{"auto_memory_enabled": false},
			AdditionalWorkingDirectories: []string{"/repo/extra"},
		},
	}
	keyA, ok := sectionInputCacheKey(section, base)
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyA2, _ := sectionInputCacheKey(section, SectionContext{
		Start:    &StartInput{},
		Turn:     &TurnInput{UserText: "noise"},
		BuildCtx: base.BuildCtx,
	})
	keyB, _ := sectionInputCacheKey(section, SectionContext{
		Start: &StartInput{},
		BuildCtx: BuildCtx{
			CWD:                          "/repo",
			GitRoot:                      "/repo",
			SessionFlags:                 map[string]bool{"auto_memory_enabled": true},
			AdditionalWorkingDirectories: []string{"/repo/extra"},
		},
	})
	if keyA != keyA2 {
		t.Fatalf("same memory gate inputs key mismatch: first=%q second=%q", keyA, keyA2)
	}
	if keyA == keyB {
		t.Fatalf("different memory gate inputs reused cache key: %q", keyA)
	}
}

func TestInputScopedSectionKeysAvailableExpertsByPromptAndCurrentTemplate(t *testing.T) {
	section := PromptSection{Name: DynamicSectionAvailableExperts, CachePolicy: InputScoped}
	base := SectionContext{
		Start:    &StartInput{Prompt: "帮我做一个完整功能", PromptKey: "main/default"},
		BuildCtx: BuildCtx{CWD: "/repo"},
	}
	keyA, ok := sectionInputCacheKey(section, base)
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyA2, _ := sectionInputCacheKey(section, SectionContext{
		Start:    &StartInput{Prompt: "帮我做一个完整功能", PromptKey: "main/default"},
		BuildCtx: BuildCtx{CWD: "/repo"},
	})
	keyB, _ := sectionInputCacheKey(section, SectionContext{
		Start:    &StartInput{Prompt: "你好", PromptKey: "main/default"},
		BuildCtx: BuildCtx{CWD: "/repo"},
	})
	keyC, _ := sectionInputCacheKey(section, SectionContext{
		Start:    &StartInput{Prompt: "帮我做一个完整功能", PromptKey: "coder/prompt"},
		BuildCtx: BuildCtx{CWD: "/repo"},
	})
	keyD, _ := sectionInputCacheKey(section, SectionContext{
		Turn:     &TurnInput{UserText: "帮我做一个完整功能", PromptKey: "main/default"},
		BuildCtx: BuildCtx{CWD: "/repo"},
	})
	keyE, _ := sectionInputCacheKey(section, SectionContext{
		Turn:     &TurnInput{UserText: "帮我做一个完整功能", PromptKey: "coder/prompt"},
		BuildCtx: BuildCtx{CWD: "/repo"},
	})
	if keyA != keyA2 {
		t.Fatalf("same available_experts inputs key mismatch: first=%q second=%q", keyA, keyA2)
	}
	if keyA == keyB {
		t.Fatalf("different user prompts reused cache key: %q", keyA)
	}
	if keyA == keyC {
		t.Fatalf("different current prompt keys reused cache key: %q", keyA)
	}
	if keyD == keyE {
		t.Fatalf("different turn prompt keys reused cache key: %q", keyD)
	}
}

func TestInputScopedSectionKeysAvailableExpertsUsesStartAndTurnCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionAvailableExperts, CachePolicy: InputScoped}
	fromStart, ok := sectionInputCacheKey(section, SectionContext{
		Start: &StartInput{Prompt: "帮我做一个完整功能", CWD: "/repo/start"},
	})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	fromTurn, _ := sectionInputCacheKey(section, SectionContext{
		Turn: &TurnInput{UserText: "帮我做一个完整功能", CWD: "/repo/turn"},
	})
	fromBuild, _ := sectionInputCacheKey(section, SectionContext{
		BuildCtx: BuildCtx{CWD: "/repo/build"},
		Start:    &StartInput{Prompt: "帮我做一个完整功能", CWD: "/repo/start"},
	})
	if fromStart == fromTurn {
		t.Fatalf("start and turn cwd reused available_experts key: %q", fromStart)
	}
	if fromBuild == fromStart {
		t.Fatalf("BuildCtx cwd did not take precedence over Start cwd: %q", fromBuild)
	}
}

func TestInputScopedSectionKeysAvailableExpertsEmptyCWDDoesNotUseProcessCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionAvailableExperts, CachePolicy: InputScoped}
	keyA, ok := sectionInputCacheKey(section, SectionContext{
		Start: &StartInput{Prompt: "帮我做一个完整功能"},
	})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyB, _ := sectionInputCacheKey(section, SectionContext{
		Start:    &StartInput{Prompt: "帮我做一个完整功能"},
		BuildCtx: BuildCtx{CWD: "/repo/a"},
	})
	if keyA == keyB {
		t.Fatalf("empty cwd reused cwd-scoped available_experts key: %q", keyA)
	}
}

func TestRecallCatalogDynamicSpecOrderAndCachePolicy(t *testing.T) {
	spec, ok := dynamicSectionSpecForName(DynamicSectionRecallCatalog)
	if !ok {
		t.Fatalf("section %q missing from dynamic spec list", DynamicSectionRecallCatalog)
	}
	availableExperts, _ := dynamicSectionSpecForName(DynamicSectionAvailableExperts)
	memory, _ := dynamicSectionSpecForName(DynamicSectionMemory)
	if spec.order != 118 || spec.order <= availableExperts.order || spec.order >= memory.order {
		t.Fatalf("recall_catalog order = %d, want 118 between available_experts=%d and memory=%d",
			spec.order, availableExperts.order, memory.order)
	}
	if spec.cachePolicy != InputScoped {
		t.Fatalf("recall_catalog cache policy = %v, want InputScoped", spec.cachePolicy)
	}
}

func TestInputScopedSectionKeysRecallCatalogByCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionRecallCatalog, CachePolicy: InputScoped}
	keyA, ok := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{CWD: "/repo/a"}})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyA2, _ := sectionInputCacheKey(section, SectionContext{
		BuildCtx: BuildCtx{CWD: "/repo/a"},
		Turn:     &TurnInput{UserText: "ignore turn noise"},
	})
	keyB, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{CWD: "/repo/b"}})
	if keyA != keyA2 {
		t.Fatalf("same recall catalog cwd key mismatch: first=%q second=%q", keyA, keyA2)
	}
	if keyA == keyB {
		t.Fatalf("different recall catalog cwd reused cache key: %q", keyA)
	}
}

func TestInputScopedSectionKeysRecallCatalogUsesStartAndTurnCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionRecallCatalog, CachePolicy: InputScoped}
	fromStart, ok := sectionInputCacheKey(section, SectionContext{Start: &StartInput{CWD: "/repo/start"}})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	fromTurn, _ := sectionInputCacheKey(section, SectionContext{Turn: &TurnInput{CWD: "/repo/turn"}})
	fromBuild, _ := sectionInputCacheKey(section, SectionContext{
		BuildCtx: BuildCtx{CWD: "/repo/build"},
		Start:    &StartInput{CWD: "/repo/start"},
	})
	if fromStart == fromTurn {
		t.Fatalf("start and turn cwd reused recall_catalog key: %q", fromStart)
	}
	if fromBuild == fromStart {
		t.Fatalf("BuildCtx cwd did not take precedence over Start cwd: %q", fromBuild)
	}
}

func TestInputScopedSectionKeysRecallCatalogEmptyCWDDoesNotUseProcessCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionRecallCatalog, CachePolicy: InputScoped}
	keyA, ok := sectionInputCacheKey(section, SectionContext{})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyB, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{CWD: "/repo/a"}})
	if keyA == keyB {
		t.Fatalf("empty cwd reused cwd-scoped recall_catalog key: %q", keyA)
	}
}

func TestProjectDefaultRulesDynamicSpecOrderAndCachePolicy(t *testing.T) {
	spec, ok := dynamicSectionSpecForName(DynamicSectionProjectDefaultRules)
	if !ok {
		t.Fatalf("section %q missing from dynamic spec list", DynamicSectionProjectDefaultRules)
	}
	sessionGuidance, _ := dynamicSectionSpecForName(DynamicSectionSessionGuidance)
	availableExperts, _ := dynamicSectionSpecForName(DynamicSectionAvailableExperts)
	if spec.order != 112 || spec.order <= sessionGuidance.order || spec.order >= availableExperts.order {
		t.Fatalf("project_default_rules order = %d, want 112 between session_guidance=%d and available_experts=%d",
			spec.order, sessionGuidance.order, availableExperts.order)
	}
	if spec.cachePolicy != InputScoped {
		t.Fatalf("project_default_rules cache policy = %v, want InputScoped", spec.cachePolicy)
	}
}

func TestInputScopedSectionKeysProjectDefaultRulesByCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionProjectDefaultRules, CachePolicy: InputScoped}
	keyA, ok := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{CWD: "/repo/a"}})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyA2, _ := sectionInputCacheKey(section, SectionContext{
		BuildCtx: BuildCtx{CWD: "/repo/a"},
		Turn:     &TurnInput{UserText: "ignore turn noise"},
	})
	keyB, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{CWD: "/repo/b"}})
	if keyA != keyA2 {
		t.Fatalf("same project default rules cwd key mismatch: first=%q second=%q", keyA, keyA2)
	}
	if keyA == keyB {
		t.Fatalf("different project default rules cwd reused cache key: %q", keyA)
	}
}

func TestInputScopedSectionKeysProjectDefaultRulesUsesStartAndTurnCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionProjectDefaultRules, CachePolicy: InputScoped}
	fromStart, ok := sectionInputCacheKey(section, SectionContext{Start: &StartInput{CWD: "/repo/start"}})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	fromTurn, _ := sectionInputCacheKey(section, SectionContext{Turn: &TurnInput{CWD: "/repo/turn"}})
	fromBuild, _ := sectionInputCacheKey(section, SectionContext{
		BuildCtx: BuildCtx{CWD: "/repo/build"},
		Start:    &StartInput{CWD: "/repo/start"},
	})
	if fromStart == fromTurn {
		t.Fatalf("start and turn cwd reused project_default_rules key: %q", fromStart)
	}
	if fromBuild == fromStart {
		t.Fatalf("BuildCtx cwd did not take precedence over Start cwd: %q", fromBuild)
	}
}

func TestInputScopedSectionKeysProjectDefaultRulesEmptyCWDDoesNotUseProcessCWD(t *testing.T) {
	section := PromptSection{Name: DynamicSectionProjectDefaultRules, CachePolicy: InputScoped}
	keyA, ok := sectionInputCacheKey(section, SectionContext{})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyB, _ := sectionInputCacheKey(section, SectionContext{BuildCtx: BuildCtx{CWD: "/repo/a"}})
	if keyA == keyB {
		t.Fatalf("empty cwd reused cwd-scoped project_default_rules key: %q", keyA)
	}
}

func TestEveryInputScopedSectionAddsExplicitCacheDependencies(t *testing.T) {
	input := SectionContext{
		BuildCtx: BuildCtx{
			CWD:          "/repo/a",
			GitRoot:      "/repo/a",
			Language:     "zh",
			EnabledTools: []string{"agent_run", "mcp_lsp.grep"},
			SessionFlags: map[string]bool{"team_memory": true, "quiet": false},
		},
		Start: &StartInput{CWD: "/repo/start", Prompt: "start prompt", PromptKey: "start-key"},
		Turn:  &TurnInput{CWD: "/repo/turn", ThreadID: "thread-1", UserText: "turn text", PromptKey: "turn-key"},
	}
	for _, spec := range dynamicSectionSpecs {
		if spec.cachePolicy != InputScoped {
			continue
		}
		section := PromptSection{Name: spec.name, CachePolicy: InputScoped}
		raw, err := json.Marshal(inputScopedCacheDependency(section, input))
		if err != nil {
			t.Fatalf("marshal dependency for %s: %v", spec.name, err)
		}
		bare, err := json.Marshal(struct {
			Section string `json:"section"`
		}{Section: spec.name})
		if err != nil {
			t.Fatalf("marshal bare dependency for %s: %v", spec.name, err)
		}
		if string(raw) == string(bare) {
			t.Fatalf("input-scoped section %q uses bare section-only cache dependency", spec.name)
		}
	}
}

func TestInputScopedSectionKeysMemoryEntrypointByHarnessEnv(t *testing.T) {
	section := PromptSection{Name: DynamicSectionMemoryEntrypoint, CachePolicy: InputScoped}
	buildCtx := BuildCtx{CWD: "/repo", GitRoot: "/repo", SessionFlags: map[string]bool{"team_memory": true}}
	t.Setenv("MULTI_AGENT_HARNESS_CLI", "")
	keyA, ok := sectionInputCacheKey(section, SectionContext{Start: &StartInput{}, BuildCtx: buildCtx})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	t.Setenv("MULTI_AGENT_HARNESS_CLI", "claude_code")
	keyB, _ := sectionInputCacheKey(section, SectionContext{Start: &StartInput{}, BuildCtx: buildCtx})
	if keyA == keyB {
		t.Fatalf("different harness env reused cache key: %q", keyA)
	}
	if got := os.Getenv("MULTI_AGENT_HARNESS_CLI"); got != "claude_code" {
		t.Fatalf("env harness = %q, want claude_code", got)
	}
}
