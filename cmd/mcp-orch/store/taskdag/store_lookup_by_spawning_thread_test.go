//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// TestLookupNodesBySpawningThread_ReverseLookupReturnsMatchingRows 验证反查能返回携带指定 thread id 的唯一节点。
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

// TestLookupNodesBySpawningThread_NoMatch_ReturnsEmptySliceNoError 验证无匹配行时返回空切片且不报错。
// 调用方收到空切片即可识别 lookup miss，不需要额外区分 pgx.ErrNoRows。
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

// TestLookupNodesBySpawningThread_FiltersNullSpawningThread 验证查询必须排除 NULL spawning_thread_id。
// 即使传入空 threadID，旧自动化节点或尚未 spawn 的模板节点也不能被匹配。
func TestLookupNodesBySpawningThread_FiltersNullSpawningThread(t *testing.T) {
	t.Parallel()
	store, db, now := newTaskDAGTestStore()
	runID := db.runs["run-1"].ID
	// 缺少 spawning_thread_id 的节点不能被空 threadID 匹配。
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
		// SpawningThreadID 故意保持零值，对应数据库 NULL。
	}
	got, err := store.(NodeSpawningThreadLookup).LookupNodesBySpawningThread(context.Background(), "")
	if err != nil {
		t.Fatalf("LookupNodesBySpawningThread err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d for empty threadID, want 0 (must not match NULL rows)", len(got))
	}
}

// TestLookupNodesBySpawningThread_MultipleRowsReturnedDescByUpdatedAt 覆盖多个节点共享同一 thread id 的脏数据场景。
// 查询返回全部匹配项并按 updated_at DESC、id DESC 排序，调用方再逐个做幂等推进。
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

// TestLookupNodesBySpawningThread_DoesNotMatchDifferentThreadID 验证查询严格按完整 thread id 匹配。
// 前缀或子串相同都不能命中其他 thread。
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
