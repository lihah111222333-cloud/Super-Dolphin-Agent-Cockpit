package taskdag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSQLiteTaskDAGTerminateRunCommitsAtomically(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteFlowTemplate(t, ctx, store, "dag-terminate")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-terminate", "dag-terminate")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-terminate", run.ID)
	if _, err := db.ExecContext(ctx, `UPDATE task_dag_nodes SET spawning_thread_id = ? WHERE dag_key = ? AND run_id = ? AND node_key = ?`, "thread-root", "dag-terminate", run.ID, "root"); err != nil {
		t.Fatalf("seed root spawning thread: %v", err)
	}
	wakeupID, err := store.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey: "dag-terminate", NodeKey: "root", RunID: run.ID, WakeupKind: "node_start",
		TargetAgentID: "agent-root", PromptPayload: json.RawMessage(`{"prompt":"start"}`), IdempotencyKey: "terminate-root",
	})
	if err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}

	result, err := store.TerminateRun(ctx, TerminateRunInput{
		DagKey: "dag-terminate", RunKey: "run-terminate", RunID: run.ID, Reason: "operator_stop",
	})
	if err != nil {
		t.Fatalf("TerminateRun() error = %v", err)
	}
	if strings.Join(result.SpawnedThreadIDs, ",") != "thread-root" {
		t.Fatalf("SpawnedThreadIDs = %v, want thread-root", result.SpawnedThreadIDs)
	}
	assertSQLiteTerminatedRunState(t, ctx, store, run.ID, wakeupID)
}

func assertSQLiteTerminatedRunState(t *testing.T, ctx context.Context, store *store, runID, wakeupID int64) {
	t.Helper()
	for _, nodeKey := range []string{"root", "child"} {
		if got := sqliteRunNodeStatus(t, ctx, store, "dag-terminate", runID, nodeKey); got != "cancelled" {
			t.Fatalf("%s status = %q, want cancelled", nodeKey, got)
		}
	}
	persisted, err := store.GetRun(ctx, "run-terminate")
	if err != nil || persisted.Status != "cancelled" {
		t.Fatalf("GetRun() status=%v error=%v, want cancelled/nil", persisted, err)
	}
	wakeup, err := store.GetWakeup(ctx, wakeupID)
	if err != nil || wakeup.Status != "failed" || wakeup.LastError != "run_cancelled: operator_stop" {
		t.Fatalf("GetWakeup() wakeup=%+v error=%v, want failed run_cancelled", wakeup, err)
	}
}

func TestSQLiteTaskDAGTerminateRunFenceMissRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteFlowTemplate(t, ctx, store, "dag-terminate-fence")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-terminate-fence", "dag-terminate-fence")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-terminate-fence", run.ID)
	wakeupID, err := store.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey: "dag-terminate-fence", NodeKey: "root", RunID: run.ID, WakeupKind: "node_start",
		TargetAgentID: "agent-root", PromptPayload: json.RawMessage(`{"prompt":"start"}`), IdempotencyKey: "terminate-fence-root",
	})
	if err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}

	if _, err := store.TerminateRun(ctx, TerminateRunInput{
		DagKey: "dag-terminate-fence", RunKey: "missing-run-key", RunID: run.ID, Reason: "operator_stop",
	}); err == nil {
		t.Fatal("TerminateRun() error = nil, want run fence miss")
	}
	assertSQLiteTerminateRollbackState(t, ctx, store, run.ID, wakeupID)
}

func assertSQLiteTerminateRollbackState(t *testing.T, ctx context.Context, store *store, runID, wakeupID int64) {
	t.Helper()
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-terminate-fence", runID, "root"); got != "ready" {
		t.Fatalf("root status after rollback = %q, want ready", got)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-terminate-fence", runID, "child"); got != "pending" {
		t.Fatalf("child status after rollback = %q, want pending", got)
	}
	persisted, err := store.GetRun(ctx, "run-terminate-fence")
	if err != nil || persisted.Status != "running" {
		t.Fatalf("GetRun() status=%v error=%v, want running/nil", persisted, err)
	}
	wakeup, err := store.GetWakeup(ctx, wakeupID)
	if err != nil || wakeup.Status != "pending" || wakeup.LastError != "" {
		t.Fatalf("GetWakeup() wakeup=%+v error=%v, want pending unchanged", wakeup, err)
	}
}
