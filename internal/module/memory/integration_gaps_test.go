package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

func TestAgentMemoryInjectedOnSubAgentStart(t *testing.T) {
	t.Parallel()

	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: t.TempDir()}
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

func TestPrefetchStartsOnTurnAndConsumesBeforeProvider(t *testing.T) {
	t.Parallel()

	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: t.TempDir()}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	if err := os.WriteFile(memoryIndexPath(root), []byte("- [fallback](fallback.md) — old index"), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}

	provider := NewContextProvider(cfg)
	assembly := prompt.NewService(&prompt.Config{}, nil)
	if err := registerPromptProviders(promptProviderParams{Registry: assembly, ContextProvider: provider}); err != nil {
		t.Fatalf("registerPromptProviders() error = %v", err)
	}

	dispatcher := event.NewDispatcher()
	app := fx.New(
		fx.NopLogger,
		fx.Supply(dispatcher),
		fx.Supply(provider),
		fx.Invoke(registerMemoryHooks),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("app.Stop() error = %v", err)
		}
	})

	initialTurn, err := assembly.AssembleTurn(context.Background(), prompt.TurnInput{
		ThreadID: "thread-1",
		UserText: "commit preference",
	})
	if err != nil {
		t.Fatalf("initial AssembleTurn() error = %v", err)
	}
	if !strings.Contains(initialTurn.UserContextText, "Contents of MEMORY.md:") {
		t.Fatalf("initial UserContextText should fall back to MEMORY.md before prefetch:\n%s", initialTurn.UserContextText)
	}

	provider.mu.Lock()
	state := provider.turnStateLocked("thread-1")
	state.manager = NewPrefetchManager(root)
	state.manager.buildManifest = func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "project/commit-style.md"}}, nil
	}
	state.manager.findRelevant = func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
		return []MemoryEntry{{
			FilePath: "project/commit-style.md",
			Frontmatter: MemoryFrontmatter{
				Name:        "Commit style",
				Description: "Use concise imperative commit messages.",
			},
			Content: "Use concise imperative commit messages, and mention the subsystem first when it helps reviewers.",
		}}, nil
	}
	provider.mu.Unlock()

	event.Publish(dispatcher, newTurnStarted("thread-1", "turn-1"))
	handle := waitForPrefetchHandle(t, provider, "thread-1")
	waitForHandle(t, handle)

	turn, err := assembly.AssembleTurn(context.Background(), prompt.TurnInput{
		ThreadID: "thread-1",
		UserText: "continue",
	})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	for _, snippet := range []string{
		"# Relevant memory hints",
		"Commit style",
		"project/commit-style.md",
		"Use concise imperative commit messages",
	} {
		if !strings.Contains(turn.UserContextText, snippet) {
			t.Fatalf("UserContextText missing %q:\n%s", snippet, turn.UserContextText)
		}
	}
	if strings.Contains(turn.UserContextText, "Contents of MEMORY.md:") {
		t.Fatalf("UserContextText unexpectedly fell back to MEMORY.md after prefetch:\n%s", turn.UserContextText)
	}
	if state := handle.state.Load(); state != prefetchStateConsumed {
		t.Fatalf("prefetch handle state = %d, want consumed", state)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if state := provider.turnStateLocked("thread-1"); state.handle != nil {
		t.Fatalf("turn state handle = %#v, want consumed handle cleared", state.handle)
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

func waitForPrefetchHandle(t *testing.T, provider *MemoryContextProvider, threadID string) *PrefetchHandle {
	t.Helper()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		provider.mu.Lock()
		handle := provider.turnStateLocked(threadID).handle
		provider.mu.Unlock()
		if handle != nil {
			return handle
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for prefetch handle on %q", threadID)
		case <-ticker.C:
		}
	}
}
