package uistate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

func TestApplyTokensUpdatedPublishesThreadPatch(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", Name: "Demo", State: "running"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-main", ThreadID: "thread-1", State: "running"}}
	if err := svc.SetPreference(context.Background(), preferenceActiveThreadID, "thread-1"); err != nil {
		t.Fatalf("SetPreference(activeThreadId) error = %v", err)
	}
	if err := svc.SetPreference(context.Background(), preferenceActiveCmdThreadID, "thread-cmd"); err != nil {
		t.Fatalf("SetPreference(activeCmdThreadId) error = %v", err)
	}
	if err := svc.SetPreference(context.Background(), preferenceMainAgentID, "agent-main"); err != nil {
		t.Fatalf("SetPreference(mainAgentId) error = %v", err)
	}

	got := make(chan uidto.UIThreadPatch, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { got <- ev })
	defer cancel()

	svc.applyTokensUpdated(uidto.UITokensUpdated{
		UITurnHeader: shared.UITurnHeader{
			UIProjectionHeader: shared.UIProjectionHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "thread-1"},
				Projection:   "thread",
			},
		},
		TotalTokens:         53,
		ContextWindowTokens: 200,
	})

	patch := mustReceiveThreadPatch(t, got)
	if patch.ThreadID != "thread-1" || patch.Sequence != 1 {
		t.Fatalf("patch identity = %#v", patch)
	}
	if patch.Status != "running" || patch.TokenUsage == nil {
		t.Fatalf("patch payload = %#v", patch)
	}
	if patch.ActiveThreadID != "thread-1" || patch.ActiveCmdThreadID != "thread-cmd" {
		t.Fatalf("patch active selection = %#v", patch)
	}
	if patch.MainAgentID != "agent-main" || patch.MainAgentState != "running" || !patch.Partial {
		t.Fatalf("patch metadata = %#v", patch)
	}
	if patch.TokenUsage.UsedTokens != 53 || patch.TokenUsage.ContextWindowTokens != 200 {
		t.Fatalf("patch token usage = %#v", patch.TokenUsage)
	}
}

func TestApplyThreadStoppedResetsPatchSequence(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)

	got := make(chan uidto.UIThreadPatch, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { got <- ev })
	defer cancel()

	svc.mu.Lock()
	first := svc.threadPatchLocked("thread-1", "turn/completed")
	svc.mu.Unlock()
	if first.Sequence != 1 {
		t.Fatalf("first patch sequence = %#v", first)
	}

	svc.applyThreadStopped(threaddto.Stopped{ThreadID: "thread-1"})
	stopped := mustReceiveThreadPatch(t, got)
	if stopped.ThreadID != "thread-1" || stopped.Sequence != 2 || stopped.Status != "idle" {
		t.Fatalf("stopped patch = %#v", stopped)
	}

	svc.mu.Lock()
	restarted := svc.threadPatchLocked("thread-1", "thread/started")
	svc.mu.Unlock()
	if restarted.Sequence != 1 {
		t.Fatalf("restarted patch sequence = %#v", restarted)
	}
}

