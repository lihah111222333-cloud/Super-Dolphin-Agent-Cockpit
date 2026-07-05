package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

func TestSQLiteTaskDAGCoreCRUDListUpdateDelete(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)

	seedSQLiteCoreDAG(t, ctx, store)
	assertSQLiteCoreDAGList(t, ctx, store)
	assertSQLiteCoreDAGUpdate(t, ctx, store)
	assertSQLiteCoreDAGDelete(t, ctx, store)
}

func TestAssignNodeAndEnqueueWakeupRollsBackAssignmentWhenWakeupEnqueueFails(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteCoreDAG(t, ctx, store)
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-assign-rollback", "dag-core")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-core", run.ID)

	_, err := store.AssignNodeAndEnqueueWakeup(ctx, AssignNodeAndEnqueueWakeupInput{
		Assign: AssignNodeInput{DagKey: "dag-core", NodeKey: "root", RunID: run.ID, AssignedTo: "agent-alpha"},
		Wakeup: EnqueueWakeupInput{
			DagKey:         "dag-core",
			NodeKey:        "root",
			RunID:          run.ID,
			WakeupKind:     "manual_dispatch",
			TargetAgentID:  "agent-alpha",
			PromptPayload:  json.RawMessage(`{`),
			IdempotencyKey: "manual_dispatch:dag-core:rollback:root:agent-alpha",
		},
	})
	if err == nil {
		t.Fatal("AssignNodeAndEnqueueWakeup() error = nil, want enqueue JSON failure")
	}
	nodes, listErr := store.ListRunNodes(ctx, "dag-core", run.ID)
	if listErr != nil {
		t.Fatalf("ListRunNodes() error = %v", listErr)
	}
	if len(nodes) != 1 || nodes[0].AssignedTo != "" {
		t.Fatalf("runtime node after failed assign+enqueue = %+v, want assigned_to rolled back", nodes)
	}
}

func TestMarkDispatchIncompleteIfMissingWakeupTreatsSentUnboundAsActive(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteCoreDAG(t, ctx, store)
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-sent-unbound", "dag-core")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-core", run.ID)
	result, err := store.AssignNodeAndEnqueueWakeup(ctx, AssignNodeAndEnqueueWakeupInput{
		Assign: AssignNodeInput{DagKey: "dag-core", NodeKey: "root", RunID: run.ID, AssignedTo: "agent-alpha"},
		Wakeup: EnqueueWakeupInput{
			DagKey:         "dag-core",
			NodeKey:        "root",
			RunID:          run.ID,
			WakeupKind:     "manual_dispatch",
			TargetAgentID:  "agent-alpha",
			PromptPayload:  json.RawMessage(`{"prompt":"dispatch"}`),
			IdempotencyKey: "manual_dispatch:dag-core:sent-unbound:root:agent-alpha",
		},
	})
	if err != nil {
		t.Fatalf("AssignNodeAndEnqueueWakeup() error = %v", err)
	}
	markSQLiteWakeupSent(t, ctx, store, result.WakeupID)

	marked, err := store.MarkDispatchIncompleteIfMissingWakeup(ctx, MarkDispatchIncompleteInput{
		DagKey: "dag-core", NodeKey: "root", RunID: run.ID, AssignedTo: "agent-alpha",
	})
	if err != nil {
		t.Fatalf("MarkDispatchIncompleteIfMissingWakeup() error = %v", err)
	}
	if marked.Marked || !marked.ActiveWakeup {
		t.Fatalf("dispatch mark = %+v, want active sent-unbound wakeup to block marking", marked)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-core", run.ID, "root"); got != "ready" {
		t.Fatalf("node status = %q, want ready while sent-unbound wakeup is active", got)
	}
}

