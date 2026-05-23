package taskdag

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func TestCompleteNodeAndScheduleDownstream_Sequential_AtoB_BtoC(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   completeDownstreamRunID,
		Status:  "done",
		Result:  json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "done" {
		t.Fatalf("complete A node = %+v", res.Node)
	}
	if got := scheduledKeys(res.ScheduledDownstream); !equalStrings(got, []string{"B"}) {
		t.Fatalf("after A scheduled = %v, want [B]", got)
	}
	// C must not be scheduled — depends_on=[B] still pending.
	if pendingForNode(db, "C") != 0 {
		t.Fatalf("C wakeup count = %d, want 0", pendingForNode(db, "C"))
	}
	// idempotency_key for B must follow plan convention.
	if want := downstreamIdempotencyKey("dag-1", "B", completeDownstreamRunID); res.ScheduledDownstream[0].IdempotencyKey != want {
		t.Fatalf("B idempotency_key = %q, want %s", res.ScheduledDownstream[0].IdempotencyKey, want)
	}
	// Payload must include upstream output path for A.
	bWakeup := lookupPendingWakeup(t, db, "B")
	assertUpstreamPayload(t, bWakeup, "agent-b", "A", "dag/dag-1/A/output.json")

	// Now mark B running (dispatcher would do this); complete B → C ready.
	transitionToRunning(t, db, "B")
	res2, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "B",
		RunID:   completeDownstreamRunID,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete B error = %v", err)
	}
	if got := scheduledKeys(res2.ScheduledDownstream); !equalStrings(got, []string{"C"}) {
		t.Fatalf("after B scheduled = %v, want [C]", got)
	}
}

func TestCompleteNodeAndScheduleDownstream_Parallel_FanOut(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   completeDownstreamRunID,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if got := scheduledKeys(res.ScheduledDownstream); !equalStrings(got, []string{"B", "C"}) {
		t.Fatalf("scheduled = %v, want [B C]", got)
	}
}

func TestCompleteNodeAndScheduleDownstream_Diamond_WaitsForAllUpstreams(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	transitionToRunning(t, db, "B")
	transitionToRunning(t, db, "C")

	resB, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "B", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete B error = %v", err)
	}
	if got := scheduledKeys(resB.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("after B alone scheduled = %v, want []", got)
	}

	resC, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := scheduledKeys(resC.ScheduledDownstream); !equalStrings(got, []string{"D"}) {
		t.Fatalf("after C scheduled = %v, want [D]", got)
	}
}

func TestCompleteNodeAndScheduleDownstream_PreExistingWakeupSkippedByIdempotencyKey(t *testing.T) {
	t.Parallel()

	// Seed a downstream wakeup row that already carries the idempotency
	// key the auto-scheduler would generate. CompleteNode-of-A must observe
	// the SQL ON CONFLICT path: zero new wakeup rows inserted, the result
	// slice empty, and the original wakeup unaffected.
	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	db.wakeupSeq = 1
	db.wakeups[1] = sqlc.TaskDagWakeup{
		ID:             1,
		DagKey:         "dag-1",
		NodeKey:        "B",
		WakeupKind:     "node_start",
		TargetAgentID:  "agent-b",
		PromptPayload:  []byte(`{"agent_id":"agent-b","note":"pre-seeded"}`),
		RunID:          completeDownstreamRunIDValue(),
		IdempotencyKey: downstreamIdempotencyKey("dag-1", "B", completeDownstreamRunID),
		Status:         "pending",
		NextRetryAt:    timestamptzValue(now),
		CreatedAt:      timestamptzValue(now),
		UpdatedAt:      timestamptzValue(now),
	}

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "done" {
		t.Fatalf("complete A node = %+v", res.Node)
	}
	if len(res.ScheduledDownstream) != 0 {
		t.Fatalf("scheduled = %v, want [] (pre-existing wakeup must dedupe)", res.ScheduledDownstream)
	}
	if pendingForNode(db, "B") != 1 {
		t.Fatalf("B wakeup count = %d, want 1 (no duplicate inserted)", pendingForNode(db, "B"))
	}
	// Original payload must not be overwritten by the conflicting INSERT.
	if w := db.wakeups[1]; string(w.PromptPayload) != `{"agent_id":"agent-b","note":"pre-seeded"}` {
		t.Fatalf("payload overwritten: %s", string(w.PromptPayload))
	}
}

