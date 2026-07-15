package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

// 7. 重复 TurnCompleted �?节点�?done：跳�?+ metric IdempotentSkipped�?
// （与 case 4 形式上重复但语义不同：case 4 �?race C（fallback），case 7
// 是同一 subscriber �?retry 链下重复收到事件）�?
func TestDAGSubscriber_DuplicateTurnCompleted_NodeAlreadyDone(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "done"}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-7", AgentID: "agent-d"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-7", true, ""))

	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 (duplicate event on terminal node)", len(flow.completeCalls))
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.IdempotentSkipped != 1 {
		t.Fatalf("IdempotentSkipped delta = %d, want 1", d.IdempotentSkipped)
	}
}

// 8. stop_helper 失败 �?DB 推进 done 成功，但 stop 报错：subscriber �?
// Warn log，不影响 DB 推进，不传错给上�?dispatcher�?
func TestDAGSubscriber_StopHelperFails_DoesNotPropagate(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "running"}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-8", AgentID: "agent-e"}}
	stop := &dagSubscriberStopSpy{stopErr: errors.New("simulated stop refused")}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	// �?panic / 不抛�?—�?函数无返值。完成后 DB 推进应已发生�?
	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-8", true, ""))

	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1 (DB advance must succeed even when stop fails)", len(flow.completeCalls))
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.CompleteDone != 1 {
		t.Fatalf("CompleteDone delta = %d, want 1", d.CompleteDone)
	}
}

// 9. CompleteNode �?4KB cap �?store �?validation err（用 generic error
// 模拟），metric CompleteSizeCapExceeded 已先 +1（subscriber 在调 SQL 之前
// �?size），随后 store err �?Warn log + �?panic�?
func TestDAGSubscriber_CompleteSizeCapExceeded(t *testing.T) {
	before := DAGSubscriberCounters()
	// 构造一�?> 4KB �?result jsonb（合�?JSON）�?
	hugePayload := `{"data":"` + strings.Repeat("x", 5000) + `"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "running"}}}
	flow := &dagSubscriberFlowSpy{completeErr: errors.New("simulated validation: result exceeds 4KB")}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-9", AgentID: "agent-f"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-9", true, hugePayload))

	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1 (size cap is metric-only in A1, still attempts SQL)", len(flow.completeCalls))
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.CompleteSizeCapExceeded != 1 {
		t.Fatalf("CompleteSizeCapExceeded delta = %d, want 1", d.CompleteSizeCapExceeded)
	}
	// CompleteDone must NOT advance because the store returned err.
	if d.CompleteDone != 0 {
		t.Fatalf("CompleteDone delta = %d, want 0 (store err)", d.CompleteDone)
	}
}

func TestDAGSubscriber_CompleteErrorEnqueuesDurableRetry(t *testing.T) {
	runID := int64(42)
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:  "dag-repair",
		NodeKey: "agent-done",
		RunID:   &runID,
		Status:  "running",
	}}}
	flow := &dagSubscriberFlowSpy{completeErr: errors.New("temporary store outage")}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-repair", AgentID: "agent-repair"}},
		&dagSubscriberStopSpy{},
	)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-repair", true, `{"summary":"done"}`))

	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	assertDAGSubscriberCompletionRetryEnqueued(t, flow, runID)
	if len(flow.failCalls) != 0 {
		t.Fatalf("failCalls = %d, want 0 when durable retry enqueue succeeds", len(flow.failCalls))
	}
}

func assertDAGSubscriberCompletionRetryEnqueued(t *testing.T, flow *dagSubscriberFlowSpy, runID int64) {
	t.Helper()
	if len(flow.enqueueCalls) != 1 {
		t.Fatalf("enqueueCalls = %d, want 1 durable retry wakeup", len(flow.enqueueCalls))
	}
	enqueued := flow.enqueueCalls[0]
	assertDAGSubscriberRetryIdentity(t, enqueued, runID)
	if got := string(enqueued.PromptPayload); got != `{"summary":"done"}` {
		t.Fatalf("retry payload = %s, want raw completion result", got)
	}
}

func assertDAGSubscriberRetryIdentity(t *testing.T, enqueued taskdag.EnqueueWakeupInput, runID int64) {
	t.Helper()
	if enqueued.WakeupKind != "turn_complete_retry" {
		t.Fatalf("wakeup kind = %q, want turn_complete_retry", enqueued.WakeupKind)
	}
	if enqueued.DagKey != "dag-repair" || enqueued.NodeKey != "agent-done" || enqueued.RunID != runID {
		t.Fatalf("enqueue identity = %+v, want dag-repair/agent-done run_id=%d", enqueued, runID)
	}
	if !strings.Contains(enqueued.IdempotencyKey, "turn-complete-retry") {
		t.Fatalf("idempotency key = %q, want turn-complete-retry marker", enqueued.IdempotencyKey)
	}
}

func TestDAGSubscriber_CompleteErrorRetryEnqueueFailureFailsNode(t *testing.T) {
	runID := int64(43)
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:  "dag-repair",
		NodeKey: "agent-done",
		RunID:   &runID,
		Status:  "running",
	}}}
	flow := &dagSubscriberFlowSpy{
		completeErr: errors.New("temporary store outage"),
		enqueueErr:  errors.New("insert retry wakeup failed"),
	}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-repair-fail", AgentID: "agent-repair"}},
		&dagSubscriberStopSpy{},
	)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-repair-fail", true, `{"summary":"done"}`))

	if len(flow.enqueueCalls) != 1 {
		t.Fatalf("enqueueCalls = %d, want one retry enqueue attempt", len(flow.enqueueCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want diagnostic terminal failure when retry enqueue fails", len(flow.failCalls))
	}
	if got := flow.failCalls[0].Reason; !strings.Contains(got, "turn.completed completion retry enqueue failed") ||
		!strings.Contains(got, "insert retry wakeup failed") ||
		!strings.Contains(got, "temporary store outage") {
		t.Fatalf("fail reason = %q, want retry enqueue and original complete error", got)
	}
	if !flow.failCalls[0].FailFast {
		t.Fatal("FailFast = false, want true for diagnostic terminal failure")
	}
}

// Additional companion case �?pgx.ErrNoRows from CompleteNode (race C SQL
// fence rejection) �?metric IdempotentSkipped, exercises §2.6 SQL fallback.
func TestDAGSubscriber_CompleteNodeReturnsNoRows_FenceRace(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "running"}}}
	flow := &dagSubscriberFlowSpy{completeErr: sql.ErrNoRows}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-fence", AgentID: "agent-fence"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-fence", true, ""))

	d := metricDelta(before, DAGSubscriberCounters())
	if d.IdempotentSkipped != 1 {
		t.Fatalf("IdempotentSkipped delta = %d, want 1 (pgx.ErrNoRows is SQL fence race)", d.IdempotentSkipped)
	}
}