func TestMarkDispatchIncompleteIfMissingWakeupIsRunFenced(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteCoreDAG(t, ctx, store)
	runA := createSQLiteTaskDAGRun(t, ctx, store, "run-active", "dag-core")
	runB := createSQLiteTaskDAGRun(t, ctx, store, "run-missing-wakeup", "dag-core")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-core", runA.ID)
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-core", runB.ID)
	if _, err := store.AssignNodeAndEnqueueWakeup(ctx, AssignNodeAndEnqueueWakeupInput{
		Assign: AssignNodeInput{DagKey: "dag-core", NodeKey: "root", RunID: runA.ID, AssignedTo: "agent-alpha"},
		Wakeup: EnqueueWakeupInput{
			DagKey:         "dag-core",
			NodeKey:        "root",
			RunID:          runA.ID,
			WakeupKind:     "manual_dispatch",
			TargetAgentID:  "agent-alpha",
			PromptPayload:  json.RawMessage(`{"prompt":"dispatch"}`),
			IdempotencyKey: "manual_dispatch:dag-core:run-active:root:agent-alpha",
		},
	}); err != nil {
		t.Fatalf("AssignNodeAndEnqueueWakeup(runA) error = %v", err)
	}
	if _, err := store.AssignNode(ctx, AssignNodeInput{DagKey: "dag-core", NodeKey: "root", RunID: runB.ID, AssignedTo: "agent-alpha"}); err != nil {
		t.Fatalf("AssignNode(runB) error = %v", err)
	}

	marked, err := store.MarkDispatchIncompleteIfMissingWakeup(ctx, MarkDispatchIncompleteInput{
		DagKey: "dag-core", NodeKey: "root", RunID: runB.ID, AssignedTo: "agent-alpha",
	})
	if err != nil {
		t.Fatalf("MarkDispatchIncompleteIfMissingWakeup() error = %v", err)
	}
	if !marked.Marked || marked.ActiveWakeup {
		t.Fatalf("dispatch mark = %+v, want runB marked despite runA active wakeup", marked)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-core", runB.ID, "root"); got != "dispatch_incomplete" {
		t.Fatalf("runB node status = %q, want dispatch_incomplete", got)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-core", runA.ID, "root"); got != "ready" {
		t.Fatalf("runA node status = %q, want ready", got)
	}
}

func seedSQLiteCoreDAG(t *testing.T, ctx context.Context, store *store) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: "dag-core", Title: "Core DAG", Description: "search target", Status: "draft", CreatedBy: "tester", Metadata: []byte(`{"full":true}`)}); err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}
	if _, err := store.UpsertNode(ctx, Node{DagKey: "dag-core", NodeKey: "root", Title: "Root", NodeType: "agent", DependsOn: []byte(`[]`), Config: []byte(`{"root":true}`)}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
}

func assertSQLiteCoreDAGList(t *testing.T, ctx context.Context, store *store) {
	t.Helper()
	listed, err := store.ListDAGs(ctx, ListDAGsFilter{Keyword: "search", Limit: 10})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	if len(listed) != 1 || listed[0].DagKey != "dag-core" {
		t.Fatalf("ListDAGs() = %#v, want dag-core", listed)
	}
}

func assertSQLiteCoreDAGUpdate(t *testing.T, ctx context.Context, store *store) {
	t.Helper()
	nextTitle := "Updated Core DAG"
	if rows, err := store.UpdateDAGPatch(ctx, UpdateDAGPatchInput{DagKey: "dag-core", Title: &nextTitle}); err != nil || rows != 1 {
		t.Fatalf("UpdateDAGPatch() rows=%d error=%v, want 1/nil", rows, err)
	}
	gotDAG, err := store.GetDAG(ctx, "dag-core")
	if err != nil {
		t.Fatalf("GetDAG() error = %v", err)
	}
	if gotDAG.Title != nextTitle || string(gotDAG.Metadata) != `{"full":true}` {
		t.Fatalf("GetDAG() title=%q metadata=%s, want updated title/full metadata", gotDAG.Title, gotDAG.Metadata)
	}
}

