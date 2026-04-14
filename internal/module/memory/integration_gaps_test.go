package memory

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

func TestAgentMemoryInjectedOnSubAgentStart(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Enabled: true, RootDir: t.TempDir(), ProjectRoot: t.TempDir(),
		Features: MemoryFeatureFlags{SearchPastContext: true},
	}
	manager := NewAgentMemoryManager(cfg)
	dir, err := manager.GetAgentMemoryDir("Worker", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir() error = %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	const body = "Remember the preferred verification workflow."
	if err := os.WriteFile(memoryIndexPath(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}

	assembly := prompt.NewService(&prompt.Config{}, nil)
	if err := registerPromptProviders(promptProviderParams{
		Registry:      assembly,
		Provider:      NewRulesProvider(cfg, NewMemoryRuleEngine()),
		AgentProvider: NewAgentMemoryPromptProvider(cfg, manager, nil),
	}); err != nil {
		t.Fatalf("registerPromptProviders() error = %v", err)
	}

	start, err := assembly.AssembleStart(context.Background(), prompt.StartInput{
		ParentAgentID: "agent-root",
		AgentType:     "Worker",
		Name:          "Worker",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	section, ok := findResolvedSection(start.ResolvedSections, prompt.DynamicSectionAgentMemory)
	if !ok {
		t.Fatalf("ResolvedSections missing %q: %#v", prompt.DynamicSectionAgentMemory, start.ResolvedSections)
	}
	if !strings.Contains(section.Content, body) {
		t.Fatalf("agent_memory section missing body:\n%s", section.Content)
	}
	if !strings.Contains(start.BaseInstructions, body) {
		t.Fatalf("BaseInstructions missing agent memory body:\n%s", start.BaseInstructions)
	}
	if _, ok := findResolvedSection(start.ResolvedSections, prompt.DynamicSectionMemory); ok {
		t.Fatalf("ResolvedSections unexpectedly include root %q section: %#v", prompt.DynamicSectionMemory, start.ResolvedSections)
	}
}

func findResolvedSection(sections []prompt.ResolvedPromptSection, name string) (prompt.ResolvedPromptSection, bool) {
	for _, section := range sections {
		if section.Name == name {
			return section, true
		}
	}
	return prompt.ResolvedPromptSection{}, false
}