func TestCompleteNodeAndScheduleDownstream_SecondCompleteOnDoneNodeIsNoRowsError(t *testing.T) {
	t.Parallel()

	// Calling CompleteNode twice on the same upstream node hits the SQL
	// status-IN ('ready','running','awaiting_verify') fence on the second
	// call (ADR-017 v1.2 §2.3 扩后仍不含 'done')。
	// The expected behaviour is the underlying not-found error surfaces;
	// the tx is short-circuited so no downstream scheduling runs and no
	// duplicate wakeup row is created.
	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("first complete error = %v", err)
	}
	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("second complete error = nil, want fence rejection")
	}
	if pendingForNode(db, "B") != 1 {
		t.Fatalf("B wakeup count = %d, want 1", pendingForNode(db, "B"))
	}
}

func TestCompleteNodeAndScheduleDownstream_ConcurrentUpstreamsConvergeOnSameDownstream(t *testing.T) {
	t.Parallel()

	// Two upstreams complete and the SECOND one's enqueue must hit the
	// idempotency-key conflict (B was already queued by the first), so the
	// returned ScheduledDownstream slice excludes it.
	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A1", deps: nil, status: "running", agent: "agent-a1"},
		{key: "A2", deps: nil, status: "running", agent: "agent-a2"},
		{key: "B", deps: []string{"A1", "A2"}, status: "pending", agent: "agent-b"},
	})

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A1", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete A1 error = %v", err)
	}
	// A1 alone shouldn't enqueue B (A2 still pending → deps unsatisfied).
	if pendingForNode(db, "B") != 0 {
		t.Fatalf("after A1 only B count = %d, want 0", pendingForNode(db, "B"))
	}
	// Complete A2 → B becomes ready and is enqueued exactly once.
	resA2, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A2", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A2 error = %v", err)
	}
	if got := scheduledKeys(resA2.ScheduledDownstream); !equalStrings(got, []string{"B"}) {
		t.Fatalf("after A2 scheduled = %v, want [B]", got)
	}
	if pendingForNode(db, "B") != 1 {
		t.Fatalf("B wakeup count = %d, want 1", pendingForNode(db, "B"))
	}
}

// TestCompleteNodeAndScheduleDownstream_SkipsWakeupForUnassignedNode
// 验证 F6.4 + F6.3 协作：下游节点 assigned_to 为空时
//   - F6.4：store 层不 enqueue wakeup（否则 dispatcher 调 LaunchAgent 会因
//     "agent id is required" 失败、retry 耗尽后让节点 permanent failed）
//   - F6.3：状态机仍然把 pending → ready 推进（依赖满足的真相），
//     等外部 agent / 人工接管补 assigned_to 后直接进 dispatcher。
//
// EN: When a downstream node has dependencies satisfied but no assigned_to:
//   - F6.4: skip the wakeup enqueue (avoid LaunchAgent failure cascade)
//   - F6.3: still promote pending → ready (state-machine truth);
//     external / manual flow can later inject assigned_to and resume.
func TestCompleteNodeAndScheduleDownstream_SkipsWakeupForUnassignedNode(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: ""},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   completeDownstreamRunID,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "done" {
		t.Fatalf("complete A node = %+v", res.Node)
	}
	// 关键断言 1（F6.4）：B 缺 assigned_to → ScheduledDownstream 必须为空。
	if got := scheduledKeys(res.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("scheduled = %v, want [] (B has empty assigned_to)", got)
	}
	// 关键断言 2（F6.4）：表里也不应该有任何 B 的 pending wakeup 行。
	if c := pendingForNode(db, "B"); c != 0 {
		t.Fatalf("B wakeup count = %d, want 0 (unassigned must skip enqueue)", c)
	}
	// 关键断言 3（F6.3）：B 节点状态被 promote 到 ready（即使 unassigned）。
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (F6.3 promote pending→ready)", got)
	}
	// 关键断言 4（F6.3）：PromotedDownstream 应包含 B（状态机真相 vs F6.4 路由）。
	if len(res.PromotedDownstream) != 1 || res.PromotedDownstream[0].NodeKey != "B" {
		t.Fatalf("PromotedDownstream = %+v, want [{dag-1 B}]", res.PromotedDownstream)
	}
}

