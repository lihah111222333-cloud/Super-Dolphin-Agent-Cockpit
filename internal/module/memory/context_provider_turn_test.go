package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

func TestMemoryContextProviderPrepareTurnInputsStartsWithoutTurnStartedEvent(t *testing.T) {
	cfg := &Config{Enabled: true, SkipIndex: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	provider := mustNewContextProvider(t, cfg)
	manager := NewPrefetchManager(root)
	started := make(chan struct{})
	manager.SetBuildManifestFunc(func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "project/commit-style.md"}}, nil
	})
	manager.SetFindRelevantFunc(func(ctx context.Context, _ string, _ []MemoryEntry) ([]MemoryEntry, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	provider.mu.Lock()
	provider.turnStateLocked("thread-1").manager = manager
	provider.mu.Unlock()

	dispatcher := event.NewDispatcher()
	var cancels []context.CancelFunc
	registerContextProviderSubscriptions(memorySubscriptionDeps{Dispatcher: dispatcher, ContextProvider: provider}, func(cancel context.CancelFunc) {
		cancels = append(cancels, cancel)
	})
	defer cancelSubscriptions(cancels)

	event.Publish(dispatcher, newTurnStarted("thread-1", "turn-1"))
	provider.mu.Lock()
	handle := provider.turnStateLocked("thread-1").handle
	provider.mu.Unlock()
	if handle != nil {
		t.Fatalf("TurnStarted unexpectedly started prefetch: %#v", handle)
	}

	inputs := provider.PrepareTurnInputs(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "commit preference")
	if len(inputs) != 0 {
		t.Fatalf("PrepareTurnInputs() = %#v, want non-blocking empty result while prefetch is pending", inputs)
	}
	handle = waitForPrefetchHandle(t, provider, "thread-1")
	waitForSignal(t, started)
	provider.onTurnTerminated("thread-1", "turn-1")
	waitForHandle(t, handle)
}

func TestMemoryContextProviderPrepareTurnInputsSearchesTranscriptWhenEnabled(t *testing.T) {
	cfg := &Config{Enabled: true, Features: MemoryFeatureFlags{SearchPastContext: true}}
	provider := mustNewContextProvider(t, cfg)
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	session := historyStubSession{history: []dto.Message{{
		Role:      "assistant",
		Content:   "Use concise imperative commit messages and mention the subsystem first when it helps reviewers.",
		Timestamp: now.Add(-2 * time.Hour),
	}}}
	inputs := provider.PrepareTurnInputs(context.Background(), session, contract.BuildCtx{
		SessionFlags: map[string]bool{"search_past_context": true},
	}, "thread-1", "commit messages")
	if len(inputs) != 1 {
		t.Fatalf("len(PrepareTurnInputs()) = %d, want 1 transcript snippet", len(inputs))
	}
	if inputs[0].Type != "filecontent" {
		t.Fatalf("input type = %q, want filecontent", inputs[0].Type)
	}
	for _, snippet := range []string{"Past context transcript", "commit messages"} {
		if !strings.Contains(strings.ToLower(inputs[0].Content), strings.ToLower(snippet)) {
			t.Fatalf("transcript content missing %q:\n%s", snippet, inputs[0].Content)
		}
	}
}

func TestMemoryContextProviderHistorySearchErrorDoesNotEnterProviderInputs(t *testing.T) {
	absPath := filepath.Join(t.TempDir(), "history-store.jsonl")
	want := errors.New("history store unavailable: " + absPath)
	cfg := &Config{Enabled: true, Features: MemoryFeatureFlags{SearchPastContext: true}}
	provider := mustNewContextProvider(t, cfg)
	payload := provider.PrepareTurnContext(context.Background(), historyStubSession{historyErr: want}, contract.BuildCtx{
		SessionFlags: map[string]bool{"search_past_context": true},
	}, "thread-1", "commit messages")
	if len(payload.Inputs) != 0 {
		t.Fatalf("PrepareTurnContext() history error inputs = %#v, want no provider-visible error input", payload.Inputs)
	}
	if providerInputsContain(payload.Inputs, absPath) {
		t.Fatalf("PrepareTurnContext() leaked absolute history path %q in inputs: %#v", absPath, payload.Inputs)
	}
}

func TestMemoryContextProviderPrefetchErrorDoesNotEnterProviderInputs(t *testing.T) {
	absPath := filepath.Join(t.TempDir(), "project", "commit-style.md")
	want := errors.New("prefetch finder unavailable: " + absPath)
	cfg := &Config{Enabled: true, SkipIndex: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	provider := mustNewContextProvider(t, cfg)
	manager := NewPrefetchManager(root)
	manager.SetBuildManifestFunc(func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "project/commit-style.md"}}, nil
	})
	manager.SetFindRelevantFunc(func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
		return nil, want
	})
	provider.mu.Lock()
	provider.turnStateLocked("thread-1").manager = manager
	provider.mu.Unlock()

	first := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "commit messages")
	if len(first.Attachments) != 0 || len(first.Inputs) != 0 {
		t.Fatalf("first PrepareTurnContext() = %#v, want pending empty payload", first)
	}
	handle := waitForPrefetchHandle(t, provider, "thread-1")
	waitForHandle(t, handle)

	payload := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "commit messages")
	if len(payload.Inputs) != 0 {
		t.Fatalf("PrepareTurnContext() prefetch error inputs = %#v, want no provider-visible error input", payload.Inputs)
	}
	if providerInputsContain(payload.Inputs, absPath) {
		t.Fatalf("PrepareTurnContext() leaked absolute prefetch path %q in inputs: %#v", absPath, payload.Inputs)
	}
}

