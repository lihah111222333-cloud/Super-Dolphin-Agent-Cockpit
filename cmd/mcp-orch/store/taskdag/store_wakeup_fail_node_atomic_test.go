//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

type wakeupNodeFailureStore interface {
	FailWakeupAndFailNodeAndCancelDownstream(context.Context, FailWakeupInput, FailNodeInput) (int64, *FailNodeResult, error)
}

func TestFailWakeupAndFailNodeAndCancelDownstream_CommitsWakeupNodeCascadeAndFinalize(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	wakeupID := int64(81)
	db.wakeups[wakeupID] = dispatchingWakeupForRun(now, wakeupID, runID, "A")

	atomicStore, ok := store.(wakeupNodeFailureStore)
	if !ok {
		t.Fatal("store missing atomic wakeup+node failure path")
	}
	rows, res, err := atomicStore.FailWakeupAndFailNodeAndCancelDownstream(context.Background(),
		failWakeupInputForRow(db.wakeups[wakeupID]),
		FailNodeInput{DagKey: "dag-1", NodeKey: "A", RunID: runID, Reason: "hard failure", FailFast: true},
	)
	if err != nil {
		t.Fatalf("FailWakeupAndFailNodeAndCancelDownstream err = %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	if got := db.wakeups[wakeupID].Status; got != "failed" {
		t.Fatalf("wakeup status = %q, want failed", got)
	}
	requireRunNodeStatus(t, db, runID, "A", "failed")
	requireRunNodeStatus(t, db, runID, "B", "failed")
	if res == nil || res.FinalizedRun == nil || res.FinalizedRun.Status != "failed" {
		t.Fatalf("FinalizedRun = %+v, want failed", res)
	}
}

func TestFailWakeupAndFailNodeAndCancelDownstream_RollsBackWakeupWhenCascadeFails(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "done", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	wakeupID := int64(82)
	db.wakeups[wakeupID] = dispatchingWakeupForRun(now, wakeupID, runID, "A")

	atomicStore, ok := store.(wakeupNodeFailureStore)
	if !ok {
		t.Fatal("store missing atomic wakeup+node failure path")
	}
	rows, res, err := atomicStore.FailWakeupAndFailNodeAndCancelDownstream(context.Background(),
		failWakeupInputForRow(db.wakeups[wakeupID]),
		FailNodeInput{DagKey: "dag-1", NodeKey: "A", RunID: runID, Reason: "late hard failure", FailFast: true},
	)
	if err == nil {
		t.Fatal("err = nil, want atomic failure")
	}
	if rows != 0 || res != nil {
		t.Fatalf("rows/res = %d/%+v, want 0/nil after rollback", rows, res)
	}
	if got := db.wakeups[wakeupID].Status; got != "dispatching" {
		t.Fatalf("wakeup status = %q, want dispatching rollback when node cascade fails", got)
	}
	requireRunNodeStatus(t, db, runID, "A", "done")
	requireRunNodeStatus(t, db, runID, "B", "pending")
	assertRunStatus(t, db, "run-1", "running", "after rollback")
}

func dispatchingWakeupForRun(now time.Time, id, runID int64, nodeKey string) sqlc.TaskDagWakeup {
	row := newDispatchingWakeup(now, id, "worker-a", time.Minute)
	row.NodeKey = nodeKey
	row.RunID = sqlc.Int8ValuePtr(&runID)
	return row
}

func failWakeupInputForRow(row sqlc.TaskDagWakeup) FailWakeupInput {
	return FailWakeupInput{
		ID:             row.ID,
		LastError:      "hard failure",
		ClaimedAt:      row.ClaimedAt.Time,
		ClaimedBy:      row.ClaimedBy,
		LeaseExpiresAt: row.LeaseExpiresAt.Time,
	}
}
