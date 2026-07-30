package uistate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

func TestApplyTokensUpdatedPublishesThreadPatch(t *testing.T) {
	t.Parallel()

	svc, dispatcher := newPatchTestService(t)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", Name: "Demo", State: "running"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-main", ThreadID: "thread-1", State: "running"}}
	mustSetThreadPreference(t, svc, preferenceActiveThreadID, "thread-1")
	mustSetThreadPreference(t, svc, preferenceActiveCmdThreadID, "thread-cmd")
	mustSetThreadPreference(t, svc, preferenceMainAgentID, "agent-main")

	got := subscribeThreadPatch(t, dispatcher)

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

	assertTokensThreadPatch(t, mustReceiveThreadPatch(t, got))
}

func TestThreadPatchIncludesRuntimeAndSortTimestamps(t *testing.T) {
	t.Parallel()

	svc, _ := newPatchTestService(t)
	createdAt := time.Unix(1710000000, 123000000).UTC()
	updatedAt := time.Unix(1710000060, 0).UTC()
	svc.state.Threads = []ThreadSummary{{ID: "thread-relaunch", Name: "Relaunched", State: "running", CreatedAt: &createdAt, UpdatedAt: &updatedAt}}
	svc.state.Agents = []AgentSummary{{
		ID:               "agent-relaunch",
		ThreadID:         "thread-relaunch",
		State:            "running",
		AgentState:       "running",
		Provider:         "codex",
		ProviderThreadID: "provider-thread",
		CWD:              "/repo/current",
		CreatedAt:        &createdAt,
		UpdatedAt:        &updatedAt,
	}}

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-relaunch", "agent/launched")
	svc.mu.Unlock()

	if patch.Thread == nil || patch.Thread.CreatedAt == nil || !patch.Thread.CreatedAt.Equal(createdAt) {
		t.Fatalf("patch.Thread.CreatedAt = %#v, want %s", patch.Thread, createdAt)
	}
	if got, _ := patch.AgentRuntime["cwd"].(string); got != "/repo/current" {
		t.Fatalf("patch.AgentRuntime[cwd] = %q, want /repo/current; runtime=%#v", got, patch.AgentRuntime)
	}
	if got, _ := patch.AgentRuntime["providerThreadId"].(string); got != "provider-thread" {
		t.Fatalf("patch.AgentRuntime[providerThreadId] = %q, want provider-thread", got)
	}
}

func TestThreadPatchInterruptibleRequiresMatchingActiveTurn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		activeTurn *TurnSummary
		want       bool
	}{
		{
			name: "nil active turn",
			want: false,
		},
		{
			name:       "matching active turn",
			activeTurn: &TurnSummary{ID: "turn-1", ThreadID: "thread-1"},
			want:       true,
		},
		{
			name:       "different active turn thread",
			activeTurn: &TurnSummary{ID: "turn-1", ThreadID: "thread-2"},
			want:       false,
		},
		{
			name:       "empty active turn id",
			activeTurn: &TurnSummary{ThreadID: "thread-1"},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newPatchTestService(t)
			svc.state.Threads = []ThreadSummary{{ID: "thread-1", Name: "Demo", State: "running"}}
			svc.state.ActiveTurn = tt.activeTurn

			svc.mu.Lock()
			patch := svc.threadPatchLocked("thread-1", "test")
			svc.mu.Unlock()

			if patch.Interruptible == nil {
				t.Fatalf("patch.Interruptible = nil, want %v", tt.want)
			}
			if *patch.Interruptible != tt.want {
				t.Fatalf("patch.Interruptible = %v, want %v; patch=%#v", *patch.Interruptible, tt.want, patch)
			}
		})
	}
}

