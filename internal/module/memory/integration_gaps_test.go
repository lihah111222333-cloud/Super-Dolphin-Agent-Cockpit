package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
		Provider:      NewRulesProvider(cfg, NewMemoryRuleEngine(), nil),
		AgentProvider: NewAgentMemoryPromptProvider(cfg, manager, nil),
	}); err != nil {
		t.Fatalf("registerPromptProviders() error = %v", err)
	}

	start, err := assembly.AssembleStart(context.Background(), prompt.StartInput{
		ParentAgentID:    "agent-root",
		AgentType:        "Worker",
		AgentMemoryScope: string(MemoryScopeProject),
		Name:             "Worker",
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

func TestRegisterPromptProvidersInjectsTeamMemoryIntoTurnUserContext(t *testing.T) {
	withTeamMemoryRuntimeReady(t, true)
	projectRoot := t.TempDir()
	autoRoot := filepath.Join(t.TempDir(), "automem")
	teamRoot := filepath.Join(projectRoot, teamMemoryRootDirName)
	for _, dir := range []string{autoRoot, teamRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	const teamBody = "- [Team](team.md) — shared memory"
	if err := os.WriteFile(memoryIndexPath(teamRoot), []byte(teamBody), 0o644); err != nil {
		t.Fatalf("WriteFile(team MEMORY.md) error = %v", err)
	}

	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	teamManager := NewTeamMemoryManager(cfg)
	assembly := prompt.NewService(&prompt.Config{}, nil)
	if err := registerPromptProviders(promptProviderParams{
		Registry:         assembly,
		PromptService:    assembly,
		ClaudeMdProvider: NewClaudeMdSourcesProvider(cfg, teamManager, nil),
	}); err != nil {
		t.Fatalf("registerPromptProviders() error = %v", err)
	}

	turn, err := assembly.AssembleTurn(context.Background(), prompt.TurnInput{
		ThreadID: "thread-1",
		GitRoot:  projectRoot,
		CWD:      projectRoot,
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	for _, snippet := range []string{
		"<team-memory-content source=\"shared\">",
		teamBody,
	} {
		if !strings.Contains(turn.UserContextText, snippet) {
			t.Fatalf("UserContextText missing %q:\n%s", snippet, turn.UserContextText)
		}
	}
}

func TestRegisterPromptProvidersSkipsTeamMemoryTurnLaneWhenKairosActive(t *testing.T) {
	withTeamMemoryRuntimeReady(t, true)
	projectRoot := t.TempDir()
	autoRoot := filepath.Join(t.TempDir(), "automem")
	teamRoot := filepath.Join(projectRoot, teamMemoryRootDirName)
	for _, dir := range []string{autoRoot, teamRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	if err := os.WriteFile(memoryIndexPath(teamRoot), []byte("- [Team](team.md) — shared memory"), 0o644); err != nil {
		t.Fatalf("WriteFile(team MEMORY.md) error = %v", err)
	}

	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true, Kairos: true},
	}
	assembly := prompt.NewService(&prompt.Config{}, nil)
	if err := registerPromptProviders(promptProviderParams{
		Registry:         assembly,
		PromptService:    assembly,
		ClaudeMdProvider: NewClaudeMdSourcesProvider(cfg, NewTeamMemoryManager(cfg), nil),
	}); err != nil {
		t.Fatalf("registerPromptProviders() error = %v", err)
	}

	turn, err := assembly.AssembleTurn(context.Background(), prompt.TurnInput{
		ThreadID:     "thread-2",
		GitRoot:      projectRoot,
		CWD:          projectRoot,
		SessionFlags: map[string]bool{"memory_kairos": true},
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(turn.UserContextText, "<team-memory-content source=\"shared\">") {
		t.Fatalf("UserContextText unexpectedly contains team memory under Kairos:\n%s", turn.UserContextText)
	}
	gate := ResolveMemoryGate(contract.BuildCtx{GitRoot: projectRoot, CWD: projectRoot, SessionFlags: map[string]bool{"memory_kairos": true}}, cfg)
	if !gate.KairosActive || gate.InjectTeamMemIndex {
		t.Fatalf("ResolveMemoryGate() = %+v, want KairosActive=true and InjectTeamMemIndex=false", gate)
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
