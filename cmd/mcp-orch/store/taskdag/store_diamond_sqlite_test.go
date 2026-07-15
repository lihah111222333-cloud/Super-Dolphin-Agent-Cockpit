package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
)

type sqliteDiamondCompletion struct {
	result *CompleteNodeWithDownstreamResult
	err    error
}

func TestSQLiteTaskDAGConcurrentDiamondConvergesOnSingleWakeup(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteConcurrentDiamondTemplate(t, ctx, store, "dag-diamond")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-diamond", "dag-diamond")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-diamond", run.ID)

	start := make(chan struct{})
	results := make(chan sqliteDiamondCompletion, 2)
	var workers sync.WaitGroup
	for _, nodeKey := range []string{"left", "right"} {
		nodeKey := nodeKey
		workers.Go(func() {
			<-start
			result, err := store.CompleteNodeAndScheduleDownstream(ctx, CompleteNodeInput{
				DagKey: "dag-diamond", NodeKey: nodeKey, RunID: run.ID, Status: "done", Result: json.RawMessage(`{"ok":true}`),
			})
			results <- sqliteDiamondCompletion{result: result, err: err}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	assertSQLiteDiamondCompletion(t, ctx, db, store, run.ID, results)
}

func assertSQLiteDiamondCompletion(t *testing.T, ctx context.Context, db *sql.DB, store *store, runID int64, results <-chan sqliteDiamondCompletion) {
	t.Helper()
	scheduled := 0
	for completed := range results {
		if completed.err != nil || completed.result == nil {
			t.Fatalf("concurrent completion result=%+v error=%v", completed.result, completed.err)
		}
		scheduled += len(completed.result.ScheduledDownstream)
	}
	if scheduled != 1 {
		t.Fatalf("scheduled downstream count = %d, want exactly 1", scheduled)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-diamond", runID, "join"); got != "ready" {
		t.Fatalf("join status = %q, want ready", got)
	}
	var wakeups int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dag_wakeups WHERE dag_key = ? AND run_id = ? AND node_key = ?`, "dag-diamond", runID, "join").Scan(&wakeups); err != nil || wakeups != 1 {
		t.Fatalf("join wakeup count = %d error=%v, want 1/nil", wakeups, err)
	}
}

func seedSQLiteConcurrentDiamondTemplate(t *testing.T, ctx context.Context, store *store, dagKey string) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: dagKey, Title: "diamond", Status: "draft", CreatedBy: "tester", Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG(%s) error = %v", dagKey, err)
	}
	nodes := []Node{
		{DagKey: dagKey, NodeKey: "left", Title: "left", NodeType: "agent", DependsOn: json.RawMessage(`[]`), Config: json.RawMessage(`{"node":"left"}`)},
		{DagKey: dagKey, NodeKey: "right", Title: "right", NodeType: "agent", DependsOn: json.RawMessage(`[]`), Config: json.RawMessage(`{"node":"right"}`)},
		{DagKey: dagKey, NodeKey: "join", Title: "join", NodeType: "agent", AssignedTo: "agent-join", DependsOn: json.RawMessage(`["left","right"]`), Config: json.RawMessage(`{"exec":{"agent_key":"joiner","cwd":"/tmp/node-cwd"}}`)},
	}
	for _, node := range nodes {
		if _, err := store.UpsertNode(ctx, node); err != nil {
			t.Fatalf("UpsertNode(%s) error = %v", node.NodeKey, err)
		}
	}
}