func TestThreadPatchActiveTurnContract(t *testing.T) {
	t.Parallel()

	svc, _ := newPatchTestService(t)
	startedAt := time.Unix(1710000100, 0).UTC()
	svc.state.Threads = []ThreadSummary{
		{ID: "thread-active", Name: "Active", State: "running"},
		{ID: "thread-other", Name: "Other", State: "running"},
	}
	svc.state.ActiveTurn = &TurnSummary{
		ID:        "turn-active",
		ThreadID:  "thread-active",
		AgentID:   "agent-active",
		Status:    "thinking",
		StartedAt: &startedAt,
	}

	svc.mu.Lock()
	matching := svc.threadPatchLocked("thread-active", "turn/started")
	mismatched := svc.threadPatchLocked("thread-other", "turn/started")
	svc.state.ActiveTurn = nil
	missing := svc.threadPatchLocked("thread-active", "turn/completed")
	svc.mu.Unlock()

	activeTurn := patchActiveTurnPayload(t, matching)
	if got, _ := activeTurn["id"].(string); got != "turn-active" {
		t.Fatalf("activeTurn.id = %q, want turn-active; payload=%#v", got, activeTurn)
	}
	if got, _ := activeTurn["threadId"].(string); got != "thread-active" {
		t.Fatalf("activeTurn.threadId = %q, want thread-active; payload=%#v", got, activeTurn)
	}
	if patchHasActiveTurn(t, mismatched) {
		t.Fatalf("mismatched thread patch leaked activeTurn: %#v", mismatched)
	}
	if patchHasActiveTurn(t, missing) {
		t.Fatalf("missing active turn patch leaked activeTurn: %#v", missing)
	}
}

func TestTurnCompletedPatchIncludesLastActiveAt(t *testing.T) {
	t.Parallel()

	svc, dispatcher := newPatchTestService(t)
	got := subscribeThreadPatch(t, dispatcher)
	startedAt := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(2 * time.Minute)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-completed", "agent-completed"), "turn-completed")

	turnHeader.Timestamp = startedAt
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnHeader})
	_ = mustReceiveThreadPatch(t, got)

	turnHeader.Timestamp = completedAt
	svc.applyTurnCompleted(canonicalPatchTurnCompleted(t, turndto.TurnCompleted{TurnHeader: turnHeader, Success: true, Status: "completed", Summary: "completed"}))
	patch := mustReceiveThreadPatch(t, got)

	if patch.Source != "turn/completed" || patch.Status != "idle" {
		t.Fatalf("completion patch status = %#v", patch)
	}
	if got, _ := patch.AgentMeta["lastActiveAt"].(string); got != completedAt.Format(time.RFC3339Nano) {
		t.Fatalf("patch.AgentMeta[lastActiveAt] = %q, want %s; meta=%#v", got, completedAt.Format(time.RFC3339Nano), patch.AgentMeta)
	}
}