// TestCompleteNodeAndScheduleDownstream_SkipsWakeupForWhitespaceAssignedTo
// F6.4 补强（防回归 Nit 1）：assigned_to 为纯空白字符串（如 "   "）必须等价于空，
// store 层经 strings.TrimSpace 守护后同样跳过 enqueue。若未来有人改成
// `agentID := cand.AssignedTo`（去掉 TrimSpace），此测试将失败 — 钉死该守护。
//
// EN: A whitespace-only assigned_to (e.g. "   ") must be treated like empty.
// The store applies strings.TrimSpace before checking, so the wakeup enqueue
// is skipped. This pins the TrimSpace guard against regressions that drop it.
func TestCompleteNodeAndScheduleDownstream_SkipsWakeupForWhitespaceAssignedTo(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "   "},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   completeDownstreamRunID,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "done" {
		t.Fatalf("complete A node = %+v", res.Node)
	}
	// 关键断言 1（F6.4）：B 的 assigned_to 为纯空白，TrimSpace 后为空 → ScheduledDownstream 必须为空。
	if got := scheduledKeys(res.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("scheduled = %v, want [] (B assigned_to is whitespace-only)", got)
	}
	// 关键断言 2（F6.4）：DB 也不应有 B 的 pending wakeup 行。
	if c := pendingForNode(db, "B"); c != 0 {
		t.Fatalf("B wakeup count = %d, want 0 (whitespace assigned_to must skip enqueue)", c)
	}
	// 关键断言 3（F6.3）：B 状态被 promote 到 ready（无论 assigned_to 是否空白）。
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (F6.3 promote pending→ready)", got)
	}
}

// TestCompleteNodeAndScheduleDownstream_MixedAssignmentFanOut
// 边界场景：扇出下游里有的有 assigned_to、有的没有 — 有的必须 enqueue、
// 没的必须跳过，互不影响。
//
// EN: Mixed fan-out — only downstream nodes carrying an assigned_to get a
// wakeup; unassigned siblings are skipped without affecting their peers.
func TestCompleteNodeAndScheduleDownstream_MixedAssignmentFanOut(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: ""},
		{key: "D", deps: []string{"A"}, status: "pending", agent: "agent-d"},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   completeDownstreamRunID,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if got := scheduledKeys(res.ScheduledDownstream); !equalStrings(got, []string{"B", "D"}) {
		t.Fatalf("scheduled = %v, want [B D] (C must be skipped)", got)
	}
	if c := pendingForNode(db, "B"); c != 1 {
		t.Fatalf("B wakeup count = %d, want 1", c)
	}
	if c := pendingForNode(db, "C"); c != 0 {
		t.Fatalf("C wakeup count = %d, want 0 (unassigned)", c)
	}
	if c := pendingForNode(db, "D"); c != 1 {
		t.Fatalf("D wakeup count = %d, want 1", c)
	}
}

// TestCompleteNodeAndScheduleDownstream_F63_PromotesPendingToReadyWithAssignedTo
// F6.3 核心场景：A done 后单个有 assigned_to 的下游 B
//   - status: pending → ready（PromoteSingleNodePendingToReady）
//   - PromotedDownstream 中含 B
//   - ScheduledDownstream 中含 B（F6.4 路由不跳过非空 assigned_to）
func TestCompleteNodeAndScheduleDownstream_F63_PromotesPendingToReadyWithAssignedTo(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (F6.3 promote)", got)
	}
	if keys := promotedKeys(res.PromotedDownstream); !equalStrings(keys, []string{"B"}) {
		t.Fatalf("PromotedDownstream = %v, want [B]", keys)
	}
	if keys := scheduledKeys(res.ScheduledDownstream); !equalStrings(keys, []string{"B"}) {
		t.Fatalf("ScheduledDownstream = %v, want [B]", keys)
	}
}

