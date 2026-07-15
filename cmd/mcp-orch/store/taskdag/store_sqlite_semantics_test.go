package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSQLiteDeleteDAGBlocksActiveRun(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteCoreDAG(t, ctx, store)
	createSQLiteTaskDAGRun(t, ctx, store, "run-delete-active", "dag-core")

	rows, err := store.DeleteDAG(ctx, "dag-core")
	if !errors.Is(err, ErrDAGDeleteActiveRun) || rows != 0 {
		t.Fatalf("DeleteDAG() rows=%d error=%v, want 0/ErrDAGDeleteActiveRun", rows, err)
	}
	if _, err := store.GetDAG(ctx, "dag-core"); err != nil {
		t.Fatalf("GetDAG() after rejected delete error = %v", err)
	}
}

func TestFinalizedRunStatusPriorityMatrix(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     string
		ready    bool
	}{
		{name: "failed dominates cancelled", statuses: []string{"done", "cancelled", "failed"}, want: "failed", ready: true},
		{name: "cancelled without failure", statuses: []string{"done", "skipped", "cancelled"}, want: "cancelled", ready: true},
		{name: "done and skipped succeed", statuses: []string{"done", "skipped"}, want: "succeeded", ready: true},
		{name: "nonterminal blocks finalization", statuses: []string{"done", "running"}},
		{name: "empty blocks finalization"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ready := finalizedRunStatus(test.statuses)
			if got != test.want || ready != test.ready {
				t.Fatalf("finalizedRunStatus(%v) = %q/%v, want %q/%v", test.statuses, got, ready, test.want, test.ready)
			}
		})
	}
}

func TestSQLiteScheduleRootWakeupsRequiresDispatchIdentityAndCWD(t *testing.T) {
	tests := []struct {
		name       string
		config     json.RawMessage
		wantRows   int64
		wantReason string
	}{
		{name: "missing cwd", config: json.RawMessage(`{"exec":{"agent_key":"alpha"}}`), wantReason: "exec.cwd"},
		{name: "missing identity", config: json.RawMessage(`{"exec":{"cwd":"/tmp/node-cwd"}}`), wantReason: "agent_key"},
		{name: "complete config", config: json.RawMessage(`{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"}}`), wantRows: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := openTaskDAGSQLiteDB(t)
			store := NewStore(db).(*store)
			dagKey := "dag-root-guard"
			if _, err := store.UpsertDAG(ctx, DAG{DagKey: dagKey, Title: "root guard", Status: "draft", CreatedBy: "tester", Metadata: json.RawMessage(`{}`)}); err != nil {
				t.Fatalf("UpsertDAG() error = %v", err)
			}
			if _, err := store.UpsertNode(ctx, Node{DagKey: dagKey, NodeKey: "root", Title: "root", NodeType: "agent", DependsOn: json.RawMessage(`[]`), Config: test.config}); err != nil {
				t.Fatalf("UpsertNode() error = %v", err)
			}
			run := createSQLiteTaskDAGRun(t, ctx, store, "run-root-guard", dagKey)
			cloneAndPromoteSQLiteRun(t, ctx, store, dagKey, run.ID)
			if _, err := store.AssignNode(ctx, AssignNodeInput{DagKey: dagKey, NodeKey: "root", RunID: run.ID, AssignedTo: "agent-root"}); err != nil {
				t.Fatalf("AssignNode() error = %v", err)
			}

			rows, err := store.ScheduleRootWakeups(ctx, dagKey, run.ID)
			if err != nil || rows != test.wantRows {
				t.Fatalf("ScheduleRootWakeups() rows=%d error=%v, want %d/nil", rows, err, test.wantRows)
			}
			assertSQLiteRootWakeupGuardState(t, ctx, db, run.ID, test.wantRows, test.wantReason)
		})
	}
}

func assertSQLiteRootWakeupGuardState(t *testing.T, ctx context.Context, db *sql.DB, runID, wantWakeups int64, wantReason string) {
	t.Helper()
	var wakeups int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dag_wakeups WHERE run_id = ?`, runID).Scan(&wakeups); err != nil {
		t.Fatalf("count root wakeups: %v", err)
	}
	if wakeups != wantWakeups {
		t.Fatalf("root wakeups = %d, want %d", wakeups, wantWakeups)
	}
	if wantReason == "" {
		return
	}
	var events string
	if err := db.QueryRowContext(ctx, `SELECT events FROM task_dag_runs WHERE id = ?`, runID).Scan(&events); err != nil {
		t.Fatalf("read dispatch blocked events: %v", err)
	}
	if !strings.Contains(events, wantReason) {
		t.Fatalf("dispatch blocked events = %s, want reason containing %q", events, wantReason)
	}
}