func TestUIStateCanonicalFailureDoesNotBecomeCleanIdle(t *testing.T) {
	t.Parallel()

	svc, dispatcher := newPatchTestService(t)
	got := subscribeThreadPatch(t, dispatcher)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-errors", "agent-errors"), "turn-errors")
	svc.state.Threads = []ThreadSummary{{ID: "thread-errors", AgentID: "agent-errors", State: "running", ThreadStatus: "running"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-errors", ThreadID: "thread-errors", State: "running", ThreadStatus: "running"}}
	svc.state.ActiveTurn = &TurnSummary{ID: "turn-errors", AgentID: "agent-errors", ThreadID: "thread-errors", Status: "running"}

	svc.applyTurnCompleted(canonicalPatchTurnCompleted(t, turndto.TurnCompleted{
		TurnHeader: turnHeader,
		Success:    false,
		Status:     "failed",
		Error:      "tool call call-file/file failed",
	}))
	patch := mustReceiveThreadPatch(t, got)

	if patch.Status != "error" {
		t.Fatalf("completion patch status = %q, want error; patch=%#v", patch.Status, patch)
	}
	if len(svc.state.RecentTurns) != 1 {
		t.Fatalf("RecentTurns = %#v, want one failed turn", svc.state.RecentTurns)
	}
	recent := svc.state.RecentTurns[0]
	if recent.Status != "failed" || recent.Success == nil || *recent.Success || recent.Error != "Provider 未能完成本次执行。" {
		t.Fatalf("RecentTurns[0] = %#v, want canonical public failure", recent)
	}
	if svc.state.Threads[0].ThreadStatus != "error" || svc.state.Threads[0].State != "error" {
		t.Fatalf("Thread state = %#v, want visible error state", svc.state.Threads[0])
	}
}

func TestTurnOutputDeltaUpdatesLastMessageWithoutPublishingThreadPatch(t *testing.T) {
	t.Parallel()

	svc, dispatcher := newPatchTestService(t)
	got := subscribeThreadPatch(t, dispatcher)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stream", "agent-stream"), "turn-stream")
	svc.state.Threads = []ThreadSummary{{ID: "thread-stream", AgentID: "agent-stream"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-stream", ThreadID: "thread-stream"}}
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnHeader})
	_ = mustReceiveThreadPatch(t, got)

	svc.applyTurnOutputDelta(turndto.TurnOutputDelta{
		TurnHeader: turnHeader,
		Stream:     "message",
		Delta:      "hello world",
	})

	svc.mu.RLock()
	threadLastMessage := svc.state.Threads[0].LastMessage
	agentLastMessage := svc.state.Agents[0].LastMessage
	svc.mu.RUnlock()
	if threadLastMessage != "hello world" || agentLastMessage != "hello world" {
		t.Fatalf("last message thread=%q agent=%q, want hello world", threadLastMessage, agentLastMessage)
	}
	assertNoThreadPatch(t, got, "turn/outputDelta")

	svc.applyTurnCompleted(canonicalPatchTurnCompleted(t, turndto.TurnCompleted{TurnHeader: turnHeader, Success: true, Status: "completed", Summary: "completed"}))
	patch := mustReceiveThreadPatch(t, got)
	if patch.Source != "turn/completed" || patch.ThreadID != "thread-stream" {
		t.Fatalf("completion patch = %#v", patch)
	}
}

func patchActiveTurnPayload(t *testing.T, patch uidto.UIThreadPatch) map[string]any {
	t.Helper()
	var payload map[string]any
	data, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	raw, ok := payload["activeTurn"]
	if !ok {
		t.Fatalf("patch JSON missing activeTurn: %s", string(data))
	}
	if _, legacy := payload["active_turn"]; legacy {
		t.Fatalf("patch JSON used legacy active_turn key: %s", string(data))
	}
	activeTurn, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("activeTurn payload = %#v, want object; json=%s", raw, string(data))
	}
	return activeTurn
}

func patchHasActiveTurn(t *testing.T, patch uidto.UIThreadPatch) bool {
	t.Helper()
	var payload map[string]any
	data, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	_, ok := payload["activeTurn"]
	return ok
}

func assertNoThreadPatch(t *testing.T, got <-chan uidto.UIThreadPatch, source string) {
	t.Helper()
	select {
	case patch := <-got:
		t.Fatalf("unexpected thread patch for %s: %#v", source, patch)
	default:
	}
}

func newPatchTestService(t *testing.T) (*service, *event.Dispatcher) {
	t.Helper()
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)
	return svc, dispatcher
}

func mustSetThreadPreference(t *testing.T, svc *service, key, value string) {
	t.Helper()
	if err := svc.SetPreference(context.Background(), key, value); err != nil {
		t.Fatalf("SetPreference(%s) error = %v", key, err)
	}
}

func subscribeThreadPatch(t *testing.T, dispatcher *event.Dispatcher) <-chan uidto.UIThreadPatch {
	t.Helper()
	got := make(chan uidto.UIThreadPatch, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { got <- ev })
	t.Cleanup(cancel)
	return got
}

func assertTokensThreadPatch(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	assertTokensPatchIdentity(t, patch)
	assertTokensPatchSelection(t, patch)
	assertTokensPatchMetadata(t, patch)
}

func assertTokensPatchIdentity(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	if patch.ThreadID != "thread-1" || patch.Sequence != 1 {
		t.Fatalf("patch identity = %#v", patch)
	}
	if patch.Status != "running" || patch.TokenUsage == nil {
		t.Fatalf("patch payload = %#v", patch)
	}
}

func assertTokensPatchSelection(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	if patch.ActiveThreadID != "thread-1" || patch.ActiveCmdThreadID != "thread-cmd" {
		t.Fatalf("patch active selection = %#v", patch)
	}
}

func assertTokensPatchMetadata(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
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

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
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
	if first.Sequence != 1 || first.Generation != 1 {
		t.Fatalf("first patch sequence = %#v", first)
	}

	svc.applyThreadStopped(threaddto.Stopped{ThreadID: "thread-1"})
	stopped := mustReceiveThreadPatch(t, got)
	if stopped.ThreadID != "thread-1" || stopped.Sequence != 2 || stopped.Generation != 1 || stopped.Status != "idle" {
		t.Fatalf("stopped patch = %#v", stopped)
	}

	svc.mu.Lock()
	restarted := svc.threadPatchLocked("thread-1", "thread/started")
	svc.mu.Unlock()
	if restarted.Sequence != 1 || restarted.Generation != 2 {
		t.Fatalf("restarted patch sequence = %#v", restarted)
	}
}