func TestMemoryContextProviderPrepareTurnContextReturnsRelevantMemoryAttachments(t *testing.T) {
	cfg := &Config{Enabled: true, SkipIndex: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	provider := mustNewContextProvider(t, cfg)
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	provider.timeNow = func() time.Time { return now }
	manager := NewPrefetchManager(root)
	manager.SetBuildManifestFunc(func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "project/commit-style.md"}}, nil
	})
	manager.SetFindRelevantFunc(func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
		return []MemoryEntry{{
			FilePath:  "project/commit-style.md",
			Content:   "Use concise imperative commit messages.",
			UpdatedAt: now,
		}}, nil
	})
	provider.mu.Lock()
	provider.turnStateLocked("thread-1").manager = manager
	provider.mu.Unlock()

	first := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "commit messages")
	if len(first.Attachments) != 0 || len(first.Inputs) != 0 {
		t.Fatalf("first PrepareTurnContext() = %#v, want pending empty payload", first)
	}
	handle := waitForPrefetchHandle(t, provider, "thread-1")
	waitForHandle(t, handle)

	payload := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "commit messages")
	if len(payload.Attachments) != 1 {
		t.Fatalf("len(payload.Attachments) = %d, want 1", len(payload.Attachments))
	}
	attachment := payload.Attachments[0]
	if attachment.Kind != dto.AttachmentKindRelevantMemory {
		t.Fatalf("attachment kind = %q, want %q", attachment.Kind, dto.AttachmentKindRelevantMemory)
	}
	if attachment.Path != "project/commit-style.md" {
		t.Fatalf("attachment path = %q, want project/commit-style.md", attachment.Path)
	}
	if !strings.Contains(attachment.Header, "saved today") || !strings.Contains(attachment.Content, "concise imperative") {
		t.Fatalf("attachment = %#v, want freshness header + content", attachment)
	}
}

func TestGateRelevantPrefetchSurfacedBytesLedger(t *testing.T) {
	cfg := &Config{Enabled: true, SkipIndex: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	provider := mustNewContextProvider(t, cfg)
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	manager := NewPrefetchManager(root)
	calls := 0
	huge := strings.Repeat("x", defaultRelevantMemoryBudgetBytes)
	manager.SetBuildManifestFunc(func(string) ([]MemoryEntry, error) {
		return []MemoryEntry{{FilePath: "project/huge.md"}}, nil
	})
	manager.SetFindRelevantFunc(func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
		calls++
		return []MemoryEntry{{
			FilePath:  "project/huge.md",
			Content:   huge,
			UpdatedAt: now,
		}}, nil
	})
	provider.mu.Lock()
	provider.turnStateLocked("thread-1").manager = manager
	provider.mu.Unlock()

	first := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "review notes")
	if len(first.Attachments) != 0 || len(first.Inputs) != 0 {
		t.Fatalf("first PrepareTurnContext() = %#v, want pending empty payload", first)
	}
	handle := waitForPrefetchHandle(t, provider, "thread-1")
	waitForHandle(t, handle)

	payload := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "review notes")
	if len(payload.Attachments) != 1 {
		t.Fatalf("len(payload.Attachments) = %d, want 1", len(payload.Attachments))
	}
	provider.mu.Lock()
	state := provider.turnStateLocked("thread-1")
	surfacedBytes := state.surfacedBytes
	handle = state.handle
	provider.mu.Unlock()
	if surfacedBytes < defaultRelevantMemoryBudgetBytes {
		t.Fatalf("surfacedBytes = %d, want at least %d", surfacedBytes, defaultRelevantMemoryBudgetBytes)
	}
	if handle != nil {
		t.Fatalf("prefetch handle after consume = %#v, want nil", handle)
	}

	again := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "review notes")
	if len(again.Attachments) != 0 {
		t.Fatalf("second PrepareTurnContext() attachments = %#v, want no new retrieval after budget is exhausted", again.Attachments)
	}
	provider.mu.Lock()
	handle = provider.turnStateLocked("thread-1").handle
	provider.mu.Unlock()
	if handle != nil {
		t.Fatalf("prefetch restarted despite surfaced-byte budget: %#v", handle)
	}
	if calls != 1 {
		t.Fatalf("findRelevant calls = %d, want 1", calls)
	}
}

