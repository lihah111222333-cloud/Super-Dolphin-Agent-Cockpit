package memory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestMemoryLifecycleHooksSkipHandledAutoMemoryWrites(t *testing.T) {
	root := newTestMemoryRoot(t)
	history := &mutableHistoryStub{
		messages: []providerdto.Message{{Role: "user", Content: "Please keep build commands guarded."}},
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, ExtractOnStop: true, RootDir: root},
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		history,
		threadLookupStub{thread: &contract.ThreadMetadata{
			ThreadID:       "thread-1",
			ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{"threadKind": "main"}),
		}},
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.extractFn = func(context.Context, string) (string, error) {
		t.Fatal("extractFn should not be called when auto memory write already happened")
		return "", nil
	}

	hooks.onTurnStarted(turnStartedEvent("thread-1", "turn-1"))
	hooks.onToolCallBegin(toolCallBeginEvent("thread-1", "turn-1", "call-1"))
	hooks.onToolDiffUpdated(tooldto.ToolDiffUpdated{
		ThreadID: "thread-1",
		CallID:   "call-1",
		Files:    []string{filepath.Join(root, "project", "saved.md")},
	})
	hooks.onTurnCompleted(context.Background(), turnCompletedEvent("thread-1", "turn-1"))

	waitForCursor(t, hooks, "thread-1", 1)
	entries, err := scanMemoryEntries(root)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("scanMemoryEntries() entries = %#v, want empty after skip", entries)
	}
}

func TestMemoryLifecycleHooksCoalescesPendingExtraction(t *testing.T) {
	root := newTestMemoryRoot(t)
	history := &mutableHistoryStub{
		messages: []providerdto.Message{{Role: "user", Content: "I prefer concise bullet points."}},
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, ExtractOnStop: true, RootDir: root},
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		history,
		threadLookupStub{thread: &contract.ThreadMetadata{
			ThreadID:       "thread-1",
			ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{"threadKind": "main"}),
		}},
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)

	started := make(chan int, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	hooks.extractFn = func(_ context.Context, prompt string) (string, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		started <- call
		if call == 1 {
			<-release
			return `{"memories":[{"content":"Prefer concise bullet points.","type":"user"}]}`, nil
		}
		if !strings.Contains(prompt, "Use guarded build commands") {
			t.Fatalf("second prompt missing latest transcript: %q", prompt)
		}
		return `{"memories":[{"content":"Use guarded build commands in this repo.","type":"project"}]}`, nil
	}

	hooks.onTurnCompleted(context.Background(), turnCompletedEvent("thread-1", "turn-1"))
	waitForStartedCall(t, started, 1)

	history.setMessages([]providerdto.Message{
		{Role: "user", Content: "I prefer concise bullet points."},
		{Role: "user", Content: "Use guarded build commands in this repo."},
	})
	hooks.onTurnCompleted(context.Background(), turnCompletedEvent("thread-1", "turn-2"))

	close(release)
	waitForStartedCall(t, started, 2)
	if err := hooks.DrainPendingExtraction(context.Background()); err != nil {
		t.Fatalf("DrainPendingExtraction() error = %v", err)
	}
	waitForCursor(t, hooks, "thread-1", 2)

	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("extractFn calls = %d, want 2", gotCalls)
	}
}