func TestEmitThreadPatchEventPayloadTooLargeFallsBack(t *testing.T) {
	t.Parallel()

	svc, dispatcher := newPatchTestService(t)
	got := subscribeThreadPatch(t, dispatcher)

	svc.emitThreadPatchEvent(largeThreadPatch())
	assertLargeFallbackPatch(t, mustReceiveThreadPatch(t, got))
}

func TestEmitThreadPatchTraceCorrelatesByThread(t *testing.T) {
	t.Parallel()

	trace := seededUITraceService(t)
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := newObservedPatchTestService(t, dispatcher, trace)
	got := subscribeThreadPatch(t, dispatcher)

	svc.emitThreadPatchEvent(uidto.UIThreadPatch{ThreadID: "thread-1", Source: "turn/completed", Sequence: 1, Status: "running", DiffText: "secret diff payload"})
	_ = mustReceiveThreadPatch(t, got)

	result := trace.Query(context.Background(), observability.Query{TraceID: "trace-ui-1", Limit: 20})
	assertCorrelatedUIPatchTrace(t, result.Events)
}

func seededUITraceService(t *testing.T) *observability.Service {
	t.Helper()
	trace := newUITraceService()
	if err := trace.Record(context.Background(), observability.TraceEvent{TraceID: "trace-ui-1", SpanID: "turn-span-1", Kind: "turn.start", Method: "turn.start", ThreadID: "thread-1", Status: observability.StatusOK}); err != nil {
		t.Fatalf("seed trace event: %v", err)
	}
	return trace
}

func newObservedPatchTestService(t *testing.T, dispatcher *event.Dispatcher, trace *observability.Service) *service {
	t.Helper()
	svc, _, err := NewService(nil, nil, nil, nil, nil, nil, WithObservability(trace))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)
	return svc
}

func assertCorrelatedUIPatchTrace(t *testing.T, events []observability.TraceEvent) {
	t.Helper()
	uiEvents := filterUITraceEvents(events, "uistate.patch.emit")
	if len(uiEvents) == 0 {
		t.Fatalf("ui trace events for trace-ui-1 = 0, want at least 1: %#v", events)
	}
	traceEvent := lastUITraceEvent(uiEvents)
	if traceEvent.TraceID != "trace-ui-1" || traceEvent.ParentSpanID != "turn-span-1" || traceEvent.SpanID == "" {
		t.Fatalf("correlated ui trace identifiers = trace:%q span:%q parent:%q", traceEvent.TraceID, traceEvent.SpanID, traceEvent.ParentSpanID)
	}
	if traceEvent.Code.Line == 0 || traceEvent.Code.Function == "" {
		t.Fatalf("code anchor not concrete: %#v", traceEvent.Code)
	}
	if raw := traceEvent.Metadata; raw["diffText"] != nil || raw["diff_text"] != nil {
		t.Fatalf("ui trace metadata leaked payload: %#v", raw)
	}
}

func lastUITraceEvent(events []observability.TraceEvent) observability.TraceEvent {
	for n := len(events) - 1; n >= 0; n-- {
		if events[n].Status == observability.StatusOK {
			return events[n]
		}
	}
	return events[len(events)-1]
}

func largeThreadPatch() uidto.UIThreadPatch {
	interruptible := true
	return uidto.UIThreadPatch{
		ThreadID:        "thread-1",
		Source:          "tool/diffUpdated",
		Sequence:        7,
		Generation:      3,
		Status:          "running",
		StatusHeader:    "Running",
		StatusDetails:   "Applying large diff",
		Interruptible:   &interruptible,
		DiffText:        strings.Repeat("+payload\n", 9000),
		AgentMeta:       map[string]any{"agent": "agent-1", "blob": strings.Repeat("m", 2048)},
		ActivityStats:   &uidto.PatchActivityStats{ToolCalls: map[string]int64{"patch_edit": 1}},
		Alerts:          []uidto.PatchAlert{{ID: "alert-1", Time: "now", Level: "warn", Message: strings.Repeat("!", 2048)}},
		TimelineItems:   []uidto.PatchTimelineItem{{ID: "item-1", Kind: "tool", Text: strings.Repeat("x", 8192)}},
		RemovedItemIds:  []string{"old-1"},
		TimelineOrder:   []string{"item-1"},
		RefreshRequired: false,
	}
}