// TestCompleteNodeAndScheduleDownstream_F63_DiamondPartialUpstreamNoPromote
// F6.3 核心场景：多依赖节点上游只部分完成时未 promote。
// Diamond A → (B, C) → D：B done 后单独不能让 D promote，仅在 C done 后两个
// 依赖都满足 D 才 ready。
func TestCompleteNodeAndScheduleDownstream_F63_DiamondPartialUpstreamNoPromote(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	// 1) A done → promote B, C 但不 promote D。
	resA, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if keys := promotedKeys(resA.PromotedDownstream); !equalStrings(keys, []string{"B", "C"}) {
		t.Fatalf("after A promoted = %v, want [B C]", keys)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "D", completeDownstreamRunID)].Status; got != "pending" {
		t.Fatalf("after A: D status = %q, want pending (deps B,C still pending)", got)
	}

	// 2) 调度上上 B 进运行后 done，D 仍不能 promote（C 未 done）。
	transitionToRunning(t, db, "B")
	resB, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "B", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete B error = %v", err)
	}
	if keys := promotedKeys(resB.PromotedDownstream); len(keys) != 0 {
		t.Fatalf("after B alone promoted = %v, want [] (D deps not fully satisfied)", keys)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "D", completeDownstreamRunID)].Status; got != "pending" {
		t.Fatalf("after B: D status = %q, want pending", got)
	}

	// 3) C done → D 依赖全部满足 → promote。
	transitionToRunning(t, db, "C")
	resC, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if keys := promotedKeys(resC.PromotedDownstream); !equalStrings(keys, []string{"D"}) {
		t.Fatalf("after C promoted = %v, want [D]", keys)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "D", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("after C: D status = %q, want ready", got)
	}
}

// TestCompleteNodeAndScheduleDownstream_F63_ChainAToBToC
// F6.3 链式场景：A → B → C。
// • A done → B promote。C 不动（依赖 B 还未 done）。
// • B done → C promote。
func TestCompleteNodeAndScheduleDownstream_F63_ChainAToBToC(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	resA, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if keys := promotedKeys(resA.PromotedDownstream); !equalStrings(keys, []string{"B"}) {
		t.Fatalf("after A promoted = %v, want [B]", keys)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("after A: B status = %q, want ready", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "C", completeDownstreamRunID)].Status; got != "pending" {
		t.Fatalf("after A: C status = %q, want pending (B not done yet)", got)
	}

	transitionToRunning(t, db, "B")
	resB, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "B", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete B error = %v", err)
	}
	if keys := promotedKeys(resB.PromotedDownstream); !equalStrings(keys, []string{"C"}) {
		t.Fatalf("after B promoted = %v, want [C]", keys)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "C", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("after B: C status = %q, want ready", got)
	}
}

// TestCompleteNodeAndScheduleDownstream_F63_PromoteEvenWithoutAssignedTo
// F6.3 创新项：无 assigned_to 的下游仍然 promote（F6.4 仅跳 wakeup）。
// 本测试钉死河际划分：promote 不滤掉 assigned_to。
//
// EN: This test pins F6.3 × F6.4 division — promote (state-machine truth) is
// independent of assigned_to. F6.4's empty-assigned_to skip only affects
// wakeup enqueue, never the promote step.
func TestCompleteNodeAndScheduleDownstream_F63_PromoteEvenWithoutAssignedTo(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: ""},
		{key: "D", deps: []string{"A"}, status: "pending", agent: "   "}, // whitespace
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	// F6.3: 三个下游都被 promote。
	if keys := promotedKeys(res.PromotedDownstream); !equalStrings(keys, []string{"B", "C", "D"}) {
		t.Fatalf("PromotedDownstream = %v, want [B C D] (promote ignores assigned_to)", keys)
	}
	for _, k := range []string{"B", "C", "D"} {
		if got := db.nodes[dagRunNodeKey("dag-1", k, completeDownstreamRunID)].Status; got != "ready" {
			t.Errorf("%s status = %q, want ready (F6.3 promote)", k, got)
		}
	}
	// F6.4: 只 B 有 wakeup（C / D 因 assigned_to 空被跳）。
	if keys := scheduledKeys(res.ScheduledDownstream); !equalStrings(keys, []string{"B"}) {
		t.Fatalf("ScheduledDownstream = %v, want [B] (F6.4 filters C/D)", keys)
	}
}

// TestCompleteNodeAndScheduleDownstream_F63_ReentrantSecondCompleteNoDoublePromote
// F6.3 幂等护栏：同一上游节点重复 complete（并发 / 第二次调用）
// 不会重复 promote 同一下游。第二次 CompleteNode 本身会被 status fence 拒、
// 未走到 promote；但我们补充“在 promote SQL 层 status='pending' fence 本身”
// 也能幂等防护。
func TestCompleteNodeAndScheduleDownstream_F63_ReentrantSecondCompleteNoDoublePromote(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})

	res1, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("first complete A error = %v", err)
	}
	if keys := promotedKeys(res1.PromotedDownstream); !equalStrings(keys, []string{"B"}) {
		t.Fatalf("first PromotedDownstream = %v, want [B]", keys)
	}
	// 第二次 complete A 被 CompleteTaskDagNode fence 拒（这部分原有测试覆盖）；
	// 不会走到 promote。B 仍然为 ready、PromotedDownstream 未重复上报。
	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("second complete A error = nil, want fence rejection")
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (idempotent)", got)
	}
}

