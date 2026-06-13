package orchestration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	_ "modernc.org/sqlite"
)

func TestSQLiteApplyOpsConcurrentSameVersionOneConflict(t *testing.T) {
	ctx := context.Background()
	db := openOrchestrationSQLiteDB(t)
	store := taskdag.NewStore(db).(taskdag.Store)
	if _, err := store.UpsertDAG(ctx, taskdag.DAG{DagKey: "dag-occ", Title: "occ", Status: "draft", CreatedBy: "tester", Metadata: []byte(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}

	svc := &service{dagStore: store}
	ops := json.RawMessage(`[{"op":"update_dag","patch":{"description":"updated"}}]`)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.ApplyOps(ctx, contract.ApplyOpsRequest{DagKey: "dag-occ", BaseVersion: 0, Ops: ops})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var success, conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrVersionConflict):
			conflict++
		default:
			t.Fatalf("ApplyOps() unexpected error = %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("ApplyOps results success=%d conflict=%d, want 1/1", success, conflict)
	}
	dag, err := store.GetDAG(ctx, "dag-occ")
	if err != nil {
		t.Fatalf("GetDAG() error = %v", err)
	}
	if dag.Version != 1 {
		t.Fatalf("dag version = %d, want 1", dag.Version)
	}
}

func TestSQLiteApplyOpsAddUpdateDeleteAndEmptyShortCircuit(t *testing.T) {
	ctx := context.Background()
	db := openOrchestrationSQLiteDB(t)
	store := taskdag.NewStore(db).(taskdag.Store)
	seedSQLiteApplyOpsDAG(t, ctx, store, "dag-ops")
	svc := &service{dagStore: store}

	resp, err := svc.ApplyOps(ctx, contract.ApplyOpsRequest{
		DagKey:      "dag-ops",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"root","title":"Root","node_type":"agent","config":{"stage":"root"}}},
			{"op":"add_node","node":{"node_key":"child","title":"Child","node_type":"agent","depends_on":["root"],"config":{"stage":"child"}}}
		]`),
	})
	if err != nil {
		t.Fatalf("ApplyOps(add) error = %v", err)
	}
	if resp.NewVersion != 1 {
		t.Fatalf("add NewVersion = %d, want 1", resp.NewVersion)
	}
	assertSQLiteApplyOpsNodeKeys(t, ctx, store, "dag-ops", []string{"root", "child"})

	resp, err = svc.ApplyOps(ctx, contract.ApplyOpsRequest{
		DagKey:      "dag-ops",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_node","node_key":"child","patch":{"config":{"stage":"updated"}}}]`),
	})
	if err != nil {
		t.Fatalf("ApplyOps(update) error = %v", err)
	}
	if resp.NewVersion != 2 {
		t.Fatalf("update NewVersion = %d, want 2", resp.NewVersion)
	}
	assertSQLiteApplyOpsNodeConfig(t, ctx, store, "dag-ops", "child", "updated")

	resp, err = svc.ApplyOps(ctx, contract.ApplyOpsRequest{
		DagKey:      "dag-ops",
		BaseVersion: 2,
		Ops:         json.RawMessage(`[{"op":"remove_node","node_key":"child"}]`),
	})
	if err != nil {
		t.Fatalf("ApplyOps(remove) error = %v", err)
	}
	if resp.NewVersion != 3 {
		t.Fatalf("remove NewVersion = %d, want 3", resp.NewVersion)
	}
	assertSQLiteApplyOpsNodeKeys(t, ctx, store, "dag-ops", []string{"root"})

	resp, err = svc.ApplyOps(ctx, contract.ApplyOpsRequest{DagKey: "dag-ops", BaseVersion: 3, Ops: json.RawMessage(`[]`)})
	if err != nil {
		t.Fatalf("ApplyOps(empty) error = %v", err)
	}
	if resp.NewVersion != 3 {
		t.Fatalf("empty ops NewVersion = %d, want unchanged 3", resp.NewVersion)
	}
}