func TestMemoryLifecycleHooksFailureFreezesCursorUntilRetry(t *testing.T) {
	root := newTestMemoryRoot(t)
	history := &mutableHistoryStub{
		messages: []providerdto.Message{{Role: "user", Content: "Remember the first durable fact."}},
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, ExtractOnStop: true, RootDir: root},
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		history,
		threadLookupStub{thread: &contract.ThreadMetadata{
			ThreadID:       "thread-1",
			ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{"threadKind": "main"}),
		}},
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)

	started := make(chan int, 2)
	var mu sync.Mutex
	calls := 0
	prompts := make([]string, 0, 2)
	hooks.extractFn = func(_ context.Context, prompt string) (string, error) {
		mu.Lock()
		calls++
		call := calls
		prompts = append(prompts, prompt)
		mu.Unlock()
		started <- call
		if call == 1 {
			return "", io.ErrUnexpectedEOF
		}
		return `{"memories":[{"content":"Remember both durable facts.","type":"project"}]}`, nil
	}

	hooks.onTurnCompleted(context.Background(), turnCompletedEvent("thread-1", "turn-1"))
	waitForStartedCall(t, started, 1)

	failed := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := hooks.extractionState("thread-1")
		state.mu.Lock()
		cursor := state.cursor
		inProgress := state.inProgress
		lastError := state.lastError
		state.mu.Unlock()
		if cursor == 0 && !inProgress && lastError != "" {
			failed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !failed {
		t.Fatalf("expected failed extraction to freeze cursor: %s", hooks.debugExtractionState("thread-1"))
	}

	history.setMessages([]providerdto.Message{
		{Role: "user", Content: "Remember the first durable fact."},
		{Role: "user", Content: "Remember the second durable fact."},
	})
	hooks.onTurnCompleted(context.Background(), turnCompletedEvent("thread-1", "turn-2"))
	waitForStartedCall(t, started, 2)
	if err := hooks.DrainPendingExtraction(context.Background()); err != nil {
		t.Fatalf("DrainPendingExtraction() error = %v", err)
	}
	waitForCursor(t, hooks, "thread-1", 2)

	state := hooks.extractionState("thread-1")
	state.mu.Lock()
	lastError := state.lastError
	state.mu.Unlock()
	if lastError != "" {
		t.Fatalf("lastError = %q, want cleared after successful retry", lastError)
	}

	mu.Lock()
	gotCalls := calls
	gotPrompts := append([]string(nil), prompts...)
	mu.Unlock()
	if gotCalls != 2 {
		t.Fatalf("extractFn calls = %d, want 2", gotCalls)
	}
	if len(gotPrompts) != 2 {
		t.Fatalf("len(prompts) = %d, want 2", len(gotPrompts))
	}
	if !strings.Contains(gotPrompts[1], "Remember the first durable fact.") || !strings.Contains(gotPrompts[1], "Remember the second durable fact.") {
		t.Fatalf("retry prompt did not restart from original cursor:\n%s", gotPrompts[1])
	}
}

func TestMemoryLifecycleHooksAgentMemoryPathExclusionDoesNotSuppressExtraction(t *testing.T) {
	root := newTestMemoryRoot(t)
	history := &mutableHistoryStub{
		messages: []providerdto.Message{{Role: "user", Content: "Remember the worker-specific runbook."}},
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, ExtractOnStop: true, RootDir: root},
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		history,
		threadLookupStub{thread: &contract.ThreadMetadata{
			ThreadID:       "thread-1",
			ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{"threadKind": "main"}),
		}},
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	manager := NewAgentMemoryManager(&Config{RootDir: root})
	agentPath, err := manager.GetAgentMemoryEntrypoint("Worker", MemoryScopeUser)
	if err != nil {
		t.Fatalf("GetAgentMemoryEntrypoint() error = %v", err)
	}

	started := make(chan int, 1)
	var mu sync.Mutex
	calls := 0
	prompts := make([]string, 0, 1)
	hooks.extractFn = func(_ context.Context, prompt string) (string, error) {
		mu.Lock()
		calls++
		prompts = append(prompts, prompt)
		mu.Unlock()
		started <- 1
		return `{"memories":[{"content":"Remember the worker-specific runbook.","type":"project"}]}`, nil
	}

	hooks.onTurnStarted(turnStartedEvent("thread-1", "turn-1"))
	hooks.onToolCallBegin(toolCallBeginEvent("thread-1", "turn-1", "call-1"))
	hooks.onToolDiffUpdated(tooldto.ToolDiffUpdated{
		ThreadID: "thread-1",
		CallID:   "call-1",
		Files:    []string{agentPath},
	})
	hooks.onTurnCompleted(context.Background(), turnCompletedEvent("thread-1", "turn-1"))
	waitForStartedCall(t, started, 1)
	if err := hooks.DrainPendingExtraction(context.Background()); err != nil {
		t.Fatalf("DrainPendingExtraction() error = %v", err)
	}
	waitForCursor(t, hooks, "thread-1", 1)

	mu.Lock()
	gotCalls := calls
	gotPrompts := append([]string(nil), prompts...)
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("extractFn calls = %d, want 1", gotCalls)
	}
	if len(gotPrompts) != 1 || !strings.Contains(gotPrompts[0], "worker-specific runbook") {
		t.Fatalf("extract prompt = %#v, want transcript content", gotPrompts)
	}
}

func TestMemoryLifecycleHooksExtractAndSaveInvalidatesPromptSections(t *testing.T) {
	root := newTestMemoryRoot(t)
	invalidator := &sectionInvalidatorStub{}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, ExtractOnStop: true, RootDir: root},
		nil,
		nil,
		nil,
		nil,
		invalidator,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.extractFn = func(context.Context, string) (string, error) {
		return `{"memories":[{"content":"Keep diffs focused.","type":"feedback"}]}`, nil
	}

	err := hooks.ExtractAndSave(context.Background(), []providerdto.Message{{Role: "user", Content: "Keep diffs focused."}}, nil)
	if err != nil {
		t.Fatalf("ExtractAndSave() error = %v", err)
	}
	reason, gotNames := invalidator.snapshot()
	if reason != contract.InvalidateMemoryWrite {
		t.Fatalf("InvalidateSections() reason = %q, want %q", reason, contract.InvalidateMemoryWrite)
	}
	wantNames := []string{
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	}
	if len(gotNames) != len(wantNames) {
		t.Fatalf("InvalidateSections() names = %#v, want %#v", gotNames, wantNames)
	}
	for i, want := range wantNames {
		if gotNames[i] != want {
			t.Fatalf("InvalidateSections() names[%d] = %q, want %q", i, gotNames[i], want)
		}
	}
}