func assertSQLiteCoreDAGDelete(t *testing.T, ctx context.Context, store *store) {
	t.Helper()
	if rows, err := store.DeleteNode(ctx, "dag-core", "root"); err != nil || rows != 1 {
		t.Fatalf("DeleteNode() rows=%d error=%v, want 1/nil", rows, err)
	}
	nodes, err := store.ListNodes(ctx, "dag-core")
	if err != nil {
		t.Fatalf("ListNodes() after delete error = %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("ListNodes() after delete = %#v, want empty", nodes)
	}
	if rows, err := store.DeleteDAG(ctx, "dag-core"); err != nil || rows != 1 {
		t.Fatalf("DeleteDAG() rows=%d error=%v, want 1/nil", rows, err)
	}
	if _, err := store.GetDAG(ctx, "dag-core"); !platformdb.IsNotFound(err) {
		t.Fatalf("GetDAG() after delete error = %v, want not found", err)
	}
}

func TestSQLiteTaskDAGCompleteDownstreamAndFinalizeRun(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteFlowTemplate(t, ctx, store, "dag-flow")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-flow", "dag-flow")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-flow", run.ID)

	first, err := store.CompleteNodeAndScheduleDownstream(ctx, CompleteNodeInput{DagKey: "dag-flow", NodeKey: "root", RunID: run.ID, Status: "done", Result: []byte(`{"root":true}`)})
	if err != nil {
		t.Fatalf("CompleteNodeAndScheduleDownstream(root) error = %v", err)
	}
	if first.FinalizedRun != nil {
		t.Fatalf("root completion finalized run = %#v, want nil while child pending", first.FinalizedRun)
	}
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-flow", run.ID, "child"); got != "ready" {
		t.Fatalf("child status after root done = %q, want ready", got)
	}

	second, err := store.CompleteNodeAndScheduleDownstream(ctx, CompleteNodeInput{DagKey: "dag-flow", NodeKey: "child", RunID: run.ID, Status: "done", Result: []byte(`{"child":true}`)})
	if err != nil {
		t.Fatalf("CompleteNodeAndScheduleDownstream(child) error = %v", err)
	}
	if second.FinalizedRun == nil || second.FinalizedRun.RunKey != "run-flow" || second.FinalizedRun.Status != "succeeded" {
		t.Fatalf("child completion finalized run = %#v, want run-flow/succeeded", second.FinalizedRun)
	}
	persisted, err := store.GetRun(ctx, "run-flow")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if persisted.Status != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", persisted.Status)
	}
}

func TestSQLiteTaskDAGFinalizeRunWritesFinalOutputMetadata(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteFinalOutputTemplate(t, ctx, store, "dag-final")
	runID := insertSQLiteTaskDAGRun(t, ctx, db, "run-final", "dag-final", `{"request_id":"req-1"}`)
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-final", runID)

	if _, err := store.CompleteNodeAndScheduleDownstream(ctx, CompleteNodeInput{DagKey: "dag-final", NodeKey: "root", RunID: runID, Status: "done", Result: []byte(`{"root":true}`)}); err != nil {
		t.Fatalf("CompleteNodeAndScheduleDownstream(root) error = %v", err)
	}
	result, err := store.CompleteNodeAndScheduleDownstream(ctx, CompleteNodeInput{
		DagKey:  "dag-final",
		NodeKey: "child",
		RunID:   runID,
		Status:  "done",
		Result:  []byte(`{"sharedfile":{"path":"reports/final.md"}}`),
	})
	if err != nil {
		t.Fatalf("CompleteNodeAndScheduleDownstream(child) error = %v", err)
	}
	if result.FinalizedRun == nil || result.FinalizedRun.Status != "succeeded" {
		t.Fatalf("FinalizedRun = %#v, want succeeded", result.FinalizedRun)
	}
	persisted, err := store.GetRun(ctx, "run-final")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	assertSQLiteFinalOutputMetadata(t, persisted.Metadata)
}

