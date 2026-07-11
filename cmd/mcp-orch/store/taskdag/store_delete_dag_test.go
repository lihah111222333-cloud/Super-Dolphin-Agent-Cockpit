//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestDeleteDAG_RemovesTemplateRunsNodesAndWakeups(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "root", status: "pending"},
		{key: "leaf", deps: []string{"root"}, status: "ready"},
	})
	seedDAGMetadata(db, "dag-1", nil)
	markDeleteDAGRunStatus(t, db, "run-1", "succeeded")
	seedDeleteDAGRun(db, "run-done", "succeeded", 42)
	db.nodes[dagRunNodeKey("dag-1", "root", 42)] = sqlc.TaskDagNode{
		ID:      42,
		DagKey:  "dag-1",
		NodeKey: "root",
		RunID:   sqlc.Int8{Int64: 42, Valid: true},
		Status:  "done",
	}
	db.wakeups[1] = sqlc.TaskDagWakeup{ID: 1, DagKey: "dag-1", NodeKey: "root", Status: "sent"}

	deleter := store.(DAGDeleteStore)
	rows, err := deleter.DeleteDAG(context.Background(), "dag-1")
	if err != nil {
		t.Fatalf("DeleteDAG() error = %v", err)
	}
	if rows != 1 {
		t.Fatalf("DeleteDAG() rows = %d, want 1", rows)
	}
	if _, ok := db.dags["dag-1"]; ok {
		t.Fatal("dag row still exists after DeleteDAG")
	}
	assertNoDAGRowsRemain(t, db, "dag-1")
}

func TestDeleteDAG_BlocksActiveRun(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{{key: "root", status: "pending"}})
	seedDAGMetadata(db, "dag-1", nil)
	seedDeleteDAGRun(db, "run-active", "running", 42)

	deleter := store.(DAGDeleteStore)
	rows, err := deleter.DeleteDAG(context.Background(), "dag-1")
	if !errors.Is(err, ErrDAGDeleteActiveRun) {
		t.Fatalf("DeleteDAG() error = %v, want ErrDAGDeleteActiveRun", err)
	}
	if rows != 0 {
		t.Fatalf("DeleteDAG() rows = %d, want 0", rows)
	}
	if _, ok := db.dags["dag-1"]; !ok {
		t.Fatal("dag row should remain when active run blocks deletion")
	}
}

func assertNoDAGRowsRemain(t *testing.T, db *fakeTaskDAGDB, dagKey string) {
	t.Helper()
	for key, node := range db.nodes {
		if node.DagKey == dagKey {
			t.Fatalf("node row %q remains for deleted dag", key)
		}
	}
	for key, run := range db.runs {
		if run.DagKey == dagKey {
			t.Fatalf("run row %q remains for deleted dag", key)
		}
	}
	for key, wakeup := range db.wakeups {
		if wakeup.DagKey == dagKey {
			t.Fatalf("wakeup row %d remains for deleted dag", key)
		}
	}
}

func seedDeleteDAGRun(db *fakeTaskDAGDB, runKey, status string, id int64) {
	db.runs[runKey] = sqlc.TaskDagRun{
		ID:        id,
		RunKey:    runKey,
		DagKey:    "dag-1",
		Status:    status,
		StartedAt: timestamptzValue(db.now),
		CreatedAt: timestamptzValue(db.now),
		UpdatedAt: timestamptzValue(db.now),
		Events:    []byte(`[]`),
		Metadata:  []byte(`{}`),
	}
}

func markDeleteDAGRunStatus(t *testing.T, db *fakeTaskDAGDB, runKey, status string) {
	t.Helper()
	run, ok := db.runs[runKey]
	if !ok {
		t.Fatalf("run %q not found", runKey)
	}
	run.Status = status
	run.UpdatedAt = timestamptzValue(db.now)
	db.runs[runKey] = run
}
