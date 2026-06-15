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
	// C must not be scheduled 鈥?depends_on=[B] still pending.
	if pendingForNode(db, "C") != 0 {
		t.Fatalf("C wakeup count = %d, want 0", pendingForNode(db, "C"))
	}
	// idempotency_key for B must follow plan convention.
	if want := downstreamIdempotencyKey("dag-1", "B", completeDownstreamRunID); res.ScheduledDownstream[0].IdempotencyKey != want {
		t.Fatalf("B idempotency_key = %q, want %s", res.ScheduledDownstream[0].IdempotencyKey, want)
	}
	// Payload no longer carries the legacy implicit upstream-output hint.
	// Downstream context is explicit: inputs.from_nodes reads node.result envelopes.
	bWakeup := lookupPendingWakeup(t, db, "B")
	assertNoImplicitUpstreamPayload(t, bWakeup, "agent-b")

	// Now mark B running (dispatcher would do this); complete B 鈫?C ready.
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
	// call (ADR-017 v1.2 搂2.3 鎵╁悗浠嶄笉鍚?'done')銆?
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
	// A1 alone shouldn't enqueue B (A2 still pending 鈫?deps unsatisfied).
	if pendingForNode(db, "B") != 0 {
		t.Fatalf("after A1 only B count = %d, want 0", pendingForNode(db, "B"))
	}
	// Complete A2 鈫?B becomes ready and is enqueued exactly once.
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
// 楠岃瘉 F6.4 + F6.3 鍗忎綔锛氫笅娓歌妭鐐?assigned_to 涓虹┖鏃?
//   - F6.4锛歴tore 灞備笉 enqueue wakeup锛堝惁鍒?dispatcher 璋?LaunchAgent 浼氬洜
//     "agent id is required" 澶辫触銆乺etry 鑰楀敖鍚庤鑺傜偣 permanent failed锛?
//   - F6.3锛氱姸鎬佹満浠嶇劧鎶?pending 鈫?ready 鎺ㄨ繘锛堜緷璧栨弧瓒崇殑鐪熺浉锛夛紝
//     绛夊閮?agent / 浜哄伐鎺ョ琛?assigned_to 鍚庣洿鎺ヨ繘 dispatcher銆?
//
// EN: When a downstream node has dependencies satisfied but no assigned_to:
//   - F6.4: skip the wakeup enqueue (avoid LaunchAgent failure cascade)
//   - F6.3: still promote pending 鈫?ready (state-machine truth);
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
	// 鍏抽敭鏂█ 1锛團6.4锛夛細B 缂?assigned_to 鈫?ScheduledDownstream 蹇呴』涓虹┖銆?
	if got := scheduledKeys(res.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("scheduled = %v, want [] (B has empty assigned_to)", got)
	}
	// 鍏抽敭鏂█ 2锛團6.4锛夛細琛ㄩ噷涔熶笉搴旇鏈変换浣?B 鐨?pending wakeup 琛屻€?
	if c := pendingForNode(db, "B"); c != 0 {
		t.Fatalf("B wakeup count = %d, want 0 (unassigned must skip enqueue)", c)
	}
	// 鍏抽敭鏂█ 3锛團6.3锛夛細B 鑺傜偣鐘舵€佽 promote 鍒?ready锛堝嵆浣?unassigned锛夈€?
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (F6.3 promote pending鈫抮eady)", got)
	}
	// 鍏抽敭鏂█ 4锛團6.3锛夛細PromotedDownstream 搴斿寘鍚?B锛堢姸鎬佹満鐪熺浉 vs F6.4 璺敱锛夈€?
	if len(res.PromotedDownstream) != 1 || res.PromotedDownstream[0].NodeKey != "B" {
		t.Fatalf("PromotedDownstream = %+v, want [{dag-1 B}]", res.PromotedDownstream)
	}
	assertRunHasDispatchBlockedEvent(t, db, "run-complete", "B", "assigned_to")
}

// TestCompleteNodeAndScheduleDownstream_SkipsWakeupForWhitespaceAssignedTo
// F6.4 琛ュ己锛堥槻鍥炲綊 Nit 1锛夛細assigned_to 涓虹函绌虹櫧瀛楃涓诧紙濡?"   "锛夊繀椤荤瓑浠蜂簬绌猴紝
// store 灞傜粡 strings.TrimSpace 瀹堟姢鍚庡悓鏍疯烦杩?enqueue銆傝嫢鏈潵鏈変汉鏀规垚
// `agentID := cand.AssignedTo`锛堝幓鎺?TrimSpace锛夛紝姝ゆ祴璇曞皢澶辫触 鈥?閽夋璇ュ畧鎶ゃ€?
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
	// 鍏抽敭鏂█ 1锛團6.4锛夛細B 鐨?assigned_to 涓虹函绌虹櫧锛孴rimSpace 鍚庝负绌?鈫?ScheduledDownstream 蹇呴』涓虹┖銆?
	if got := scheduledKeys(res.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("scheduled = %v, want [] (B assigned_to is whitespace-only)", got)
	}
	// 鍏抽敭鏂█ 2锛團6.4锛夛細DB 涔熶笉搴旀湁 B 鐨?pending wakeup 琛屻€?
	if c := pendingForNode(db, "B"); c != 0 {
		t.Fatalf("B wakeup count = %d, want 0 (whitespace assigned_to must skip enqueue)", c)
	}
	// 鍏抽敭鏂█ 3锛團6.3锛夛細B 鐘舵€佽 promote 鍒?ready锛堟棤璁?assigned_to 鏄惁绌虹櫧锛夈€?
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (F6.3 promote pending鈫抮eady)", got)
	}
}