func TestEmitThreadPatchEventPayloadTooLargeFallsBack(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)

	got := make(chan uidto.UIThreadPatch, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { got <- ev })
	defer cancel()

	interruptible := true
	svc.emitThreadPatchEvent(uidto.UIThreadPatch{
		ThreadID:        "thread-1",
		Source:          "tool/diffUpdated",
		Sequence:        7,
		Status:          "running",
		StatusHeader:    "Running",
		StatusDetails:   "Applying large diff",
		Interruptible:   &interruptible,
		DiffText:        strings.Repeat("+payload\n", 9000),
		AgentMeta:       map[string]any{"agent": "agent-1", "blob": strings.Repeat("m", 2048)},
		ActivityStats:   &uidto.PatchActivityStats{ToolCalls: map[string]int64{"lsp_edit": 1}},
		Alerts:          []uidto.PatchAlert{{ID: "alert-1", Time: "now", Level: "warn", Message: strings.Repeat("!", 2048)}},
		TimelineItems:   []uidto.PatchTimelineItem{{ID: "item-1", Kind: "tool", Text: strings.Repeat("x", 8192)}},
		RemovedItemIds:  []string{"old-1"},
		TimelineOrder:   []string{"item-1"},
		RefreshRequired: false,
	})

	patch := mustReceiveThreadPatch(t, got)
	if patch.ThreadID != "thread-1" || patch.Source != "tool/diffUpdated" || patch.Sequence != 7 {
		t.Fatalf("patch identity = %#v", patch)
	}
	if patch.Status != "running" || patch.StatusHeader != "Running" || patch.StatusDetails != "Applying large diff" {
		t.Fatalf("patch status = %#v", patch)
	}
	if patch.Interruptible == nil || !*patch.Interruptible {
		t.Fatalf("patch interruptible = %#v", patch.Interruptible)
	}
	if !patch.Recover || !patch.RefreshRequired || patch.FallbackReason != "payload_too_large" {
		t.Fatalf("fallback flags = %#v", patch)
	}
	if patch.DiffText != "" || patch.AgentMeta != nil || patch.ActivityStats != nil {
		t.Fatalf("expected heavy fields to be dropped, got %#v", patch)
	}
	if len(patch.Alerts) != 0 || len(patch.TimelineItems) != 0 || len(patch.RemovedItemIds) != 0 || len(patch.TimelineOrder) != 0 {
		t.Fatalf("expected timeline/alert fields to be dropped, got %#v", patch)
	}
}

func TestApplyToolDiffUpdatedPublishesDiffThreadPatch(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", AgentID: "agent-1", State: "running"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-1", ThreadID: "thread-1", State: "running"}}

	got := make(chan uidto.UIThreadPatch, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { got <- ev })
	defer cancel()

	svc.applyToolDiffUpdated(tooldto.ToolDiffUpdated{
		ThreadID: "thread-1",
		AgentID:  "agent-1",
		DiffText: "--- a/main.go\n+++ b/main.go\n@@\n-old\n+new\n",
	})

	patch := mustReceiveThreadPatch(t, got)
	if patch.ThreadID != "thread-1" || patch.DiffRevision != 1 {
		t.Fatalf("diff patch identity = %#v", patch)
	}
	if !strings.Contains(patch.DiffText, "+++ b/main.go") {
		t.Fatalf("diff patch text = %q, want unified diff", patch.DiffText)
	}
}

func TestGetStateOmitsInternalDiffMapsUnlessRequested(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", AgentID: "agent-1", State: "running"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-1", ThreadID: "thread-1", State: "running"}}
	svc.state.DiffTextByAgent = map[string]string{"agent-1": "diff-text"}
	svc.state.DiffRevisionByAgent = map[string]int64{"agent-1": 3}

	snapshot, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if len(snapshot.DiffTextByAgent) != 0 || len(snapshot.DiffRevisionByAgent) != 0 {
		t.Fatalf("GetState() leaked internal diff maps: %#v %#v", snapshot.DiffTextByAgent, snapshot.DiffRevisionByAgent)
	}

	diffSnapshot, err := svc.GetState(withDiffStateRequest(context.Background(), "thread-1", true, 0))
	if err != nil {
		t.Fatalf("GetState(includeDiff) error = %v", err)
	}
	if got := diffSnapshot.DiffTextByAgent["thread-1"]; got != "diff-text" {
		t.Fatalf("DiffTextByAgent[thread-1] = %q, want diff-text", got)
	}
	if got := diffSnapshot.DiffRevisionByAgent["thread-1"]; got != 3 {
		t.Fatalf("DiffRevisionByAgent[thread-1] = %d, want 3", got)
	}
}

func mustReceiveThreadPatch(t *testing.T, ch <-chan uidto.UIThreadPatch) uidto.UIThreadPatch {
	t.Helper()

	select {
	case patch := <-ch:
		return patch
	case <-time.After(time.Second):
		t.Fatal("expected thread patch")
		return uidto.UIThreadPatch{}
	}
}