func TestMemoryContextProviderOnPromptInvalidateResetsSurfacedLedger(t *testing.T) {
	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	provider := mustNewContextProvider(t, cfg)
	entry := MemoryEntry{FilePath: "project/commit-style.md", Content: "Use concise imperative commit messages."}
	provider.mu.Lock()
	state := provider.turnStateLocked("thread-1")
	state.manager = NewPrefetchManager(root)
	state.manager.MarkSurfaced([]MemoryEntry{entry})
	state.surfacedBytes = len(entry.Content)
	provider.mu.Unlock()

	provider.OnPromptInvalidate(contract.InvalidateClear)
	if got := state.manager.FilterAlreadySurfaced([]MemoryEntry{entry}); len(got) != 1 {
		t.Fatalf("FilterAlreadySurfaced() after reset = %#v, want original entry", got)
	}
	if state.surfacedBytes != 0 {
		t.Fatalf("surfacedBytes after reset = %d, want 0", state.surfacedBytes)
	}
}

func TestMemoryHeaderUsesFreshnessLanguage(t *testing.T) {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	fresh := memoryHeader(now, MemoryEntry{FilePath: "project/today.md", UpdatedAt: now})
	if fresh != "Memory (saved today): project/today.md:" {
		t.Fatalf("fresh header = %q", fresh)
	}
	yesterday := memoryHeader(now, MemoryEntry{FilePath: "project/yesterday.md", UpdatedAt: now.Add(-24 * time.Hour)})
	if yesterday != "Memory (saved yesterday): project/yesterday.md:" {
		t.Fatalf("yesterday header = %q", yesterday)
	}
	stale := memoryHeader(now, MemoryEntry{FilePath: "project/stale.md", UpdatedAt: now.Add(-72 * time.Hour)})
	if !strings.Contains(stale, "saved 3 days ago") || !strings.Contains(stale, "Memory: project/stale.md:") {
		t.Fatalf("stale header = %q", stale)
	}
}

type historyStubSession struct {
	historyStubSessionIdentity
	historyStubSessionControl

	history    []dto.Message
	historyErr error
}

type historyStubSessionIdentity struct{}

func (historyStubSessionIdentity) ThreadID() string                { return "thread-1" }
func (historyStubSessionIdentity) RolloutPath() string             { return "" }
func (historyStubSessionIdentity) Capabilities() dto.CapabilitySet { return dto.CapabilitySet{} }

type historyStubSessionControl struct{}

func (historyStubSessionControl) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}
func (historyStubSessionControl) Interrupt(context.Context, dto.InterruptRequest) error { return nil }
func (historyStubSessionControl) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}
func (historyStubSessionControl) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, nil
}
func (historyStubSessionControl) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}
func (s historyStubSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	if s.historyErr != nil {
		return nil, s.historyErr
	}
	return append([]dto.Message(nil), s.history...), nil
}
func (historyStubSessionControl) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }
func (historyStubSessionControl) Close(context.Context) error                            { return nil }
func (historyStubSessionControl) ForceStop() error                                       { return nil }

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

func providerInputsContain(inputs []shareddto.InputItem, needle string) bool {
	for _, input := range inputs {
		if strings.Contains(input.Name, needle) || strings.Contains(input.Content, needle) {
			return true
		}
	}
	return false
}

func newTurnStarted(threadID, turnID string) turndto.TurnStarted {
	return turndto.TurnStarted{TurnHeader: shareddto.TurnHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID},
			AgentID:      "agent-1",
		},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
	}}
}
