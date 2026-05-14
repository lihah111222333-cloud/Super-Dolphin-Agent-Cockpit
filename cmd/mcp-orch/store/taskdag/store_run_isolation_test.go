package taskdag

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func TestCloneNodesForRunKeepsTemplateAndRunsIndependent(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runStore := any(store).(RunStore)
	seedDAG(t, db, now, []seedNode{
		{key: "root", deps: nil, status: "pending", agent: "agent-root"},
		{key: "child", deps: []string{"root"}, status: "pending", agent: "agent-child"},
	})
	runA := seedRunID(db, "dag-1", "run-a")
	runB := seedRunID(db, "dag-1", "run-b")

	clonedA, err := runStore.CloneNodesForRun(context.Background(), "dag-1", runA)
	if err != nil {
		t.Fatalf("clone run A error = %v", err)
	}
	if clonedA != 2 {
		t.Fatalf("clone run A rows = %d, want 2", clonedA)
	}
	if promotedA, err := runStore.PromoteRootNodesToReady(context.Background(), "dag-1", runA); err != nil {
		t.Fatalf("promote run A error = %v", err)
	} else if promotedA != 1 {
		t.Fatalf("promote run A rows = %d, want 1", promotedA)
	}
	clonedB, err := runStore.CloneNodesForRun(context.Background(), "dag-1", runB)
	if err != nil {
		t.Fatalf("clone run B error = %v", err)
	}
	if clonedB != 2 {
		t.Fatalf("clone run B rows = %d, want 2", clonedB)
	}
	if promotedB, err := runStore.PromoteRootNodesToReady(context.Background(), "dag-1", runB); err != nil {
		t.Fatalf("promote run B error = %v", err)
	} else if promotedB != 1 {
		t.Fatalf("promote run B rows = %d, want 1", promotedB)
	}

	if got := db.nodes[dagNodeKey("dag-1", "root")].Status; got != "pending" {
		t.Fatalf("template root status = %q, want pending", got)
	}
	if got := db.nodes[dagNodeKey("dag-1", "child")].Status; got != "pending" {
		t.Fatalf("template child status = %q, want pending", got)
	}
	assertRunNodeStatus(t, db, runA, "root", "ready")
	assertRunNodeStatus(t, db, runA, "child", "pending")
	assertRunNodeStatus(t, db, runB, "root", "ready")
	assertRunNodeStatus(t, db, runB, "child", "pending")
}

func TestCompleteNodeAndScheduleDownstreamStaysInsideRun(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAGMetadata(db, "dag-1", json.RawMessage(`{"final_node_key":"child"}`))
	runA := seedRunID(db, "dag-1", "run-a")
	runB := seedRunID(db, "dag-1", "run-b")
	seedRuntimeNode(t, db, now, runA, "root", nil, "running", "agent-root")
	seedRuntimeNode(t, db, now, runA, "child", []string{"root"}, "pending", "agent-child")
	seedRuntimeNode(t, db, now, runB, "root", nil, "running", "agent-root")
	seedRuntimeNode(t, db, now, runB, "child", []string{"root"}, "pending", "agent-child")

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "root",
		RunID:   runA,
		Status:  "done",
		Result:  json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete run A root error = %v", err)
	}
	if len(res.PromotedDownstream) != 1 || res.PromotedDownstream[0].NodeKey != "child" || res.PromotedDownstream[0].RunID != runA {
		t.Fatalf("PromotedDownstream = %+v, want run A child only", res.PromotedDownstream)
	}
	if len(res.ScheduledDownstream) != 1 || res.ScheduledDownstream[0].NodeKey != "child" || res.ScheduledDownstream[0].RunID != runA {
		t.Fatalf("ScheduledDownstream = %+v, want run A child only", res.ScheduledDownstream)
	}
	wakeupA := lookupPendingWakeupForRun(t, db, runA, "child")
	if got := sqlc.Int8Ptr(wakeupA.RunID); got == nil || *got != runA {
		t.Fatalf("run A wakeup run_id = %v, want %d", got, runA)
	}
	if want := downstreamIdempotencyKey("dag-1", "child", runA); wakeupA.IdempotencyKey != want {
		t.Fatalf("run A wakeup idempotency_key = %q, want %q", wakeupA.IdempotencyKey, want)
	}
	assertRunNodeStatus(t, db, runA, "root", "done")
	assertRunNodeStatus(t, db, runA, "child", "ready")
	assertRunNodeStatus(t, db, runB, "root", "running")
	assertRunNodeStatus(t, db, runB, "child", "pending")
	if got := runStatusByKey(t, db, "run-a"); got != "running" {
		t.Fatalf("run A status after root = %q, want running", got)
	}
	if got := runStatusByKey(t, db, "run-b"); got != "running" {
		t.Fatalf("run B status after run A root = %q, want running", got)
	}

	res, err = store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "child",
		RunID:   runA,
		Status:  "done",
		Result:  json.RawMessage(`"final"`),
	})
	if err != nil {
		t.Fatalf("complete run A child error = %v", err)
	}
	if res.FinalizedRun == nil || res.FinalizedRun.RunKey != "run-a" || res.FinalizedRun.Status != "succeeded" {
		t.Fatalf("FinalizedRun = %+v, want run-a succeeded", res.FinalizedRun)
	}
	if got := runStatusByKey(t, db, "run-b"); got != "running" {
		t.Fatalf("run B status after run A finalize = %q, want running", got)
	}
	assertRunNodeStatus(t, db, runB, "root", "running")
	assertRunNodeStatus(t, db, runB, "child", "pending")

	res, err = store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "root",
		RunID:   runB,
		Status:  "done",
		Result:  json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete run B root error = %v", err)
	}
	if len(res.ScheduledDownstream) != 1 || res.ScheduledDownstream[0].RunID != runB {
		t.Fatalf("run B ScheduledDownstream = %+v, want one run B wakeup", res.ScheduledDownstream)
	}
	wakeupB := lookupPendingWakeupForRun(t, db, runB, "child")
	if wakeupB.IdempotencyKey == wakeupA.IdempotencyKey {
		t.Fatalf("run B wakeup reused run A idempotency key %q", wakeupB.IdempotencyKey)
	}
	if want := downstreamIdempotencyKey("dag-1", "child", runB); wakeupB.IdempotencyKey != want {
		t.Fatalf("run B wakeup idempotency_key = %q, want %q", wakeupB.IdempotencyKey, want)
	}
}