// --- helpers --------------------------------------------------------------

func promotedKeys(items []PromotedDownstreamNode) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.NodeKey)
	}
	sort.Strings(keys)
	return keys
}

type seedNode struct {
	key    string
	deps   []string
	status string
	agent  string
	thread string
}

const completeDownstreamRunID int64 = 501

func completeDownstreamRunIDValue() sqlc.Int8 {
	id := completeDownstreamRunID
	return sqlc.Int8ValuePtr(&id)
}

func seedDAG(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode) {
	t.Helper()
	seedDAGRows(t, db, now, nodes, 0)
}

func seedRuntimeDAG(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode) {
	t.Helper()
	seedDAGRows(t, db, now, nodes, completeDownstreamRunID)
}

func seedDAGRows(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode, runID int64) {
	t.Helper()
	for i, n := range nodes {
		depsJSON := []byte(`[]`)
		if len(n.deps) > 0 {
			b, err := json.Marshal(n.deps)
			if err != nil {
				t.Fatalf("marshal deps for %s: %v", n.key, err)
			}
			depsJSON = b
		}
		row := sqlc.TaskDagNode{
			ID:         int64(i + 1),
			DagKey:     "dag-1",
			NodeKey:    n.key,
			Title:      n.key,
			Status:     n.status,
			AssignedTo: n.agent,
			DependsOn:  depsJSON,
			Config:     []byte(`{}`),
			Result:     []byte(`{}`),
			CreatedAt:  timestamptzValue(now.Add(time.Duration(i) * time.Millisecond)),
			UpdatedAt:  timestamptzValue(now),
		}
		key := dagNodeKey("dag-1", n.key)
		if runID > 0 {
			id := runID
			row.RunID = sqlc.Int8ValuePtr(&id)
			key = dagRunNodeKey("dag-1", n.key, runID)
		}
		if n.thread != "" {
			row.SpawningThreadID = sqlc.Text{String: n.thread, Valid: true}
		}
		db.nodes[key] = row
	}
}

func transitionToRunning(t *testing.T, db *fakeTaskDAGDB, nodeKey string) {
	t.Helper()
	key := dagRunNodeKey("dag-1", nodeKey, completeDownstreamRunID)
	row, ok := db.nodes[key]
	if !ok {
		t.Fatalf("node %s not found", nodeKey)
	}
	row.Status = "running"
	db.nodes[key] = row
}

func scheduledKeys(items []ScheduledDownstreamWakeup) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.NodeKey)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pendingForNode(db *fakeTaskDAGDB, nodeKey string) int {
	count := 0
	for _, w := range db.wakeups {
		if w.NodeKey == nodeKey && w.Status == "pending" {
			count++
		}
	}
	return count
}

func lookupPendingWakeup(t *testing.T, db *fakeTaskDAGDB, nodeKey string) sqlc.TaskDagWakeup {
	t.Helper()
	for _, w := range db.wakeups {
		if w.NodeKey == nodeKey && w.Status == "pending" {
			return w
		}
	}
	t.Fatalf("no pending wakeup for node %s", nodeKey)
	return sqlc.TaskDagWakeup{}
}

func assertUpstreamPayload(t *testing.T, w sqlc.TaskDagWakeup, wantAgent, wantUpstreamKey, wantPath string) {
	t.Helper()
	var payload DownstreamWakeupPayload
	if err := json.Unmarshal(w.PromptPayload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v (raw=%s)", err, string(w.PromptPayload))
	}
	if payload.AgentID != wantAgent {
		t.Fatalf("payload.AgentID = %q, want %q", payload.AgentID, wantAgent)
	}
	if len(payload.UpstreamOutputs) != 1 {
		t.Fatalf("payload.UpstreamOutputs = %v, want 1 entry", payload.UpstreamOutputs)
	}
	got := payload.UpstreamOutputs[0]
	if got.NodeKey != wantUpstreamKey || got.Path != wantPath {
		t.Fatalf("upstream = %+v, want {%s %s}", got, wantUpstreamKey, wantPath)
	}
}