// TestCompleteNodeAndScheduleDownstream_MixedAssignmentFanOut
// 杈圭晫鍦烘櫙锛氭墖鍑轰笅娓搁噷鏈夌殑鏈?assigned_to銆佹湁鐨勬病鏈?鈥?鏈夌殑蹇呴』 enqueue銆?
// 娌＄殑蹇呴』璺宠繃锛屼簰涓嶅奖鍝嶃€?
//
// EN: Mixed fan-out 鈥?only downstream nodes carrying an assigned_to get a
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
// F6.3 鏍稿績鍦烘櫙锛欰 done 鍚庡崟涓湁 assigned_to 鐨勪笅娓?B
//   - status: pending 鈫?ready锛圥romoteSingleNodePendingToReady锛?
//   - PromotedDownstream 涓惈 B
//   - ScheduledDownstream 涓惈 B锛團6.4 璺敱涓嶈烦杩囬潪绌?assigned_to锛?
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
// F6.3 鏍稿績鍦烘櫙锛氬渚濊禆鑺傜偣涓婃父鍙儴鍒嗗畬鎴愭椂鏈?promote銆?
// Diamond A 鈫?(B, C) 鈫?D锛欱 done 鍚庡崟鐙笉鑳借 D promote锛屼粎鍦?C done 鍚庝袱涓?
// 渚濊禆閮芥弧瓒?D 鎵?ready銆?
func TestCompleteNodeAndScheduleDownstream_F63_DiamondPartialUpstreamNoPromote(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	// 1) A done 鈫?promote B, C 浣嗕笉 promote D銆?
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

	// 2) 璋冨害涓婁笂 B 杩涜繍琛屽悗 done锛孌 浠嶄笉鑳?promote锛圕 鏈?done锛夈€?
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

	// 3) C done 鈫?D 渚濊禆鍏ㄩ儴婊¤冻 鈫?promote銆?
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
// F6.3 閾惧紡鍦烘櫙锛欰 鈫?B 鈫?C銆?
// 鈥?A done 鈫?B promote銆侰 涓嶅姩锛堜緷璧?B 杩樻湭 done锛夈€?
// 鈥?B done 鈫?C promote銆?
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
// F6.3 鍒涙柊椤癸細鏃?assigned_to 鐨勪笅娓镐粛鐒?promote锛團6.4 浠呰烦 wakeup锛夈€?
// 鏈祴璇曢拤姝绘渤闄呭垝鍒嗭細promote 涓嶆护鎺?assigned_to銆?
//
// EN: This test pins F6.3 脳 F6.4 division 鈥?promote (state-machine truth) is
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
	// F6.3: 涓変釜涓嬫父閮借 promote銆?
	if keys := promotedKeys(res.PromotedDownstream); !equalStrings(keys, []string{"B", "C", "D"}) {
		t.Fatalf("PromotedDownstream = %v, want [B C D] (promote ignores assigned_to)", keys)
	}
	for _, k := range []string{"B", "C", "D"} {
		if got := db.nodes[dagRunNodeKey("dag-1", k, completeDownstreamRunID)].Status; got != "ready" {
			t.Errorf("%s status = %q, want ready (F6.3 promote)", k, got)
		}
	}
	// F6.4: 鍙?B 鏈?wakeup锛圕 / D 鍥?assigned_to 绌鸿璺筹級銆?
	if keys := scheduledKeys(res.ScheduledDownstream); !equalStrings(keys, []string{"B"}) {
		t.Fatalf("ScheduledDownstream = %v, want [B] (F6.4 filters C/D)", keys)
	}
}

// TestCompleteNodeAndScheduleDownstream_F63_ReentrantSecondCompleteNoDoublePromote
// F6.3 骞傜瓑鎶ゆ爮锛氬悓涓€涓婃父鑺傜偣閲嶅 complete锛堝苟鍙?/ 绗簩娆¤皟鐢級
// 涓嶄細閲嶅 promote 鍚屼竴涓嬫父銆傜浜屾 CompleteNode 鏈韩浼氳 status fence 鎷掋€?
// 鏈蛋鍒?promote锛涗絾鎴戜滑琛ュ厖鈥滃湪 promote SQL 灞?status='pending' fence 鏈韩鈥?
// 涔熻兘骞傜瓑闃叉姢銆?
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
	// 绗簩娆?complete A 琚?CompleteTaskDagNode fence 鎷掞紙杩欓儴鍒嗗師鏈夋祴璇曡鐩栵級锛?
	// 涓嶄細璧板埌 promote銆侭 浠嶇劧涓?ready銆丳romotedDownstream 鏈噸澶嶄笂鎶ャ€?
	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("second complete A error = nil, want fence rejection")
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready (idempotent)", got)
	}
}