func TestSQLiteTaskDAGFailCascadeFinalizesRun(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteCascadeTemplate(t, ctx, store, "dag-fail")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-fail", "dag-fail")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-fail", run.ID)

	result, err := store.FailNodeAndCancelDownstream(ctx, FailNodeInput{DagKey: "dag-fail", NodeKey: "root", RunID: run.ID, Reason: "boom", FailFast: true})
	if err != nil {
		t.Fatalf("FailNodeAndCancelDownstream() error = %v", err)
	}
	if result.Node == nil || result.Node.Status != "failed" {
		t.Fatalf("failed node = %#v, want root failed", result.Node)
	}
	if len(result.CanceledDownstream) != 2 {
		t.Fatalf("CanceledDownstream = %#v, want child and leaf", result.CanceledDownstream)
	}
	if result.FinalizedRun == nil || result.FinalizedRun.Status != "failed" {
		t.Fatalf("FinalizedRun = %#v, want failed", result.FinalizedRun)
	}
	for _, nodeKey := range []string{"root", "child", "leaf"} {
		if got := sqliteRunNodeStatus(t, ctx, store, "dag-fail", run.ID, nodeKey); got != "failed" {
			t.Fatalf("%s status = %q, want failed", nodeKey, got)
		}
	}
}

func TestSQLiteTaskDAGFailFastFalseTerminalizesBlockedChainAndDiamond(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteBlockedDiamondTemplate(t, ctx, store, "dag-blocked")
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-blocked", "dag-blocked")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-blocked", run.ID)

	result, err := store.FailNodeAndCancelDownstream(ctx, FailNodeInput{DagKey: "dag-blocked", NodeKey: "A", RunID: run.ID, Reason: "ancestor failed"})
	if err != nil {
		t.Fatalf("FailNodeAndCancelDownstream() error = %v", err)
	}
	if result.Node == nil || result.Node.Status != "failed" {
		t.Fatalf("failed node = %#v, want A failed", result.Node)
	}
	if result.FinalizedRun == nil || result.FinalizedRun.Status != "failed" {
		t.Fatalf("FinalizedRun = %#v, want failed after all blocked downstream terminalized", result.FinalizedRun)
	}
	for _, nodeKey := range []string{"A", "B", "C", "D", "E"} {
		if got := sqliteRunNodeStatus(t, ctx, store, "dag-blocked", run.ID, nodeKey); got != "failed" {
			t.Fatalf("%s status = %q, want failed", nodeKey, got)
		}
	}
}

func TestSQLiteTaskDAGRecordNodeSpawnAndLookupAreRunFenced(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteTaskDAGTemplate(t, ctx, store)
	runA := createSQLiteTaskDAGRun(t, ctx, store, "run-spawn-a", "dag-multi")
	runB := createSQLiteTaskDAGRun(t, ctx, store, "run-spawn-b", "dag-multi")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-multi", runA.ID)
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-multi", runB.ID)

	first, err := store.RecordNodeSpawn(ctx, RecordNodeSpawnInput{DagKey: "dag-multi", NodeKey: "root", RunID: runA.ID, ThreadID: "thread-a"})
	if err != nil {
		t.Fatalf("RecordNodeSpawn(first) error = %v", err)
	}
	assertSQLiteFirstSpawn(t, first)
	second, err := store.RecordNodeSpawn(ctx, RecordNodeSpawnInput{DagKey: "dag-multi", NodeKey: "root", RunID: runA.ID, ThreadID: "thread-a-retry"})
	if err != nil {
		t.Fatalf("RecordNodeSpawn(retry) error = %v", err)
	}
	assertSQLiteRetrySpawn(t, second)
	matches, err := store.LookupNodesBySpawningThread(ctx, "thread-a-retry")
	if err != nil {
		t.Fatalf("LookupNodesBySpawningThread() error = %v", err)
	}
	assertSQLiteSpawnLookup(t, matches, runA.ID)
	if got := sqliteRunNodeStatus(t, ctx, store, "dag-multi", runB.ID, "root"); got != "ready" {
		t.Fatalf("run B root status = %q, want ready and untouched", got)
	}
}