func TestFailNodeAndCancelDownstreamStaysInsideRun(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runA := seedRunID(db, "dag-1", "run-a")
	runB := seedRunID(db, "dag-1", "run-b")
	seedRuntimeNode(t, db, now, runA, "root", nil, "running", "agent-root")
	seedRuntimeNode(t, db, now, runA, "child", []string{"root"}, "pending", "agent-child")
	seedRuntimeNode(t, db, now, runB, "root", nil, "running", "agent-root")
	seedRuntimeNode(t, db, now, runB, "child", []string{"root"}, "pending", "agent-child")

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "root",
		RunID:    runA,
		Reason:   "boom",
		FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail run A root error = %v", err)
	}
	if res.Node == nil || res.Node.RunID == nil || *res.Node.RunID != runA || res.Node.Status != "failed" {
		t.Fatalf("failed node = %+v, want run A root failed", res.Node)
	}
	if len(res.CanceledDownstream) != 1 || res.CanceledDownstream[0].NodeKey != "child" || res.CanceledDownstream[0].RunID != runA {
		t.Fatalf("CanceledDownstream = %+v, want run A child only", res.CanceledDownstream)
	}
	if res.FinalizedRun == nil || res.FinalizedRun.RunKey != "run-a" || res.FinalizedRun.Status != "failed" {
		t.Fatalf("FinalizedRun = %+v, want run-a failed", res.FinalizedRun)
	}
	assertRunNodeStatus(t, db, runA, "root", "failed")
	assertRunNodeStatus(t, db, runA, "child", "failed")
	assertRunNodeStatus(t, db, runB, "root", "running")
	assertRunNodeStatus(t, db, runB, "child", "pending")
	if got := runStatusByKey(t, db, "run-a"); got != "failed" {
		t.Fatalf("run-a status = %q, want failed", got)
	}
	if got := runStatusByKey(t, db, "run-b"); got != "running" {
		t.Fatalf("run-b status = %q, want running", got)
	}
}

func seedRunID(db *fakeTaskDAGDB, dagKey, runKey string) int64 {
	seedRun(db, dagKey, runKey)
	return db.runs[runKey].ID
}

func seedRuntimeNode(t *testing.T, db *fakeTaskDAGDB, now time.Time, runID int64, key string, deps []string, status, agent string) {
	t.Helper()
	encodedDeps, err := json.Marshal(deps)
	if err != nil {
		t.Fatalf("marshal deps: %v", err)
	}
	db.nodes[dagRunNodeKey("dag-1", key, runID)] = sqlc.TaskDagNode{
		ID:         int64(len(db.nodes) + 1),
		DagKey:     "dag-1",
		NodeKey:    key,
		RunID:      sqlc.Int8ValuePtr(&runID),
		Title:      key,
		NodeType:   "agent",
		AssignedTo: agent,
		DependsOn:  encodedDeps,
		Status:     status,
		Config:     json.RawMessage(`{}`),
		CreatedAt:  timestamptzValue(now),
		UpdatedAt:  timestamptzValue(now),
	}
}

func assertRunNodeStatus(t *testing.T, db *fakeTaskDAGDB, runID int64, nodeKey, want string) {
	t.Helper()
	row, ok := db.nodes[dagRunNodeKey("dag-1", nodeKey, runID)]
	if !ok {
		t.Fatalf("runtime node run_id=%d node_key=%s not found", runID, nodeKey)
	}
	if row.Status != want {
		t.Fatalf("runtime node run_id=%d node_key=%s status = %q, want %q", runID, nodeKey, row.Status, want)
	}
}

func lookupPendingWakeupForRun(t *testing.T, db *fakeTaskDAGDB, runID int64, nodeKey string) sqlc.TaskDagWakeup {
	t.Helper()
	for _, w := range db.wakeups {
		if w.NodeKey == nodeKey && fakeWakeupRunID(w) == runID && w.Status == "pending" {
			return w
		}
	}
	t.Fatalf("no pending wakeup for run_id=%d node_key=%s", runID, nodeKey)
	return sqlc.TaskDagWakeup{}
}
