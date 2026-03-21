package uistate

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

func TestApplyTokensUpdatedPublishesThreadPatch(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil)
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

	svc, _, err := NewService(nil, nil, nil, nil)
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
