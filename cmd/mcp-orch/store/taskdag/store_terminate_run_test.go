//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestTerminateRunCancelsRunningRunAndNonTerminalNodes(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "done-node", status: "done", thread: "thr-done"},
		{key: "ready-node", status: "ready", thread: "thr-ready"},
		{key: "running-node", status: "running", thread: "thr-running"},
		{key: "pending-node", status: "pending"},
	})
	seedRunningRunForTerminate(db, "dag-1", "run-cancel", completeDownstreamRunID)

	result, err := any(store).(RunTerminationStore).TerminateRun(context.Background(), TerminateRunInput{
		DagKey: "dag-1",
		RunKey: "run-cancel",
		RunID:  completeDownstreamRunID,
		Reason: "user_requested",
	})
	if err != nil {
		t.Fatalf("TerminateRun() error = %v, want nil", err)
	}
	if got := strings.Join(result.SpawnedThreadIDs, ","); got != "thr-done,thr-ready,thr-running" {
		t.Fatalf("TerminateRun spawned thread IDs = %q, want all persisted spawned thread IDs for the run", got)
	}

	if got := runStatusByKey(t, db, "run-cancel"); got != "cancelled" {
		t.Fatalf("run.status = %q, want cancelled", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "done-node", completeDownstreamRunID)].Status; got != "done" {
		t.Fatalf("done-node status = %q, want done", got)
	}
	for _, nodeKey := range []string{"ready-node", "running-node", "pending-node"} {
		row := db.nodes[dagRunNodeKey("dag-1", nodeKey, completeDownstreamRunID)]
		if row.Status != "cancelled" {
			t.Fatalf("%s status = %q, want cancelled", nodeKey, row.Status)
		}
		var payload map[string]any
		if err := json.Unmarshal(row.Result, &payload); err != nil {
			t.Fatalf("%s result json = %s: %v", nodeKey, string(row.Result), err)
		}
		if payload["kind"] != "run_cancelled" || payload["reason"] != "user_requested" {
			t.Fatalf("%s result = %v, want run_cancelled user_requested", nodeKey, payload)
		}
	}
}

func TestTerminateRunCancelsOnlyActiveWakeupsForTargetRun(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "ready-node", status: "ready"},
	})
	seedRunningRunForTerminate(db, "dag-1", "run-cancel", completeDownstreamRunID)
	seedRunningRunForTerminate(db, "dag-1", "run-other", completeDownstreamRunID+1)
	db.wakeups[1] = terminateWakeupRun(newPendingWakeup(now, 1), completeDownstreamRunID)
	db.wakeups[2] = terminateWakeupRun(newDispatchingWakeup(now, 2, "worker-a", time.Minute), completeDownstreamRunID)
	db.wakeups[3] = terminateWakeupRun(newSentWakeup(now, 3), completeDownstreamRunID)
	db.wakeups[4] = terminateWakeupRun(newPendingWakeup(now, 4), completeDownstreamRunID+1)
	db.wakeups[5] = terminateWakeupRun(newPendingWakeup(now, 5), completeDownstreamRunID)
	db.wakeups[5] = terminateWakeupStatus(db.wakeups[5], "failed")

	_, err := any(store).(RunTerminationStore).TerminateRun(context.Background(), TerminateRunInput{
		DagKey: "dag-1",
		RunKey: "run-cancel",
		RunID:  completeDownstreamRunID,
		Reason: "user_requested",
	})
	if err != nil {
		t.Fatalf("TerminateRun() error = %v, want nil", err)
	}

	for _, id := range []int64{1, 2, 3} {
		assertRunCancelledWakeup(t, db.wakeups[id], id)
	}
	if got := db.wakeups[4].Status; got != "pending" {
		t.Fatalf("other run wakeup status = %q, want pending", got)
	}
	if got := db.wakeups[5].Status; got != "failed" || db.wakeups[5].LastError != "" {
		t.Fatalf("terminal wakeup = status %q last_error %q, want unchanged failed/empty", got, db.wakeups[5].LastError)
	}
}

func TestTerminateRunCancelledRunReturnsPersistedSpawnedThreadIDs(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "cancelled-node", status: "cancelled", thread: "thr-cancelled"},
		{key: "done-node", status: "done", thread: "thr-done"},
	})
	seedRunningRunForTerminate(db, "dag-1", "run-cancelled", completeDownstreamRunID)
	run := db.runs["run-cancelled"]
	run.Status = "cancelled"
	db.runs["run-cancelled"] = run

	result, err := any(store).(RunTerminationStore).TerminateRun(context.Background(), TerminateRunInput{
		DagKey: "dag-1",
		RunKey: "run-cancelled",
		RunID:  completeDownstreamRunID,
		Reason: "user_requested",
	})
	if err != nil {
		t.Fatalf("TerminateRun() error = %v, want nil for already-cancelled retry", err)
	}
	if got := strings.Join(result.SpawnedThreadIDs, ","); got != "thr-cancelled,thr-done" {
		t.Fatalf("TerminateRun spawned thread IDs = %q, want all persisted thread IDs for retry", got)
	}
}

func TestTerminateRunRollsBackNodeAndWakeupCancelsWhenRunRowMisses(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "ready-node", status: "ready"},
	})
	db.wakeups[1] = terminateWakeupRun(newPendingWakeup(now, 1), completeDownstreamRunID)
	seedRunningRunForTerminate(db, "dag-1", "run-cancel", completeDownstreamRunID)
	run := db.runs["run-cancel"]
	run.Status = "succeeded"
	db.runs["run-cancel"] = run

	_, err := any(store).(RunTerminationStore).TerminateRun(context.Background(), TerminateRunInput{
		DagKey: "dag-1",
		RunKey: "run-cancel",
		RunID:  completeDownstreamRunID,
		Reason: "user_requested",
	})
	if err == nil {
		t.Fatal("TerminateRun() error = nil, want run row miss")
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "ready-node", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("node status after rollback = %q, want ready", got)
	}
	if got := db.wakeups[1].Status; got != "pending" {
		t.Fatalf("wakeup status after rollback = %q, want pending", got)
	}
}

func seedRunningRunForTerminate(db *fakeTaskDAGDB, dagKey, runKey string, runID int64) {
	db.runs[runKey] = sqlc.TaskDagRun{
		ID:                 runID,
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: 1,
		TriggerSource:      "manual",
		Status:             "running",
		StartedAt:          timestamptzValue(db.now),
		Metadata:           json.RawMessage(`{}`),
		CreatedAt:          timestamptzValue(db.now),
		UpdatedAt:          timestamptzValue(db.now),
	}
}

func assertRunCancelledWakeup(t *testing.T, row sqlc.TaskDagWakeup, id int64) {
	t.Helper()
	if row.Status != "failed" {
		t.Fatalf("wakeup %d status = %q, want failed", id, row.Status)
	}
	if row.LastError != "run_cancelled: user_requested" {
		t.Fatalf("wakeup %d last_error = %q, want run_cancelled reason", id, row.LastError)
	}
	if row.ClaimedAt.Valid || row.ClaimedBy != "" || row.LeaseExpiresAt.Valid {
		t.Fatalf("wakeup %d claim fence not cleared: %+v", id, row)
	}
}

func terminateWakeupRun(row sqlc.TaskDagWakeup, runID int64) sqlc.TaskDagWakeup {
	row.RunID = sqlc.Int8ValuePtr(&runID)
	return row
}

func terminateWakeupStatus(row sqlc.TaskDagWakeup, status string) sqlc.TaskDagWakeup {
	row.Status = status
	return row
}