// TestRecordNodeSpawnRequiresWakeupFence verifies dispatch-scoped spawn writes reject missing lease fences.
func TestRecordNodeSpawnRequiresWakeupFence(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteTaskDAGTemplate(t, ctx, store)
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-spawn-fence", "dag-multi")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-multi", run.ID)
	wakeupID, err := store.EnqueueWakeup(ctx, EnqueueWakeupInput{
		DagKey:         "dag-multi",
		NodeKey:        "root",
		RunID:          run.ID,
		WakeupKind:     "node_start",
		TargetAgentID:  "agent-alpha",
		PromptPayload:  json.RawMessage(`{"prompt":"start"}`),
		IdempotencyKey: "node_start:dag-multi:run-spawn-fence:root",
	})
	if err != nil {
		t.Fatalf("EnqueueWakeup() error = %v", err)
	}
	if _, err := store.RecordNodeSpawn(ctx, RecordNodeSpawnInput{
		DagKey:   "dag-multi",
		NodeKey:  "root",
		RunID:    run.ID,
		ThreadID: "thread-unfenced",
		WakeupID: wakeupID,
	}); err == nil {
		t.Fatal("RecordNodeSpawn() error = nil, want missing wakeup fence rejection")
	}
	if got := sqliteRunNodeSpawningThread(t, ctx, store, "dag-multi", run.ID, "root"); got != "" {
		t.Fatalf("spawning_thread_id = %q, want empty after rejected unfenced spawn", got)
	}
}

func assertSQLiteFirstSpawn(t *testing.T, got *RecordNodeSpawnResult) {
	t.Helper()
	if got == nil {
		t.Fatal("first spawn result is nil")
	}
	if got.AppendedEvent {
		t.Fatalf("first spawn AppendedEvent = true, want false")
	}
}

func assertSQLiteRetrySpawn(t *testing.T, got *RecordNodeSpawnResult) {
	t.Helper()
	if got == nil {
		t.Fatal("retry spawn result is nil")
	}
	if !got.AppendedEvent || got.PreviousThreadID != "thread-a" || got.RunKey != "run-spawn-a" {
		t.Fatalf("retry spawn result = %#v, want appended event on run-spawn-a with previous thread-a", got)
	}
}

func assertSQLiteSpawnLookup(t *testing.T, got []Node, wantRunID int64) {
	t.Helper()
	if len(got) != 1 || got[0].RunID == nil || *got[0].RunID != wantRunID {
		t.Fatalf("lookup matches = %#v, want only run A", got)
	}
}

func TestSQLiteTaskDAGMultiRunIsolationUsesRunIDFence(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)

	seedSQLiteTaskDAGTemplate(t, ctx, store)
	runAID := insertSQLiteTaskDAGRun(t, ctx, db, "run-a", "dag-multi", `{"run":"a"}`)
	runBID := insertSQLiteTaskDAGRun(t, ctx, db, "run-b", "dag-multi", `{"run":"b"}`)
	cloneSQLiteRunNodes(t, ctx, store, runAID, "run-a")
	cloneSQLiteRunNodes(t, ctx, store, runBID, "run-b")
	completeSQLiteRunNode(t, ctx, store, runAID)
	assertSQLiteRunningRuns(t, ctx, store, "dag-multi", 2)
	assertSQLiteRunNodeStatus(t, ctx, store, runAID, "run-a", "done")
	assertSQLiteRunNodeStatus(t, ctx, store, runBID, "run-b", "pending")
}

