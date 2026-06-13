//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"testing"
)

func TestDeleteNode_RemovesExistingNode(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "root", status: "pending"},
		{key: "leaf", deps: []string{"root"}, status: "pending"},
	})

	deleter := store.(interface {
		DeleteNode(context.Context, string, string) (int64, error)
	})
	rows, err := deleter.DeleteNode(context.Background(), "dag-1", "leaf")
	if err != nil {
		t.Fatalf("DeleteNode() error = %v", err)
	}
	if rows != 1 {
		t.Fatalf("DeleteNode() rows = %d, want 1", rows)
	}
	if _, ok := db.nodes[dagNodeKey("dag-1", "leaf")]; ok {
		t.Fatal("leaf still exists after DeleteNode")
	}
	if _, ok := db.nodes[dagNodeKey("dag-1", "root")]; !ok {
		t.Fatal("root should remain after deleting leaf")
	}
}

func TestDeleteNode_MissingReturnsZeroRows(t *testing.T) {
	t.Parallel()

	store, _, _ := newTaskDAGTestStore()
	deleter := store.(interface {
		DeleteNode(context.Context, string, string) (int64, error)
	})
	rows, err := deleter.DeleteNode(context.Background(), "dag-1", "missing")
	if err != nil {
		t.Fatalf("DeleteNode() error = %v", err)
	}
	if rows != 0 {
		t.Fatalf("DeleteNode() rows = %d, want 0", rows)
	}
}

func TestDeleteNode_RunningNodeReturnsZeroRows(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "running", status: "running"},
	})

	deleter := store.(interface {
		DeleteNode(context.Context, string, string) (int64, error)
	})
	rows, err := deleter.DeleteNode(context.Background(), "dag-1", "running")
	if err != nil {
		t.Fatalf("DeleteNode() error = %v", err)
	}
	if rows != 0 {
		t.Fatalf("DeleteNode() rows = %d, want 0 for running node", rows)
	}
	if _, ok := db.nodes[dagNodeKey("dag-1", "running")]; !ok {
		t.Fatal("running node should remain when DeleteNode returns zero rows")
	}
}