func TestSQLiteApplyOpsRejectsConfigChangeForDoneTemplateNode(t *testing.T) {
	ctx := context.Background()
	db := openOrchestrationSQLiteDB(t)
	store := taskdag.NewStore(db).(taskdag.Store)
	seedSQLiteApplyOpsDAG(t, ctx, store, "dag-done")
	if _, err := store.UpsertNode(ctx, taskdag.Node{
		DagKey:    "dag-done",
		NodeKey:   "done-node",
		Title:     "Done",
		NodeType:  "agent",
		DependsOn: json.RawMessage(`[]`),
		Config:    json.RawMessage(`{"stage":"locked"}`),
	}); err != nil {
		t.Fatalf("UpsertNode(done-node) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_dag_nodes SET status = 'done' WHERE dag_key = ? AND node_key = ? AND run_id IS NULL`, "dag-done", "done-node"); err != nil {
		t.Fatalf("mark template node done: %v", err)
	}

	svc := &service{dagStore: store}
	_, err := svc.ApplyOps(ctx, contract.ApplyOpsRequest{
		DagKey:      "dag-done",
		BaseVersion: 0,
		Ops:         json.RawMessage(`[{"op":"update_node","node_key":"done-node","patch":{"config":{"stage":"changed"}}}]`),
	})
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps(done config) error = %v, want ErrApplyOpsInvalid", err)
	}
	assertSQLiteApplyOpsNodeConfig(t, ctx, store, "dag-done", "done-node", "locked")
}

func seedSQLiteApplyOpsDAG(t *testing.T, ctx context.Context, store taskdag.Store, dagKey string) {
	t.Helper()
	if _, err := store.UpsertDAG(ctx, taskdag.DAG{DagKey: dagKey, Title: dagKey, Status: "draft", CreatedBy: "tester", Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG(%s) error = %v", dagKey, err)
	}
}

func assertSQLiteApplyOpsNodeKeys(t *testing.T, ctx context.Context, store taskdag.Store, dagKey string, want []string) {
	t.Helper()
	nodes, err := store.ListNodes(ctx, dagKey)
	if err != nil {
		t.Fatalf("ListNodes(%s) error = %v", dagKey, err)
	}
	got := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		got[node.NodeKey] = true
	}
	if len(got) != len(want) {
		t.Fatalf("node keys = %v, want %v", got, want)
	}
	for _, key := range want {
		if !got[key] {
			t.Fatalf("node keys = %v, want %v", got, want)
		}
	}
}

func assertSQLiteApplyOpsNodeConfig(t *testing.T, ctx context.Context, store taskdag.Store, dagKey, nodeKey, wantStage string) {
	t.Helper()
	nodes, err := store.ListNodes(ctx, dagKey)
	if err != nil {
		t.Fatalf("ListNodes(%s) error = %v", dagKey, err)
	}
	for _, node := range nodes {
		if node.NodeKey != nodeKey {
			continue
		}
		var cfg struct {
			Stage string `json:"stage"`
		}
		if err := json.Unmarshal(node.Config, &cfg); err != nil {
			t.Fatalf("decode config for %s: %v", nodeKey, err)
		}
		if cfg.Stage != wantStage {
			t.Fatalf("%s config stage = %q, want %q", nodeKey, cfg.Stage, wantStage)
		}
		return
	}
	t.Fatalf("node %s not found in %s", nodeKey, dagKey)
}

func openOrchestrationSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orchestration.sqlite")
	db, err := sql.Open("sqlite", orchestrationSQLiteDSN(path))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(4)
	if err := sqliteruntime.RunMigrations(ctx, db, orchestrationSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("run sqlite migrations: %v", err)
	}
	return db
}

func orchestrationSQLiteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	return path + "?" + q.Encode()
}

func orchestrationSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations dir: %v", err)
	}
	return dir
}
