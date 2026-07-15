package taskdag

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSQLiteTaskDAGFailWakeupCascadeCommitsAtomically(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteCascadeTemplate(t, ctx, store, "dag-wakeup-fail")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-wakeup-fail", "dag-wakeup-fail")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-wakeup-fail", run.ID)
	claimed := enqueueAndClaimSQLiteWakeup(t, ctx, store, EnqueueWakeupInput{
		DagKey: "dag-wakeup-fail", NodeKey: "root", RunID: run.ID, WakeupKind: "node_start",
		TargetAgentID: "agent-root", PromptPayload: json.RawMessage(`{"prompt":"start"}`), IdempotencyKey: "wakeup-fail-root",
	})

	rows, result, err := store.FailWakeupAndFailNodeAndCancelDownstream(ctx, failSQLiteWakeupInput(claimed, "hard failure"), FailNodeInput{
		DagKey: "dag-wakeup-fail", NodeKey: "root", RunID: run.ID, Reason: "hard failure", FailFast: true,
	})
	if err != nil || rows != 1 || result == nil || result.FinalizedRun == nil || result.FinalizedRun.Status != "failed" {
		t.Fatalf("failure result rows=%d result=%+v error=%v, want finalized failed run", rows, result, err)
	}
	assertSQLiteFailedWakeupCascade(t, ctx, store, run.ID, claimed.ID)
}

func assertSQLiteFailedWakeupCascade(t *testing.T, ctx context.Context, store *store, runID, wakeupID int64) {
	t.Helper()
	wakeup, err := store.GetWakeup(ctx, wakeupID)
	if err != nil || wakeup.Status != "failed" || wakeup.LastError != "hard failure" {
		t.Fatalf("GetWakeup() wakeup=%+v error=%v, want failed/hard failure", wakeup, err)
	}
	for _, nodeKey := range []string{"root", "child", "leaf"} {
		if got := sqliteRunNodeStatus(t, ctx, store, "dag-wakeup-fail", runID, nodeKey); got != "failed" {
			t.Fatalf("%s status = %q, want failed", nodeKey, got)
		}
	}
}

func TestSQLiteTaskDAGFailWakeupCascadeRollsBackAtomically(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteFlowTemplate(t, ctx, store, "dag-wakeup-rollback")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-wakeup-rollback", "dag-wakeup-rollback")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-wakeup-rollback", run.ID)
	claimed := enqueueAndClaimSQLiteWakeup(t, ctx, store, EnqueueWakeupInput{
		DagKey: "dag-wakeup-rollback", NodeKey: "root", RunID: run.ID, WakeupKind: "node_start",
		TargetAgentID: "agent-root", PromptPayload: json.RawMessage(`{"prompt":"start"}`), IdempotencyKey: "wakeup-rollback-root",
	})
	if _, err := db.ExecContext(ctx, `UPDATE task_dag_nodes SET status = 'done' WHERE dag_key = ? AND run_id = ? AND node_key = ?`, "dag-wakeup-rollback", run.ID, "root"); err != nil {
		t.Fatalf("seed terminal root: %v", err)
	}

	rows, result, err := store.FailWakeupAndFailNodeAndCancelDownstream(ctx, failSQLiteWakeupInput(claimed, "late failure"), FailNodeInput{
		DagKey: "dag-wakeup-rollback", NodeKey: "root", RunID: run.ID, Reason: "late failure", FailFast: true,
	})
	if err == nil || rows != 0 || result != nil {
		t.Fatalf("rollback result rows=%d result=%+v error=%v, want 0/nil/error", rows, result, err)
	}
	assertSQLiteFailedWakeupRollback(t, ctx, store, run.ID, claimed.ID)
}

func assertSQLiteFailedWakeupRollback(t *testing.T, ctx context.Context, store *store, runID, wakeupID int64) {
	t.Helper()
	wakeup, err := store.GetWakeup(ctx, wakeupID)
	if err != nil || wakeup.Status != "dispatching" || wakeup.LastError != "" {
		t.Fatalf("GetWakeup() after rollback wakeup=%+v error=%v, want dispatching unchanged", wakeup, err)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-wakeup-rollback", runID, "root"); got != "done" {
		t.Fatalf("root status after rollback = %q, want done", got)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-wakeup-rollback", runID, "child"); got != "pending" {
		t.Fatalf("child status after rollback = %q, want pending", got)
	}
}

func enqueueAndClaimSQLiteWakeup(t *testing.T, ctx context.Context, store *store, input EnqueueWakeupInput) Wakeup {
	t.Helper()
	wakeupID, err := store.EnqueueWakeup(ctx, input)
	if err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}
	claimed, err := store.ClaimDueWakeups(ctx, ClaimDueWakeupsInput{ClaimedBy: "worker-atomic", LeaseInterval: "30s", Limit: 1})
	if err != nil || len(claimed) != 1 || claimed[0].ID != wakeupID {
		t.Fatalf("claimed wakeups = %+v error=%v, want wakeup %d", claimed, err, wakeupID)
	}
	if claimed[0].ClaimedAt == nil || claimed[0].LeaseExpiresAt == nil {
		t.Fatalf("claimed wakeup missing fence: %+v", claimed[0])
	}
	return claimed[0]
}

func failSQLiteWakeupInput(wakeup Wakeup, lastError string) FailWakeupInput {
	return FailWakeupInput{
		ID: wakeup.ID, LastError: lastError, ClaimedAt: *wakeup.ClaimedAt,
		ClaimedBy: wakeup.ClaimedBy, LeaseExpiresAt: *wakeup.LeaseExpiresAt,
	}
}