func assertLargeFallbackPatch(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	assertLargeFallbackIdentity(t, patch)
	assertLargeFallbackStatus(t, patch)
	assertLargeFallbackFields(t, patch)
}

func assertLargeFallbackIdentity(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	if patch.ThreadID != "thread-1" || patch.Source != "tool/diffUpdated" || patch.Sequence != 7 || patch.Generation != 3 {
		t.Fatalf("patch identity = %#v", patch)
	}
}

func assertLargeFallbackStatus(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	if patch.Status != "running" || patch.StatusHeader != "Running" || patch.StatusDetails != "Applying large diff" {
		t.Fatalf("patch status = %#v", patch)
	}
	if patch.Interruptible == nil || !*patch.Interruptible {
		t.Fatalf("patch interruptible = %#v", patch.Interruptible)
	}
	if !patch.Recover || !patch.RefreshRequired || patch.FallbackReason != "payload_too_large" {
		t.Fatalf("fallback flags = %#v", patch)
	}
}

func assertLargeFallbackFields(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
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

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
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

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
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

func TestAgentBoardSnapshotAndRealtimePatchStayConsistent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	outcome := &agentdto.Outcome{Kind: agentdto.OutcomeKindSuccess, Summary: "完成", CompletedAt: now}
	assignment := &agentdto.Assignment{Title: "看板契约", Description: "打通后端链路", AssignedAt: now.Add(-time.Hour)}
	agents := summarizeAgents([]contract.AgentSnapshot{{
		ID: "agent-1", ThreadID: "thread-1", ParentID: "agent-root", Name: "worker", State: "idle", UpdatedAt: now,
		Assignment: assignment, Progress: agentdto.Progress{Status: "idle", UpdatedAt: now}, Outcome: outcome,
	}})
	if len(agents) != 1 || agents[0].Assignment == nil || agents[0].Outcome == nil {
		t.Fatalf("summarizeAgents() = %#v, want complete Agent board snapshot", agents)
	}
	svc, _ := newPatchTestService(t)
	svc.state.Agents = agents
	patch := svc.threadPatchLocked("thread-1", "agent/completed")
	if patch.Agent == nil {
		t.Fatal("threadPatchLocked().Agent = nil")
	}
	if patch.Agent.ID != agents[0].ID || patch.Agent.ThreadID != agents[0].ThreadID || patch.Agent.ParentAgentID != agents[0].ParentAgentID || patch.Agent.Name != agents[0].Name {
		t.Fatalf("threadPatchLocked().Agent identity = %#v, snapshot = %#v", patch.Agent, agents[0])
	}
	if patch.Agent.Assignment.Title != assignment.Title || patch.Agent.Progress.Status != "idle" || patch.Agent.Outcome.Kind != agentdto.OutcomeKindSuccess {
		t.Fatalf("threadPatchLocked().Agent = %#v, want snapshot assignment/progress/outcome", patch.Agent)
	}
	if patch.Agent.Progress.CurrentStep != nil || patch.Agent.Progress.CompletedSteps != nil || patch.Agent.Progress.TotalSteps != nil {
		t.Fatalf("threadPatchLocked().Agent.Progress = %#v, want unavailable structured steps", patch.Agent.Progress)
	}
}

func canonicalPatchTurnCompleted(t *testing.T, completed turndto.TurnCompleted) turndto.TurnCompleted {
	t.Helper()
	terminal, err := turndto.NewTurnTerminalV2(completed, "patch-terminal-event")
	if err != nil {
		t.Fatalf("NewTurnTerminalV2() error = %v", err)
	}
	attached, err := turndto.AttachCanonicalTurnTerminal(completed, terminal)
	if err != nil {
		t.Fatalf("AttachCanonicalTurnTerminal() error = %v", err)
	}
	return attached
}
