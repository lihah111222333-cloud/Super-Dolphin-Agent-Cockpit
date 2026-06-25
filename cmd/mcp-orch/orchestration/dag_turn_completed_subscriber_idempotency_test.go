package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

func TestDAGSubscriberDuplicateTurnCompletedPromotesDownstreamOnce(t *testing.T) {
	ctx := context.Background()
	db := openDAGSubscriberSQLiteDB(t)
	rawStore := taskdag.NewStore(db)
	runStore := rawStore.(taskdag.RunStore)
	flowStore := rawStore.(taskdag.NodeFlowStore)
	runningStore := rawStore.(taskdag.RunningNodeStore)
	spawnStore := rawStore.(taskdag.NodeSpawnRecorderStore)
	lookupStore := rawStore.(taskdag.NodeSpawningThreadLookup)
	wakeupStore := rawStore.(taskdag.WakeupStore)
	runNodeStore := rawStore.(taskdag.RunNodeReadStore)
	seedDAGSubscriberRuntime(t, ctx, rawStore, runStore, "dag-dup-turn", "run-dup-turn")
	run, err := runStore.GetRun(ctx, "run-dup-turn")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if _, err := runningStore.UpdateRunningNodeStatus(ctx, taskdag.RunningNodeStatusUpdate{
		DagKey:  "dag-dup-turn",
		NodeKey: "root",
		RunID:   run.ID,
		Status:  "running",
		Result:  []byte(`{}`),
	}); err != nil {
		t.Fatalf("UpdateRunningNodeStatus(root) error = %v", err)
	}
	if _, err := spawnStore.RecordNodeSpawn(ctx, taskdag.RecordNodeSpawnInput{
		DagKey:   "dag-dup-turn",
		NodeKey:  "root",
		RunID:    run.ID,
		ThreadID: "thread-dup",
	}); err != nil {
		t.Fatalf("RecordNodeSpawn(root) error = %v", err)
	}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := DAGSubscriberDeps{
		LookupStore:      lookupStore,
		FlowStore:        flowStore,
		SharedFileWriter: writer,
		AgentThreads:     &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thread-dup", AgentID: "agent-root"}},
		SvcStopper:       &dagSubscriberStopSpy{},
	}
	ev := newTurnCompletedEvent("thread-dup", true, `{"summary":"done"}`)

	handleDAGTurnCompleted(ctx, deps, discardLogger(), ev)
	handleDAGTurnCompleted(ctx, deps, discardLogger(), ev)

	nodes, err := runNodeStore.ListRunNodes(ctx, "dag-dup-turn", run.ID)
	if err != nil {
		t.Fatalf("ListRunNodes() error = %v", err)
	}
	assertDAGSubscriberNodeStatus(t, nodes, "root", "done")
	assertDAGSubscriberNodeStatus(t, nodes, "child", "ready")
	wakeups, err := wakeupStore.ListPendingOrDispatchingWakeups(ctx)
	if err != nil {
		t.Fatalf("ListPendingOrDispatchingWakeups() error = %v", err)
	}
	if len(wakeups) != 1 || wakeups[0].NodeKey != "child" {
		t.Fatalf("pending wakeups = %+v, want one child wakeup", wakeups)
	}
	if len(writer.writes) != 2 {
		t.Fatalf("sharedfile writes = %d, want exactly content + owner marker once", len(writer.writes))
	}
	if got := findSharedFileWrite(t, writer, "reports/root.md"); got != `{"summary":"done"}` {
		t.Fatalf("sharedfile content = %s, want first turn output", got)
	}
}

func seedDAGSubscriberRuntime(t *testing.T, ctx context.Context, store taskdag.Store, runStore taskdag.RunStore, dagKey, runKey string) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, taskdag.DAG{DagKey: dagKey, Title: dagKey, Status: "draft", CreatedBy: "tester", Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG(%s) error = %v", dagKey, err)
	}
	cwd := t.TempDir()
	nodes := []taskdag.Node{
		{
			DagKey:     dagKey,
			NodeKey:    "root",
			Title:      "Root",
			NodeType:   "agent",
			AssignedTo: "agent-root",
			DependsOn:  json.RawMessage(`[]`),
			Config: dagSubscriberAgentConfig(t, "writer", cwd, map[string]any{
				"to_sharedfile": map[string]any{
					"path":      "reports/root.md",
					"lock_mode": "exclusive",
				},
			}),
		},
		{
			DagKey:     dagKey,
			NodeKey:    "child",
			Title:      "Child",
			NodeType:   "agent",
			AssignedTo: "agent-child",
			DependsOn:  json.RawMessage(`["root"]`),
			Config:     dagSubscriberAgentConfig(t, "reviewer", cwd, nil),
		},
	}
	for _, node := range nodes {
		if _, err := store.UpsertNode(ctx, node); err != nil {
			t.Fatalf("UpsertNode(%s) error = %v", node.NodeKey, err)
		}
	}
	run, err := runStore.CreateRun(ctx, taskdag.CreateRunInput{
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: 1,
		TriggerSource:      "manual",
		Metadata:           json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateRun(%s) error = %v", runKey, err)
	}
	if rows, err := runStore.CloneNodesForRun(ctx, dagKey, run.ID); err != nil || rows != 2 {
		t.Fatalf("CloneNodesForRun rows=%d error=%v, want 2/nil", rows, err)
	}
	if rows, err := runStore.PromoteRootNodesToReady(ctx, dagKey, run.ID); err != nil || rows != 1 {
		t.Fatalf("PromoteRootNodesToReady rows=%d error=%v, want 1/nil", rows, err)
	}
}

func dagSubscriberAgentConfig(t *testing.T, agentKey, cwd string, outputs map[string]any) json.RawMessage {
	t.Helper()
	cfg := map[string]any{
		"exec": map[string]any{
			"agent_key": agentKey,
			"cwd":       cwd,
		},
	}
	if outputs != nil {
		cfg["outputs"] = outputs
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal node config: %v", err)
	}
	return raw
}

func assertDAGSubscriberNodeStatus(t *testing.T, nodes []taskdag.Node, nodeKey, want string) {
	t.Helper()
	for _, node := range nodes {
		if node.NodeKey == nodeKey {
			if node.Status != want {
				t.Fatalf("%s status = %q, want %q", nodeKey, node.Status, want)
			}
			return
		}
	}
	t.Fatalf("node %s not found in %+v", nodeKey, nodes)
}

func openDAGSubscriberSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dag-subscriber.sqlite")
	db, err := sql.Open("sqlite", dagSubscriberSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(context.Background(), db, dagSubscriberSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func dagSubscriberSQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func dagSubscriberSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}
