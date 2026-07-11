package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

func TestWakeupDispatcher_TurnCompletionRetryCompletesNodeAndMarksSent(t *testing.T) {
	runID := int64(77)
	wakeup := turnCompletionRetryWakeupForTest(t, runID, `{"sharedfile":{"path":"reports/final.md"}}`)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{wakeup},
		completeReply: &taskdag.CompleteNodeWithDownstreamResult{
			Node: &taskdag.Node{DagKey: "dag-repair", NodeKey: "agent-done", RunID: &runID, Status: "done"},
		},
	}
	launcher := &dispatcherStubLauncher{}
	dispatcher, err := NewWakeupDispatcher(store, launcher, discardLogger(), WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher error = %v", err)
	}

	requireProcessBatchHandledOne(t, dispatcher)
	assertTurnCompletionRetryCompleteCall(t, store, runID, `{"sharedfile":{"path":"reports/final.md"}}`)
	assertTurnCompletionRetryMarkedSent(t, store)
	assertNoTurnCompletionRetryFailures(t, store)
	if len(launcher.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 for local repair wakeup", len(launcher.calls))
	}
}

func TestWakeupDispatcher_TurnCompletionRetryRetriesStoreFailureThenCompletes(t *testing.T) {
	runID := int64(78)
	wakeup := turnCompletionRetryWakeupForTest(t, runID, `{"summary":"done"}`)
	store := &dispatcherStubStore{
		claimReply:  []taskdag.Wakeup{wakeup},
		completeErr: errors.New("db temporarily unavailable"),
	}
	dispatcher, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, discardLogger(), WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher error = %v", err)
	}

	requireProcessBatchHandledOne(t, dispatcher)
	if len(store.completeCalls) != 1 {
		t.Fatalf("first completeCalls = %d, want 1", len(store.completeCalls))
	}
	assertTurnCompletionRetryRecordedRetry(t, store, "db temporarily unavailable")
	if len(store.markSentCalls) != 0 {
		t.Fatalf("markSentCalls = %d, want 0 before retry succeeds", len(store.markSentCalls))
	}

	store.completeErr = nil
	store.claimReply = []taskdag.Wakeup{wakeup}
	requireProcessBatchHandledOne(t, dispatcher)
	if len(store.completeCalls) != 2 {
		t.Fatalf("completeCalls = %d, want 2 after retry", len(store.completeCalls))
	}
	assertTurnCompletionRetryMarkedSent(t, store)
	if len(store.failCalls) != 0 || len(store.failNodeCalls) != 0 {
		t.Fatalf("fail wakeup/node calls = %d/%d, want 0/0", len(store.failCalls), len(store.failNodeCalls))
	}
}

func requireProcessBatchHandledOne(t *testing.T, dispatcher *WakeupDispatcher) {
	t.Helper()
	handled, err := dispatcher.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch error = %v", err)
	}
	if handled != 1 {
		t.Fatalf("handled = %d, want 1", handled)
	}
}

func assertTurnCompletionRetryCompleteCall(t *testing.T, store *dispatcherStubStore, runID int64, wantResult string) {
	t.Helper()
	if len(store.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(store.completeCalls))
	}
	got := store.completeCalls[0]
	if got.Status != "done" || got.DagKey != "dag-repair" || got.NodeKey != "agent-done" || got.RunID != runID {
		t.Fatalf("complete input = %+v, want done dag-repair/agent-done run_id=%d", got, runID)
	}
	if string(got.Result) != wantResult {
		t.Fatalf("complete result = %s, want %s", got.Result, wantResult)
	}
}

func assertTurnCompletionRetryMarkedSent(t *testing.T, store *dispatcherStubStore) {
	t.Helper()
	if len(store.markSentCalls) != 1 {
		t.Fatalf("markSentCalls = %d, want 1", len(store.markSentCalls))
	}
}

func assertNoTurnCompletionRetryFailures(t *testing.T, store *dispatcherStubStore) {
	t.Helper()
	if len(store.retryCalls) != 0 || len(store.failCalls) != 0 {
		t.Fatalf("retry/fail calls = %d/%d, want 0/0", len(store.retryCalls), len(store.failCalls))
	}
}

func assertTurnCompletionRetryRecordedRetry(t *testing.T, store *dispatcherStubStore, want string) {
	t.Helper()
	if len(store.retryCalls) != 1 {
		t.Fatalf("retryCalls = %d, want 1 after transient completion failure", len(store.retryCalls))
	}
	if !strings.Contains(store.retryCalls[0].LastError, want) {
		t.Fatalf("retry LastError = %q, want %q", store.retryCalls[0].LastError, want)
	}
}

func turnCompletionRetryWakeupForTest(t *testing.T, runID int64, result string) taskdag.Wakeup {
	t.Helper()
	claimedAt := time.Unix(100, 0).UTC()
	leaseAt := claimedAt.Add(time.Minute)
	return taskdag.Wakeup{
		ID:             99,
		DagKey:         "dag-repair",
		NodeKey:        "agent-done",
		RunID:          &runID,
		WakeupKind:     "turn_complete_retry",
		TargetAgentID:  "mcp-orch",
		PromptPayload:  []byte(result),
		Status:         "dispatching",
		ClaimedAt:      &claimedAt,
		ClaimedBy:      "dispatcher-test",
		LeaseExpiresAt: &leaseAt,
	}
}
