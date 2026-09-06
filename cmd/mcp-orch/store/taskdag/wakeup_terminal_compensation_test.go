package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestClaimDueAllowsTerminalFailureCompensationAfterRunTerminal(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-terminal-compensation", "dag-terminal-compensation")
	markRunTerminal(t, ctx, db, run.ID)
	enqueueTerminalCompensation(t, ctx, store, run.ID)
	claimed, err := store.ClaimDueWakeups(ctx, ClaimDueWakeupsInput{ClaimedBy: "compensation-worker", LeaseInterval: "30s", Limit: 10})
	if err != nil {
		t.Fatalf("claim terminal compensation: %v", err)
	}
	assertTerminalCompensationClaim(t, claimed)
	assertTerminalCompensationStatus(t, ctx, db)
}

func markRunTerminal(t *testing.T, ctx context.Context, db *sql.DB, runID int64) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `UPDATE task_dag_runs SET status = 'failed', finished_at = 2 WHERE id = ?`, runID); err != nil {
		t.Fatalf("mark run terminal: %v", err)
	}
}

func enqueueTerminalCompensation(t *testing.T, ctx context.Context, store *store, runID int64) {
	t.Helper()
	compensation, err := json.Marshal(map[string]any{"reason": "node failed", "store_error": "write failed", "fail_fast": false})
	if err != nil {
		t.Fatalf("marshal compensation: %v", err)
	}
	if _, err := store.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey: "dag-terminal-compensation", NodeKey: "root", RunID: runID,
		WakeupKind: "terminal_failure_compensation", TargetAgentID: "mcp-orch",
		PromptPayload: compensation, IdempotencyKey: "terminal-compensation-terminal-run",
	}); err != nil {
		t.Fatalf("enqueue terminal compensation: %v", err)
	}
}

func assertTerminalCompensationClaim(t *testing.T, claimed []Wakeup) {
	t.Helper()
	if len(claimed) != 1 || claimed[0].WakeupKind != "terminal_failure_compensation" {
		t.Fatalf("claimed = %+v, want one terminal compensation", claimed)
	}
	if claimed[0].PayloadSchemaVersion != WakeupPayloadSchemaVersion1 {
		t.Fatalf("claimed payload schema version = %d, want 1", claimed[0].PayloadSchemaVersion)
	}
}

func assertTerminalCompensationStatus(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM task_dag_wakeups WHERE idempotency_key = ?`, "terminal-compensation-terminal-run").Scan(&status); err != nil {
		t.Fatalf("read compensation status: %v", err)
	}
	if status != "dispatching" {
		t.Fatalf("compensation status = %q, want dispatching", status)
	}
	var ordinary int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dag_wakeups WHERE wakeup_kind = 'node_start' AND status = 'dispatching'`).Scan(&ordinary); err != nil {
		t.Fatalf("count ordinary terminal wakeups: %v", err)
	}
	if ordinary != 0 {
		t.Fatalf("ordinary terminal wakeups claimed = %d, want 0", ordinary)
	}
}
