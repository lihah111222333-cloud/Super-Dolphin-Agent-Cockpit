//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

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
	// C 依赖 B，B 尚未完成时不能提前调度。
	if pendingForNode(db, "C") != 0 {
		t.Fatalf("C wakeup count = %d, want 0", pendingForNode(db, "C"))
	}
	// B 的 idempotency_key 必须由 DAG、节点和 run 共同确定，避免重复完成时插入重复 wakeup。
	if want := downstreamIdempotencyKey("dag-1", "B", completeDownstreamRunID); res.ScheduledDownstream[0].IdempotencyKey != want {
		t.Fatalf("B idempotency_key = %q, want %s", res.ScheduledDownstream[0].IdempotencyKey, want)
	}
	// wakeup payload 不再隐式塞上游输出，下游上下文必须通过 inputs.from_nodes 显式读取节点 result。
	bWakeup := lookupPendingWakeup(t, db, "B")
	assertNoImplicitUpstreamPayload(t, bWakeup, "agent-b")

	// 模拟 dispatcher 已把 B 切到 running；B 完成后 C 才能 ready 并入队。
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

	// 预置一条下游 wakeup，使用自动调度会生成的同一 idempotency key。
	// A 完成时必须走冲突去重路径：不新增 wakeup，返回空调度列表，并保留原 payload。
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
	// 冲突插入不能覆盖已有 payload，否则会丢失先前调度上下文。
	if w := db.wakeups[1]; string(w.PromptPayload) != `{"agent_id":"agent-b","note":"pre-seeded"}` {
		t.Fatalf("payload overwritten: %s", string(w.PromptPayload))
	}
}

func TestCompleteNodeAndScheduleDownstream_SecondCompleteOnDoneNodeIsNoRowsError(t *testing.T) {
	t.Parallel()

	// 同一上游节点重复 complete 时，第二次会被状态栅栏拒绝。
	// 预期行为是直接返回底层未命中错误，并且事务不再继续调度下游或创建重复 wakeup。
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

	// 两个上游最终汇聚到同一下游；只有最后满足依赖的 complete 才能入队 B。
	// 若后续再遇到同一 idempotency key，返回的 ScheduledDownstream 不能重复包含 B。
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
	// A1 单独完成还不能入队 B，因为 A2 仍未完成。
	if pendingForNode(db, "B") != 0 {
		t.Fatalf("after A1 only B count = %d, want 0", pendingForNode(db, "B"))
	}
	// A2 完成后 B 的依赖才全部满足，且只应入队一次。
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

// TestCompleteNodeAndScheduleDownstream_SkipsWakeupForUnassignedNode 验证未指派下游的分流边界。
// 依赖满足时仍要把节点推进到 ready，但 assigned_to 为空不能 enqueue wakeup，避免 dispatcher 拿空 agent id 启动失败。
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
	// B 缺 assigned_to，调度结果必须为空。
	if got := scheduledKeys(res.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("scheduled = %v, want [] (B has empty assigned_to)", got)
	}
	// DB 中也不能留下 B 的 pending wakeup。
	if c := pendingForNode(db, "B"); c != 0 {
		t.Fatalf("B wakeup count = %d, want 0 (unassigned must skip enqueue)", c)
	}
	// 即使未指派，B 的状态仍要推进到 ready，等待后续人工或外部接管。
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (F6.3 promote pending鈫抮eady)", got)
	}
	// PromotedDownstream 仍要包含 B，方便上层解释“已 ready 但阻塞在指派”。
	if len(res.PromotedDownstream) != 1 || res.PromotedDownstream[0].NodeKey != "B" {
		t.Fatalf("PromotedDownstream = %+v, want [{dag-1 B}]", res.PromotedDownstream)
	}
	assertRunHasDispatchBlockedEvent(t, db, "run-complete", "B", "assigned_to")
}

// TestCompleteNodeAndScheduleDownstream_SkipsWakeupForWhitespaceAssignedTo 锁定 assigned_to 的 TrimSpace 边界。
// 纯空白 assignee 必须等同于空值：节点可 ready，但不能 enqueue wakeup。
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
	// B 的 assigned_to 只有空白，裁剪后必须被视为未指派。
	if got := scheduledKeys(res.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("scheduled = %v, want [] (B assigned_to is whitespace-only)", got)
	}
	// DB 中不能出现 B 的 pending wakeup。
	if c := pendingForNode(db, "B"); c != 0 {
		t.Fatalf("B wakeup count = %d, want 0 (whitespace assigned_to must skip enqueue)", c)
	}
	// 状态仍要推进到 ready，等待后续显式指派。
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (F6.3 promote pending鈫抮eady)", got)
	}
}

// TestCompleteNodeAndScheduleDownstream_MixedAssignmentFanOut 覆盖同一批下游里已指派和未指派混合的场景。
// 已指派节点必须 enqueue，未指派节点只 ready 不 enqueue，二者不能互相影响。
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

// 已指派下游走正常推进路径。
// A 完成后 B 要从 pending 进入 ready，同时出现在 promoted 和 scheduled 两个返回列表里。
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

// 菱形依赖只允许在全部上游完成后推进汇聚节点。
// D 同时依赖 B/C；只有两条上游都完成后才允许从 pending 推进到 ready。
func TestCompleteNodeAndScheduleDownstream_F63_DiamondPartialUpstreamNoPromote(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	// 1) A 完成后只推进 B/C，D 还缺 B/C 的完成信号。
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

	// 2) B 完成后 D 仍不能推进，因为 C 尚未完成。
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

	// 3) C 完成后 D 的所有依赖满足，才能推进到 ready。
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

// 链式依赖只能逐级推进。
// A 完成只能推进 B；B 完成后 C 才能 ready，防止链路下游被提前唤醒。
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

// ready 推进和 wakeup 入队是两个独立边界。
// assigned_to 只决定是否 enqueue；依赖满足的节点仍必须进入 ready。
func TestCompleteNodeAndScheduleDownstream_F63_PromoteEvenWithoutAssignedTo(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: ""},
		{key: "D", deps: []string{"A"}, status: "pending", agent: "   "}, // 纯空白 assignee
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	// 三个下游都要推进到 ready，不能因为缺少 assignee 被过滤。
	if keys := promotedKeys(res.PromotedDownstream); !equalStrings(keys, []string{"B", "C", "D"}) {
		t.Fatalf("PromotedDownstream = %v, want [B C D] (promote ignores assigned_to)", keys)
	}
	for _, k := range []string{"B", "C", "D"} {
		if got := db.nodes[dagRunNodeKey("dag-1", k, completeDownstreamRunID)].Status; got != "ready" {
			t.Errorf("%s status = %q, want ready (F6.3 promote)", k, got)
		}
	}
	// 只有 B 有有效 assignee，所以只有 B 会生成 wakeup。
	if keys := scheduledKeys(res.ScheduledDownstream); !equalStrings(keys, []string{"B"}) {
		t.Fatalf("ScheduledDownstream = %v, want [B] (F6.4 filters C/D)", keys)
	}
}

// 重复 complete 不能重复推进同一个下游。
// 第二次 complete 会被状态栅栏挡住，不能重复推进同一个下游。
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
	// 第二次 complete A 被 CompleteTaskDagNode 的状态栅栏拒绝，不会再次进入 promote 逻辑。
	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("second complete A error = nil, want fence rejection")
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (idempotent)", got)
	}
}
