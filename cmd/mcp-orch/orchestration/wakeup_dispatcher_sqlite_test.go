package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

func TestWakeupDispatcherSQLiteReclaimedTerminalNodeSkipsSideEffect(t *testing.T) {
	ctx := context.Background()
	db := openOrchestrationSQLiteDB(t)
	store := taskdag.NewStore(db).(sqliteWakeupTerminalStore)
	wakeupID := seedSQLiteReclaimedTerminalWakeup(t, ctx, db, store)

	runner := &terminalWakeupAutomationRunner{}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-b", LeaseInterval: "30s"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher() error = %v", err)
	}
	d.WithNodeRouter(NewNodeExecutorRouter(store, nil, nodeexec.NewAutomationExecutor(terminalWakeupAutomationGetter{}, runner), nil, nil, nil))
	requireSQLiteTerminalWakeupDispatchSkipped(t, ctx, d, store, wakeupID, runner)
}

type sqliteWakeupTerminalStore interface {
	taskdag.Store
	taskdag.RunStore
}

func seedSQLiteReclaimedTerminalWakeup(t *testing.T, ctx context.Context, db *sql.DB, store sqliteWakeupTerminalStore) int64 {
	t.Helper()
	seedSQLiteWakeupTerminalDAG(t, ctx, store)
	run := createSQLiteWakeupTerminalRun(t, ctx, store)
	cloneSQLiteWakeupTerminalRun(t, ctx, store, run.ID)
	wakeupID := enqueueSQLiteWakeupTerminalNode(t, ctx, store, run.ID)
	claimAndReclaimSQLiteTerminalWakeup(t, ctx, db, store, run.ID)
	return wakeupID
}

func createSQLiteWakeupTerminalRun(t *testing.T, ctx context.Context, store sqliteWakeupTerminalStore) *taskdag.Run {
	t.Helper()
	run, err := store.CreateRun(ctx, taskdag.CreateRunInput{RunKey: "run-wakeup-terminal", DagKey: "dag-wakeup-terminal", DagVersionSnapshot: 0, TriggerSource: "manual", Metadata: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	return run
}

func cloneSQLiteWakeupTerminalRun(t *testing.T, ctx context.Context, store sqliteWakeupTerminalStore, runID int64) {
	t.Helper()
	if rows, err := store.CloneNodesForRun(ctx, "dag-wakeup-terminal", runID); err != nil || rows == 0 {
		t.Fatalf("CloneNodesForRun() rows=%d error=%v, want rows/nil", rows, err)
	}
	if rows, err := store.PromoteRootNodesToReady(ctx, "dag-wakeup-terminal", runID); err != nil || rows != 2 {
		t.Fatalf("PromoteRootNodesToReady() rows=%d error=%v, want 2/nil", rows, err)
	}
}

func enqueueSQLiteWakeupTerminalNode(t *testing.T, ctx context.Context, store sqliteWakeupTerminalStore, runID int64) int64 {
	t.Helper()
	wakeupID, err := store.EnqueueWakeup(ctx, taskdag.EnqueueWakeupInput{
		DagKey:         "dag-wakeup-terminal",
		NodeKey:        "auto",
		RunID:          runID,
		WakeupKind:     "node_start",
		TargetAgentID:  "auto-agent",
		PromptPayload:  json.RawMessage(`{}`),
		IdempotencyKey: "dag-wakeup-terminal:auto:start",
	})
	if err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}
	return wakeupID
}

func claimAndReclaimSQLiteTerminalWakeup(t *testing.T, ctx context.Context, db *sql.DB, store sqliteWakeupTerminalStore, runID int64) {
	t.Helper()
	claimed, err := store.ClaimDueWakeups(ctx, taskdag.ClaimDueWakeupsInput{ClaimedBy: "worker-a", LeaseInterval: "1ms", Limit: 1})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimDueWakeups(first) len=%d err=%v, want 1/nil", len(claimed), err)
	}
	expireSQLiteWakeupLease(t, ctx, db, claimed[0].ID)
	if _, err := store.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput{DagKey: "dag-wakeup-terminal", NodeKey: "auto", RunID: runID, Status: "done", Result: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatalf("CompleteNodeAndScheduleDownstream(auto) error = %v", err)
	}
	if rows, err := store.ReclaimStaleDispatchingWakeups(ctx); err != nil || rows != 1 {
		t.Fatalf("ReclaimStaleDispatchingWakeups() rows=%d err=%v, want 1/nil", rows, err)
	}
}

func expireSQLiteWakeupLease(t *testing.T, ctx context.Context, db *sql.DB, wakeupID int64) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `UPDATE task_dag_wakeups SET lease_expires_at = 0 WHERE id = ?`, wakeupID); err != nil {
		t.Fatalf("expire wakeup lease: %v", err)
	}
}

func requireSQLiteTerminalWakeupDispatchSkipped(t *testing.T, ctx context.Context, d *WakeupDispatcher, store taskdag.Store, wakeupID int64, runner *terminalWakeupAutomationRunner) {
	t.Helper()
	handled, err := d.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch() error = %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("automation side effects = %d, want 0 for reclaimed terminal node", runner.calls)
	}
	if handled != 1 {
		t.Fatalf("ProcessBatch handled=%d, want 1", handled)
	}
	got, err := store.GetWakeup(ctx, wakeupID)
	if err != nil {
		t.Fatalf("GetWakeup() error = %v", err)
	}
	if got.Status != "sent" {
		t.Fatalf("wakeup status = %q, want sent", got.Status)
	}
}

func seedSQLiteWakeupTerminalDAG(t *testing.T, ctx context.Context, store taskdag.Store) {
	t.Helper()
	root := t.TempDir()
	if _, err := store.UpsertDAG(ctx, taskdag.DAG{DagKey: "dag-wakeup-terminal", Title: "wakeup terminal", Status: "draft", CreatedBy: "tester", Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}
	autoConfig, err := json.Marshal(map[string]any{
		"exec": map[string]any{
			"kind":            "command_card",
			"command_ref":     "already-ran",
			"cwd":             root,
			"workspace_roots": []string{root},
		},
	})
	if err != nil {
		t.Fatalf("marshal auto config: %v", err)
	}
	if _, err := store.UpsertNode(ctx, taskdag.Node{DagKey: "dag-wakeup-terminal", NodeKey: "auto", Title: "Auto", NodeType: "automation", DependsOn: json.RawMessage(`[]`), Config: autoConfig}); err != nil {
		t.Fatalf("UpsertNode(auto) error = %v", err)
	}
	if _, err := store.UpsertNode(ctx, taskdag.Node{DagKey: "dag-wakeup-terminal", NodeKey: "hold", Title: "Hold", NodeType: "agent", DependsOn: json.RawMessage(`[]`), Config: json.RawMessage(`{"exec":{"provider":"claude","agent_key":"hold","prompt_key":"main/hold","cwd":"` + root + `"},"first_turn":"hold"}`)}); err != nil {
		t.Fatalf("UpsertNode(hold) error = %v", err)
	}
}