func seedSQLiteTaskDAGTemplate(t *testing.T, ctx context.Context, store *store) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: "dag-multi", Title: "multi", Status: "draft", CreatedBy: "tester", Metadata: []byte(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}
	if _, err := store.UpsertNode(ctx, Node{DagKey: "dag-multi", NodeKey: "root", Title: "root", NodeType: "agent", DependsOn: []byte(`[]`), Config: []byte(`{"template":true}`)}); err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
}

func cloneSQLiteRunNodes(t *testing.T, ctx context.Context, store *store, runID int64, label string) {
	t.Helper()
	if _, err := store.CloneNodesForRun(ctx, "dag-multi", runID); err != nil {
		t.Fatalf("CloneNodesForRun(%s) error = %v", label, err)
	}
}

func completeSQLiteRunNode(t *testing.T, ctx context.Context, store *store, runID int64) {
	t.Helper()
	if _, err := store.UpdateNodeStatus(ctx, NodeStatusUpdate{DagKey: "dag-multi", NodeKey: "root", RunID: runID, ExpectedStatus: "pending", Status: "done", Result: []byte(`{"ok":true}`)}); err != nil {
		t.Fatalf("UpdateNodeStatus(run-a) error = %v", err)
	}
}

func assertSQLiteRunningRuns(t *testing.T, ctx context.Context, store *store, dagKey string, want int64) {
	t.Helper()
	running, err := store.CountRunningRunsByDagKey(ctx, dagKey)
	if err != nil {
		t.Fatalf("CountRunningRunsByDagKey() error = %v", err)
	}
	if running != want {
		t.Fatalf("running runs = %d, want %d", running, want)
	}
}

func assertSQLiteRunNodeStatus(t *testing.T, ctx context.Context, store *store, runID int64, label, want string) {
	t.Helper()
	nodes, err := store.ListRunNodes(ctx, "dag-multi", runID)
	if err != nil {
		t.Fatalf("ListRunNodes(%s) error = %v", label, err)
	}
	if got := singleNodeStatus(t, nodes); got != want {
		t.Fatalf("%s node status = %q, want %s", label, got, want)
	}
}

func singleNodeStatus(t *testing.T, nodes []Node) string {
	t.Helper()
	if len(nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(nodes))
	}
	return nodes[0].Status
}

func insertSQLiteTaskDAGRun(t *testing.T, ctx context.Context, db *sql.DB, runKey, dagKey, metadata string) int64 {
	t.Helper()
	now := time.Now().UTC().UnixMilli()
	res, err := db.ExecContext(ctx, `
INSERT INTO task_dag_runs (
	run_key, dag_key, dag_version_snapshot, trigger_source, status,
	started_at, metadata, created_at, updated_at
) VALUES (?, ?, 0, 'manual', 'running', ?, ?, ?, ?)`,
		runKey, dagKey, now, metadata, now, now)
	if err != nil {
		t.Fatalf("insert task_dag_run %s: %v", runKey, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id for %s: %v", runKey, err)
	}
	return id
}

func seedSQLiteFlowTemplate(t *testing.T, ctx context.Context, store *store, dagKey string) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: dagKey, Title: "flow", Status: "draft", CreatedBy: "tester", Metadata: []byte(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG(%s) error = %v", dagKey, err)
	}
	if _, err := store.UpsertNode(ctx, Node{DagKey: dagKey, NodeKey: "root", Title: "root", NodeType: "agent", DependsOn: []byte(`[]`), Config: []byte(`{"node":"root"}`)}); err != nil {
		t.Fatalf("UpsertNode(root) error = %v", err)
	}
	if _, err := store.UpsertNode(ctx, Node{DagKey: dagKey, NodeKey: "child", Title: "child", NodeType: "agent", DependsOn: []byte(`["root"]`), Config: []byte(`{"node":"child"}`)}); err != nil {
		t.Fatalf("UpsertNode(child) error = %v", err)
	}
}

