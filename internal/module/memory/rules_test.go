package memory

import (
	"context"
	"os"
	"strings"
	"testing"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

func TestMemoryRuleEngineRulesForKnownTypes(t *testing.T) {
	engine := NewMemoryRuleEngine()
	for _, memoryType := range diskMemoryTypes {
		behavior, ok := engine.RulesForType(memoryType)
		if !ok {
			t.Fatalf("RulesForType(%q) missing", memoryType)
		}
		if strings.TrimSpace(behavior.Summary) == "" {
			t.Fatalf("RulesForType(%q) summary is empty", memoryType)
		}
		if len(behavior.Save) == 0 || len(behavior.Access) == 0 || len(behavior.Trust) == 0 {
			t.Fatalf("RulesForType(%q) has incomplete layers: %#v", memoryType, behavior)
		}
	}
}

func TestBuildMemoryLinesIncludesDeterministicCompleteSections(t *testing.T) {
	text := NewMemoryRuleEngine().BuildMemoryLines(MemoryRuleOptions{})
	orderedHeadings := []string{
		"### 1. memory system",
		"### 2. taxonomy",
		"### 3. exclusions",
		"### 4. save rules",
		"### 5. access rules",
		"### 6. trust rules",
		"### 7. memory vs plan/tasks",
		"### 8. searching past context",
	}
	last := -1
	for _, heading := range orderedHeadings {
		idx := strings.Index(text, heading)
		if idx == -1 {
			t.Fatalf("missing heading %q in prompt:\n%s", heading, text)
		}
		if idx <= last {
			t.Fatalf("heading %q is out of order in prompt", heading)
		}
		last = idx
	}
	for _, memoryType := range diskMemoryTypes {
		if got := strings.Count(text, "#### "+string(memoryType)); got != 3 {
			t.Fatalf("type heading %q count = %d, want 3 in save/access/trust sections", memoryType, got)
		}
	}
	for _, snippet := range []string{
		"sanitize + resolve + authorize",
		"ignore or not use memory",
		"`scope` is an ACL boundary",
		"local_unavailable",
		"Verify referenced functions and flags still exist",
		"V3 also re-checks referenced type names",
		"both what to avoid and what to keep doing",
		"who is doing what, why, or by when",
		"`git log` / `git blame` are authoritative",
		"what was surprising or non-obvious about it",
		"Standard mode is a two-step save",
		"Each durable fact belongs in its own topic file",
		"runtime retrieval work instead of prompt-level directory or transcript grep",
	} {
		if !strings.Contains(text, snippet) {
			t.Fatalf("prompt missing required snippet %q", snippet)
		}
	}
}

func TestBuildMemoryLinesSkipIndexAndExtraGuidelines(t *testing.T) {
	text := NewMemoryRuleEngine().BuildMemoryLines(MemoryRuleOptions{
		SkipIndex:       true,
		ExtraGuidelines: []string{"Keep explanations short.", "Prefer absolute dates in summaries."},
	})
	if !strings.Contains(text, "When `skipIndex` is enabled, write or update the topic file only") {
		t.Fatalf("skipIndex rule missing from prompt:\n%s", text)
	}
	extraIndex := strings.Index(text, "### 8. extra guidelines")
	if extraIndex == -1 {
		t.Fatalf("extra guidelines section missing from prompt:\n%s", text)
	}
	extra := text[extraIndex:]
	for _, snippet := range []string{"Keep explanations short.", "Prefer absolute dates in summaries."} {
		if !strings.Contains(extra, snippet) {
			t.Fatalf("extra guidelines section missing %q", snippet)
		}
	}
	searchIndex := strings.Index(text, "### 9. searching past context")
	if searchIndex == -1 {
		t.Fatalf("searching past context section missing from prompt:\n%s", text)
	}
	if searchIndex <= extraIndex {
		t.Fatalf("searching past context section should appear after extra guidelines")
	}
}

func TestMemoryRulesProviderRegistersStartOnlyDynamicSection(t *testing.T) {
	provider := NewRulesProvider(&Config{
		Enabled:         true,
		SkipIndex:       true,
		ExtraGuidelines: []string{"Keep explanations short."},
		Features: MemoryFeatureFlags{
			Kairos:            true,
			TeamMemory:        true,
			SearchPastContext: true,
		},
	}, NewMemoryRuleEngine())
	var dynamic prompt.DynamicSectionProvider = provider
	if dynamic.SectionName() != prompt.DynamicSectionMemory {
		t.Fatalf("SectionName() = %q, want %q", dynamic.SectionName(), prompt.DynamicSectionMemory)
	}

	svc := prompt.NewService(&prompt.Config{}, nil)
	if err := svc.RegisterDynamicProvider(dynamic); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	start, err := svc.AssembleStart(context.Background(), prompt.StartInput{})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if !strings.Contains(start.BaseInstructions, "### 1. memory system") {
		t.Fatalf("BaseInstructions missing rendered memory rules:\n%s", start.BaseInstructions)
	}
	for _, snippet := range []string{
		"### 2. taxonomy",
		"When `skipIndex` is enabled",
		"### 8. extra guidelines",
		"Keep explanations short.",
		"Feature flag `kairos` is enabled",
		"Feature flag `teammem` is enabled",
		"Feature flag `search_past_context` is enabled",
	} {
		if !strings.Contains(start.BaseInstructions, snippet) {
			t.Fatalf("BaseInstructions missing %q:\n%s", snippet, start.BaseInstructions)
		}
	}

	turn, err := svc.AssembleTurn(context.Background(), prompt.TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if strings.Contains(turn.UserContextText, "### 1. memory system") {
		t.Fatalf("UserContextText unexpectedly contains memory dynamic section:\n%s", turn.UserContextText)
	}
}

func TestChildAgentStartUsesDedicatedAgentMemoryPrompt(t *testing.T) {
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

	newAssembly := func() prompt.Service {
		svc := prompt.NewService(&prompt.Config{}, nil)
		if err := registerPromptProviders(promptProviderParams{
			Registry:      svc,
			Provider:      NewRulesProvider(cfg, NewMemoryRuleEngine()),
			AgentProvider: NewAgentMemoryPromptProvider(cfg, manager, nil),
		}); err != nil {
			t.Fatalf("registerPromptProviders() error = %v", err)
		}
		return svc
	}

	rootStart, err := newAssembly().AssembleStart(context.Background(), prompt.StartInput{})
	if err != nil {
		t.Fatalf("AssembleStart(root) error = %v", err)
	}
	if !strings.Contains(rootStart.BaseInstructions, "## "+prompt.DynamicSectionMemory) {
		t.Fatalf("root BaseInstructions missing standard memory section:\n%s", rootStart.BaseInstructions)
	}

	childStart, err := newAssembly().AssembleStart(context.Background(), prompt.StartInput{
		ParentAgentID: "agent-root",
		AgentType:     "Worker",
		Name:          "Worker",
	})
	if err != nil {
		t.Fatalf("AssembleStart(child) error = %v", err)
	}
	if !strings.Contains(childStart.BaseInstructions, body) {
		t.Fatalf("child BaseInstructions missing agent memory body:\n%s", childStart.BaseInstructions)
	}
	if strings.Contains(childStart.BaseInstructions, "## "+prompt.DynamicSectionMemory) {
		t.Fatalf("child BaseInstructions unexpectedly reused root memory rules:\n%s", childStart.BaseInstructions)
	}
}

func TestAgentMemoryPromptProviderEnsuresProjectScopeDir(t *testing.T) {
	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: t.TempDir()}
	manager := NewAgentMemoryManager(cfg)
	provider := NewAgentMemoryPromptProvider(cfg, manager, nil)

	text, err := provider.Resolve(context.Background(), prompt.SectionContext{Start: &prompt.StartInput{
		ParentAgentID: "agent-root",
		AgentType:     "Worker",
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, emptyAgentMemoryPrompt) {
		t.Fatalf("Resolve() = %#v, want empty agent-memory placeholder", text)
	}
	dir, err := manager.GetAgentMemoryDir("Worker", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir() error = %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Stat(%q) error = %v", dir, err)
	}
}

func TestMemoryContextProviderInjectsEntrypointIntoTurnUserContext(t *testing.T) {
	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: t.TempDir()}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	body := "- [Project preference](project-preference.md) — use concise commit messages."
	if err := os.WriteFile(memoryIndexPath(root), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}

	svc := prompt.NewService(&prompt.Config{}, nil)
	provider := NewContextProvider(cfg)
	if provider.SectionName() != prompt.DynamicSectionMemoryContext {
		t.Fatalf("SectionName() = %q, want %q", provider.SectionName(), prompt.DynamicSectionMemoryContext)
	}
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}

	turn, err := svc.AssembleTurn(context.Background(), prompt.TurnInput{})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	for _, snippet := range []string{"Contents of MEMORY.md:", body} {
		if !strings.Contains(turn.UserContextText, snippet) {
			t.Fatalf("UserContextText missing %q:\n%s", snippet, turn.UserContextText)
		}
	}

	start, err := svc.AssembleStart(context.Background(), prompt.StartInput{})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	if strings.Contains(start.BaseInstructions, body) {
		t.Fatalf("BaseInstructions unexpectedly contains MEMORY.md entrypoint:\n%s", start.BaseInstructions)
	}
}

func TestMemoryContextProviderPrefersPrefetchedRelevantEntries(t *testing.T) {
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
	provider.rememberTurnQuery("thread-1", "commit preference")
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

	provider.onTurnStarted(context.Background(), newTurnStarted("thread-1", "turn-1"))
	provider.mu.Lock()
	handle := provider.turnStateLocked("thread-1").handle
	provider.mu.Unlock()
	if handle == nil {
		t.Fatal("prefetch handle = nil, want started prefetch")
	}
	waitForHandle(t, handle)

	svc := prompt.NewService(&prompt.Config{}, nil)
	if err := svc.RegisterDynamicProvider(provider); err != nil {
		t.Fatalf("RegisterDynamicProvider() error = %v", err)
	}
	turn, err := svc.AssembleTurn(context.Background(), prompt.TurnInput{
		ThreadID: "thread-1",
		UserText: "continue",
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
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
		t.Fatalf("UserContextText unexpectedly fell back to MEMORY.md:\n%s", turn.UserContextText)
	}
}

func TestMemoryContextProviderClearsPrefetchStateOnTurnTermination(t *testing.T) {
	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: t.TempDir()}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	provider := NewContextProvider(cfg)
	provider.rememberTurnQuery("thread-2", "review notes")
	provider.mu.Lock()
	state := provider.turnStateLocked("thread-2")
	state.manager = NewPrefetchManager(root)
	provider.mu.Unlock()

	provider.onTurnStarted(context.Background(), newTurnStarted("thread-2", "turn-2"))
	provider.onTurnTerminated("thread-2", "turn-2")

	provider.mu.Lock()
	defer provider.mu.Unlock()
	state = provider.turnStateLocked("thread-2")
	if state.query != "" || state.turnID != "" || state.handle != nil {
		t.Fatalf("turn state = %#v, want cleared query/turn/handle", state)
	}
}

func newTurnStarted(threadID, turnID string) turndto.TurnStarted {
	return turndto.TurnStarted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
		},
	}
}