func TestMemoryLifecycleHooksExtractAndSaveHonorsSkipIndex(t *testing.T) {
	root := newTestMemoryRoot(t)
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, ExtractOnStop: true, SkipIndex: true, RootDir: root},
		nil,
		nil,
		nil,
		nil,
		nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.extractFn = func(context.Context, string) (string, error) {
		return `{"memories":[{"content":"Keep diffs focused.","type":"feedback"}]}`, nil
	}

	err := hooks.ExtractAndSave(context.Background(), []providerdto.Message{{Role: "user", Content: "Keep diffs focused."}}, nil)
	if err != nil {
		t.Fatalf("ExtractAndSave(skipIndex) error = %v", err)
	}
	if _, err := os.Stat(memoryIndexPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(MEMORY.md) error = %v, want %v", err, os.ErrNotExist)
	}
	entries, err := scanMemoryEntries(root)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 1", len(entries))
	}
}

func TestMemoryExtractorExtractWithoutFuncUsesInternalHeuristics(t *testing.T) {
	memories, err := NewMemoryExtractor().Extract(context.Background(), nil, ExtractParams{
		Transcript: []providerdto.Message{{Role: "user", Content: "I prefer concise bullet points for code reviews."}},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("len(memories) = %d, want 1", len(memories))
	}
	if memories[0].Type != MemoryTypeUser {
		t.Fatalf("Type = %q, want %q", memories[0].Type, MemoryTypeUser)
	}
}

type mutableHistoryStub struct {
	mu       sync.Mutex
	messages []providerdto.Message
}

func (s *mutableHistoryStub) ReadHistory(context.Context, string, int) ([]providerdto.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]providerdto.Message(nil), s.messages...), nil
}

func (s *mutableHistoryStub) setMessages(messages []providerdto.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append([]providerdto.Message(nil), messages...)
}

// sectionInvalidatorStub mirrors the concurrency contract that
// `contract.SectionInvalidator` declares (see prompt.go): test-time fan-out
// from background goroutines (auto-dream, extractor, turn-tracking) hits
// this stub without external synchronization, so the stub itself must be
// race-free. The mutex was added in Phase 2.0.1 after a code review noted
// the contract was overstated repo-wide.
type sectionInvalidatorStub struct {
	mu     sync.Mutex
	reason contract.InvalidateReason
	names  []string
}

func (s *sectionInvalidatorStub) InvalidateSections(reason contract.InvalidateReason, names ...string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reason = reason
	s.names = append([]string(nil), names...)
	return 1
}

// snapshot returns a copy of the captured state under the mutex; callers
// in tests can inspect it without re-locking.
func (s *sectionInvalidatorStub) snapshot() (contract.InvalidateReason, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason, append([]string(nil), s.names...)
}

func turnStartedEvent(threadID, turnID string) turndto.TurnStarted {
	return turndto.TurnStarted{TurnHeader: shareddto.TurnHeader{
		AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID}},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
	}}
}

func turnCompletedEvent(threadID, turnID string) turndto.TurnCompleted {
	return turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID}},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
		},
		Success: true,
	}
}

func toolCallBeginEvent(threadID, turnID, callID string) tooldto.ToolCallBegin {
	return tooldto.ToolCallBegin{
		ToolCallHeader: shareddto.ToolCallHeader{
			TurnHeader: shareddto.TurnHeader{
				AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID}},
				TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
			},
			CallID: callID,
		},
	}
}

func waitForCursor(t *testing.T, hooks *MemoryLifecycleHooks, threadID string, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := hooks.extractionState(threadID)
		state.mu.Lock()
		cursor := state.cursor
		inProgress := state.inProgress
		state.mu.Unlock()
		if cursor >= want && !inProgress {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("cursor for %s did not reach %d: %s", threadID, want, hooks.debugExtractionState(threadID))
}

func waitForStartedCall(t *testing.T, started <-chan int, want int) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("extractFn call order = %d, want %d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("extractFn did not reach call %d", want)
	}
}
