package prompt

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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

func TestInputScopedSectionKeysAgentMemoryByScope(t *testing.T) {
	section := PromptSection{Name: DynamicSectionAgentMemory, CachePolicy: InputScoped}
	keyProject, ok := sectionInputCacheKey(section, SectionContext{Start: &StartInput{
		ParentAgentID:    "agent-root",
		AgentType:        "worker",
		AgentMemoryScope: "project",
	}})
	if !ok {
		t.Fatal("sectionInputCacheKey() cacheable = false, want true")
	}
	keyProject2, _ := sectionInputCacheKey(section, SectionContext{Start: &StartInput{
		ParentAgentID:    "agent-root",
		AgentType:        "worker",
		AgentMemoryScope: "project",
	}})
	keyLocal, _ := sectionInputCacheKey(section, SectionContext{Start: &StartInput{
		ParentAgentID:    "agent-root",
		AgentType:        "worker",
		AgentMemoryScope: "local",
	}})
	if keyProject != keyProject2 {
		t.Fatalf("same agent scope key mismatch: first=%q second=%q", keyProject, keyProject2)
	}
	if keyProject == keyLocal {
		t.Fatalf("different agent-memory scopes reused cache key: %q", keyProject)
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
