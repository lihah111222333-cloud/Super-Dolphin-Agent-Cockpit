//go:build legacy_pg_fake

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

	cloneAndPromoteRunRoot(t, runStore, runA, "run A")
	cloneAndPromoteRunRoot(t, runStore, runB, "run B")

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

	wakeupA := completeRunRootAndAssertIsolation(t, store, db, runA, runB)
	completeRunChildAndAssertFinalized(t, store, db, runA, runB)
	res := completeRunRoot(t, store, runB, "run B")
	assertSingleScheduledDownstream(t, "run B scheduled", res.ScheduledDownstream, runB)
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
	assertFailedRunResult(t, res, runA)
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

func cloneAndPromoteRunRoot(t *testing.T, runStore RunStore, runID int64, label string) {
	t.Helper()
	cloned, err := runStore.CloneNodesForRun(context.Background(), "dag-1", runID)
	if err != nil {
		t.Fatalf("clone %s error = %v", label, err)
	}
	if cloned != 2 {
		t.Fatalf("clone %s rows = %d, want 2", label, cloned)
	}
	promoted, err := runStore.PromoteRootNodesToReady(context.Background(), "dag-1", runID)
	if err != nil {
		t.Fatalf("promote %s error = %v", label, err)
	}
	if promoted != 1 {
		t.Fatalf("promote %s rows = %d, want 1", label, promoted)
	}
}

func completeRunRootAndAssertIsolation(t *testing.T, store Store, db *fakeTaskDAGDB, runA, runB int64) sqlc.TaskDagWakeup {
	t.Helper()
	res := completeRunRoot(t, store, runA, "run A")
	assertSinglePromotedDownstream(t, "PromotedDownstream", res.PromotedDownstream, runA)
	assertSingleScheduledDownstream(t, "ScheduledDownstream", res.ScheduledDownstream, runA)
	wakeupA := lookupPendingWakeupForRun(t, db, runA, "child")
	assertWakeupRunAndKey(t, wakeupA, runA, "run A")
	assertRunNodeStatus(t, db, runA, "root", "done")
	assertRunNodeStatus(t, db, runA, "child", "ready")
	assertRunNodeStatus(t, db, runB, "root", "running")
	assertRunNodeStatus(t, db, runB, "child", "pending")
	assertRunStatus(t, db, "run-a", "running", "after root")
	assertRunStatus(t, db, "run-b", "running", "after run A root")
	return wakeupA
}

func completeRunChildAndAssertFinalized(t *testing.T, store Store, db *fakeTaskDAGDB, runA, runB int64) {
	t.Helper()
	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "child", RunID: runA, Status: "done", Result: json.RawMessage(`"final"`),
	})
	if err != nil {
		t.Fatalf("complete run A child error = %v", err)
	}
	if res.FinalizedRun == nil || res.FinalizedRun.RunKey != "run-a" || res.FinalizedRun.Status != "succeeded" {
		t.Fatalf("FinalizedRun = %+v, want run-a succeeded", res.FinalizedRun)
	}
	assertRunStatus(t, db, "run-b", "running", "after run A finalize")
	assertRunNodeStatus(t, db, runB, "root", "running")
	assertRunNodeStatus(t, db, runB, "child", "pending")
}

func completeRunRoot(t *testing.T, store Store, runID int64, label string) *CompleteNodeWithDownstreamResult {
	t.Helper()
	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "root", RunID: runID, Status: "done", Result: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete %s root error = %v", label, err)
	}
	return res
}

func assertSinglePromotedDownstream(t *testing.T, label string, nodes []PromotedDownstreamNode, runID int64) {
	t.Helper()
	if len(nodes) != 1 || nodes[0].NodeKey != "child" || nodes[0].RunID != runID {
		t.Fatalf("%s = %+v, want child only for run %d", label, nodes, runID)
	}
}

func assertSingleScheduledDownstream(t *testing.T, label string, nodes []ScheduledDownstreamWakeup, runID int64) {
	t.Helper()
	if len(nodes) != 1 || nodes[0].NodeKey != "child" || nodes[0].RunID != runID {
		t.Fatalf("%s = %+v, want child only for run %d", label, nodes, runID)
	}
}

func assertWakeupRunAndKey(t *testing.T, wakeup sqlc.TaskDagWakeup, runID int64, label string) {
	t.Helper()
	if got := sqlc.Int8Ptr(wakeup.RunID); got == nil || *got != runID {
		t.Fatalf("%s wakeup run_id = %v, want %d", label, got, runID)
	}
	if want := downstreamIdempotencyKey("dag-1", "child", runID); wakeup.IdempotencyKey != want {
		t.Fatalf("%s wakeup idempotency_key = %q, want %q", label, wakeup.IdempotencyKey, want)
	}
}

func assertRunStatus(t *testing.T, db *fakeTaskDAGDB, runKey, want, context string) {
	t.Helper()
	if got := runStatusByKey(t, db, runKey); got != want {
		t.Fatalf("%s status %s = %q, want %q", runKey, context, got, want)
	}
}

func assertFailedRunResult(t *testing.T, res *FailNodeResult, runA int64) {
	t.Helper()
	assertFailedPrimaryNode(t, res.Node, runA)
	assertCanceledChild(t, res.CanceledDownstream, runA)
	assertFinalizedFailedRun(t, res.FinalizedRun)
}

func assertFailedPrimaryNode(t *testing.T, node *Node, runA int64) {
	t.Helper()
	if node == nil || node.RunID == nil || *node.RunID != runA || node.Status != "failed" {
		t.Fatalf("failed node = %+v, want run A root failed", node)
	}
}

func assertCanceledChild(t *testing.T, nodes []CanceledDownstreamNode, runA int64) {
	t.Helper()
	if len(nodes) != 1 || nodes[0].NodeKey != "child" || nodes[0].RunID != runA {
		t.Fatalf("CanceledDownstream = %+v, want run A child only", nodes)
	}
}

func assertFinalizedFailedRun(t *testing.T, finalized *FinalizedRunInfo) {
	t.Helper()
	if finalized == nil || finalized.RunKey != "run-a" || finalized.Status != "failed" {
		t.Fatalf("FinalizedRun = %+v, want run-a failed", finalized)
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
		Config:     validAgentConfigForTest(agent),
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