func seedSQLiteCascadeTemplate(t *testing.T, ctx context.Context, store *store, dagKey string) {
	t.Helper()
	seedSQLiteFlowTemplate(t, ctx, store, dagKey)
	if _, err := store.UpsertNode(ctx, Node{DagKey: dagKey, NodeKey: "leaf", Title: "leaf", NodeType: "agent", DependsOn: []byte(`["child"]`), Config: []byte(`{"node":"leaf"}`)}); err != nil {
		t.Fatalf("UpsertNode(leaf) error = %v", err)
	}
}

func seedSQLiteBlockedDiamondTemplate(t *testing.T, ctx context.Context, store *store, dagKey string) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: dagKey, Title: "blocked", Status: "draft", CreatedBy: "tester", Metadata: []byte(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG(%s) error = %v", dagKey, err)
	}
	nodes := []Node{
		{DagKey: dagKey, NodeKey: "A", Title: "A", NodeType: "agent", DependsOn: []byte(`[]`), Config: []byte(`{"node":"A"}`)},
		{DagKey: dagKey, NodeKey: "B", Title: "B", NodeType: "agent", DependsOn: []byte(`["A"]`), Config: []byte(`{"node":"B"}`)},
		{DagKey: dagKey, NodeKey: "C", Title: "C", NodeType: "agent", DependsOn: []byte(`["B"]`), Config: []byte(`{"node":"C"}`)},
		{DagKey: dagKey, NodeKey: "D", Title: "D", NodeType: "agent", DependsOn: []byte(`["A"]`), Config: []byte(`{"node":"D"}`)},
		{DagKey: dagKey, NodeKey: "E", Title: "E", NodeType: "agent", DependsOn: []byte(`["B","D"]`), Config: []byte(`{"node":"E"}`)},
	}
	for _, node := range nodes {
		if _, err := store.UpsertNode(ctx, node); err != nil {
			t.Fatalf("UpsertNode(%s) error = %v", node.NodeKey, err)
		}
	}
}

func seedSQLiteFinalOutputTemplate(t *testing.T, ctx context.Context, store *store, dagKey string) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, DAG{DagKey: dagKey, Title: "flow", Status: "draft", CreatedBy: "tester", Metadata: []byte(`{"final_node_key":"child"}`)}); err != nil {
		t.Fatalf("UpsertDAG(%s) error = %v", dagKey, err)
	}
	if _, err := store.UpsertNode(ctx, Node{DagKey: dagKey, NodeKey: "root", Title: "Root", NodeType: "agent", DependsOn: []byte(`[]`), Config: []byte(`{"node":"root"}`)}); err != nil {
		t.Fatalf("UpsertNode(root) error = %v", err)
	}
	if _, err := store.UpsertNode(ctx, Node{DagKey: dagKey, NodeKey: "child", Title: "Child", NodeType: "agent", DependsOn: []byte(`["root"]`), Config: []byte(`{"node":"child"}`)}); err != nil {
		t.Fatalf("UpsertNode(child) error = %v", err)
	}
}

func assertSQLiteFinalOutputMetadata(t *testing.T, metadata json.RawMessage) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(metadata, &got); err != nil {
		t.Fatalf("run metadata is not JSON: %v; raw=%s", err, metadata)
	}
	if got["request_id"] != "req-1" {
		t.Fatalf("metadata.request_id = %v, want req-1; metadata=%v", got["request_id"], got)
	}
	rawFinal, ok := got["final_output"].(map[string]any)
	if !ok {
		t.Fatalf("metadata.final_output = %T, want object; metadata=%v", got["final_output"], got)
	}
	if rawFinal["kind"] != "file" || rawFinal["role"] != "final_output" || rawFinal["source_node_key"] != "child" {
		t.Fatalf("final_output identity = %v, want file final_output child", rawFinal)
	}
	if rawFinal["path"] != "reports/final.md" {
		t.Fatalf("final_output.path = %v, want reports/final.md", rawFinal["path"])
	}
	if _, ok := rawFinal["result"]; ok {
		t.Fatalf("file final_output must not duplicate node result: %v", rawFinal)
	}
}

