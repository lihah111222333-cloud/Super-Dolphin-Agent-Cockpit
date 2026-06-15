//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// TestLookupNodesBySpawningThread_ReverseLookupReturnsMatchingRows is the
// ADR-017 v1.2 搂2.2 happy path: a single node carrying the given thread id is
// returned by the reverse lookup.
func TestLookupNodesBySpawningThread_ReverseLookupReturnsMatchingRows(t *testing.T) {
	t.Parallel()
	store, db, now := newTaskDAGTestStore()
	thr := "thr-aaa"
	runID := db.runs["run-1"].ID
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = sqlc.TaskDagNode{
		ID:               1,
		DagKey:           "dag-1",
		NodeKey:          "node-1",
		RunID:            sqlc.Int8ValuePtr(&runID),
		Title:            "n1",
		Status:           "running",
		DependsOn:        []byte(`[]`),
		Config:           []byte(`{}`),
		Result:           []byte(`{}`),
		CreatedAt:        timestamptzValue(now),
		UpdatedAt:        timestamptzValue(now),
		SpawningThreadID: sqlc.TextValuePtr(&thr),
	}
	got, err := store.(NodeSpawningThreadLookup).LookupNodesBySpawningThread(context.Background(), thr)
	if err != nil {
		t.Fatalf("LookupNodesBySpawningThread err = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].DagKey != "dag-1" || got[0].NodeKey != "node-1" {
		t.Fatalf("got node = %s/%s, want dag-1/node-1", got[0].DagKey, got[0].NodeKey)
	}
	if got[0].SpawningThreadID == nil || *got[0].SpawningThreadID != thr {
		t.Fatalf("got spawning_thread_id = %v, want %q", got[0].SpawningThreadID, thr)
	}
}

// TestLookupNodesBySpawningThread_NoMatch_ReturnsEmptySliceNoError verifies
// that a thread id with no matching row returns an empty slice (not
// pgx.ErrNoRows) 鈥?ADR-017 搂2.2 contract for "lookup miss".
func TestLookupNodesBySpawningThread_NoMatch_ReturnsEmptySliceNoError(t *testing.T) {
	t.Parallel()
	store, _, _ := newTaskDAGTestStore()
	got, err := store.(NodeSpawningThreadLookup).LookupNodesBySpawningThread(context.Background(), "thr-does-not-exist")
	if err != nil {
		t.Fatalf("LookupNodesBySpawningThread err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
}

// TestLookupNodesBySpawningThread_FiltersNullSpawningThread verifies the
// `spawning_thread_id IS NOT NULL` guard 鈥?node rows without a spawning
// thread (legacy automation nodes, never-spawned templates) must not be
// returned even when threadID is empty.
func TestLookupNodesBySpawningThread_FiltersNullSpawningThread(t *testing.T) {
	t.Parallel()
	store, db, now := newTaskDAGTestStore()
	runID := db.runs["run-1"].ID
	// node without spawning_thread_id (NULL): must never match.
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = sqlc.TaskDagNode{
		ID:        10,
		DagKey:    "dag-1",
		NodeKey:   "node-1",
		RunID:     sqlc.Int8ValuePtr(&runID),
		Status:    "pending",
		DependsOn: []byte(`[]`),
		Config:    []byte(`{}`),
		Result:    []byte(`{}`),
		CreatedAt: timestamptzValue(now),
		UpdatedAt: timestamptzValue(now),
		// SpawningThreadID intentionally zero-value (NULL).
	}
	got, err := store.(NodeSpawningThreadLookup).LookupNodesBySpawningThread(context.Background(), "")
	if err != nil {
		t.Fatalf("LookupNodesBySpawningThread err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d for empty threadID, want 0 (must not match NULL rows)", len(got))
	}
}

// TestLookupNodesBySpawningThread_MultipleRowsReturnedDescByUpdatedAt covers
// the N>1 "dirty data" case (ADR-017 搂2.2): partial index has no UNIQUE so
// retry / recovery can leave more than one node with the same thread id.
// Lookup returns all of them ordered by updated_at DESC, id DESC 鈥?caller
// iterates and applies idempotent advancement on each.
func TestLookupNodesBySpawningThread_MultipleRowsReturnedDescByUpdatedAt(t *testing.T) {
	t.Parallel()
	store, db, now := newTaskDAGTestStore()
	thr := "thr-shared"
	runID := db.runs["run-1"].ID
	older := now
	newer := now.Add(10 * time.Second)
	db.nodes[dagRunNodeKey("dag-1", "older", runID)] = sqlc.TaskDagNode{
		ID:               1,
		DagKey:           "dag-1",
		NodeKey:          "older",
		RunID:            sqlc.Int8ValuePtr(&runID),
		Status:           "ready",
		DependsOn:        []byte(`[]`),
		Config:           []byte(`{}`),
		Result:           []byte(`{}`),
		CreatedAt:        timestamptzValue(older),
		UpdatedAt:        timestamptzValue(older),
		SpawningThreadID: sqlc.TextValuePtr(&thr),
	}
	db.nodes[dagRunNodeKey("dag-1", "newer", runID)] = sqlc.TaskDagNode{
		ID:               2,
		DagKey:           "dag-1",
		NodeKey:          "newer",
		RunID:            sqlc.Int8ValuePtr(&runID),
		Status:           "running",
		DependsOn:        []byte(`[]`),
		Config:           []byte(`{}`),
		Result:           []byte(`{}`),
		CreatedAt:        timestamptzValue(newer),
		UpdatedAt:        timestamptzValue(newer),
		SpawningThreadID: sqlc.TextValuePtr(&thr),
	}
	got, err := store.(NodeSpawningThreadLookup).LookupNodesBySpawningThread(context.Background(), thr)
	if err != nil {
		t.Fatalf("LookupNodesBySpawningThread err = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (N>1 dirty-data case)", len(got))
	}
	if got[0].NodeKey != "newer" || got[1].NodeKey != "older" {
		t.Fatalf("order = [%s,%s], want [newer,older] (DESC updated_at)", got[0].NodeKey, got[1].NodeKey)
	}
}

// TestLookupNodesBySpawningThread_DoesNotMatchDifferentThreadID ensures the
// query is strict on the thread id (no prefix / substring matching).
func TestLookupNodesBySpawningThread_DoesNotMatchDifferentThreadID(t *testing.T) {
	t.Parallel()
	store, db, now := newTaskDAGTestStore()
	thrA, thrB := "thr-A", "thr-B"
	runID := db.runs["run-1"].ID
	db.nodes[dagRunNodeKey("dag-1", "node-1", runID)] = sqlc.TaskDagNode{
		ID:               1,
		DagKey:           "dag-1",
		NodeKey:          "node-1",
		RunID:            sqlc.Int8ValuePtr(&runID),
		Status:           "running",
		DependsOn:        []byte(`[]`),
		Config:           []byte(`{}`),
		Result:           []byte(`{}`),
		CreatedAt:        timestamptzValue(now),
		UpdatedAt:        timestamptzValue(now),
		SpawningThreadID: sqlc.TextValuePtr(&thrA),
	}
	got, err := store.(NodeSpawningThreadLookup).LookupNodesBySpawningThread(context.Background(), thrB)
	if err != nil {
		t.Fatalf("LookupNodesBySpawningThread err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 (thrA must not match thrB)", len(got))
	}
}