func createSQLiteTaskDAGRun(t *testing.T, ctx context.Context, store *store, runKey, dagKey string) *Run {
	t.Helper()
	run, err := store.CreateRun(ctx, CreateRunInput{RunKey: runKey, DagKey: dagKey, DagVersionSnapshot: 0, TriggerSource: "manual", Metadata: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("CreateRun(%s) error = %v", runKey, err)
	}
	return run
}

func markSQLiteWakeupSent(t *testing.T, ctx context.Context, store *store, wakeupID int64) {
	t.Helper()
	claimed, err := store.ClaimDueWakeups(ctx, ClaimDueWakeupsInput{ClaimedBy: "worker-alpha", LeaseInterval: "30s", Limit: 1})
	if err != nil {
		t.Fatalf("ClaimDueWakeups() error = %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != wakeupID {
		t.Fatalf("claimed wakeups = %+v, want wakeup %d", claimed, wakeupID)
	}
	wakeup := claimed[0]
	if wakeup.ClaimedAt == nil || wakeup.LeaseExpiresAt == nil {
		t.Fatalf("claimed wakeup missing fence fields: %+v", wakeup)
	}
	rows, err := store.MarkWakeupSent(ctx, MarkWakeupSentInput{
		ID:             wakeup.ID,
		ClaimedAt:      *wakeup.ClaimedAt,
		ClaimedBy:      wakeup.ClaimedBy,
		LeaseExpiresAt: *wakeup.LeaseExpiresAt,
	})
	if err != nil || rows != 1 {
		t.Fatalf("MarkWakeupSent() rows=%d error=%v, want 1/nil", rows, err)
	}
}

func cloneAndPromoteSQLiteRun(t *testing.T, ctx context.Context, store *store, dagKey string, runID int64) {
	t.Helper()
	if rows, err := store.CloneNodesForRun(ctx, dagKey, runID); err != nil || rows == 0 {
		t.Fatalf("CloneNodesForRun(%s/%d) rows=%d error=%v, want rows/nil", dagKey, runID, rows, err)
	}
	if rows, err := store.PromoteRootNodesToReady(ctx, dagKey, runID); err != nil || rows == 0 {
		t.Fatalf("PromoteRootNodesToReady(%s/%d) rows=%d error=%v, want rows/nil", dagKey, runID, rows, err)
	}
}

func sqliteRunNodeStatus(t *testing.T, ctx context.Context, store *store, dagKey string, runID int64, nodeKey string) string {
	t.Helper()
	nodes, err := store.ListRunNodes(ctx, dagKey, runID)
	if err != nil {
		t.Fatalf("ListRunNodes(%s/%d) error = %v", dagKey, runID, err)
	}
	for _, node := range nodes {
		if node.NodeKey == nodeKey {
			return node.Status
		}
	}
	t.Fatalf("node %s not found in run %s/%d", nodeKey, dagKey, runID)
	return ""
}

func sqliteRunNodeSpawningThread(t *testing.T, ctx context.Context, store *store, dagKey string, runID int64, nodeKey string) string {
	t.Helper()
	nodes, err := store.ListRunNodes(ctx, dagKey, runID)
	if err != nil {
		t.Fatalf("ListRunNodes(%s/%d) error = %v", dagKey, runID, err)
	}
	for _, node := range nodes {
		if node.NodeKey != nodeKey {
			continue
		}
		if node.SpawningThreadID == nil {
			return ""
		}
		return *node.SpawningThreadID
	}
	t.Fatalf("node %s not found in run %s/%d", nodeKey, dagKey, runID)
	return ""
}

func openTaskDAGSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "taskdag.sqlite")
	db, err := sql.Open("sqlite", taskDAGSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(ctx, db, taskDAGSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func taskDAGSQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func taskDAGSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}
