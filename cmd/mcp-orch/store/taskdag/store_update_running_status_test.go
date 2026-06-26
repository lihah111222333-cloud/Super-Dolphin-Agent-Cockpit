//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// TestUpdateRunningNodeStatus_FenceAcceptsPendingAndReady 锁定 running 状态更新的 SQL fence。
//
// UpdateRunningTaskDagNodeStatus 必须允许 pending 和 ready 两种起点进入 running；
// 如果只允许 pending，已被调度器提升到 ready 的根节点会在派发时匹配 0 行并卡住。
//
// 测试同时覆盖合法起点和 already-running/terminal 拒绝路径，防止 fence 上界被放宽。
func TestUpdateRunningNodeStatus_FenceAcceptsPendingAndReady(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		initial     string
		wantSuccess bool
	}{
		{name: "pending to running", initial: "pending", wantSuccess: true},
		{name: "ready to running", initial: "ready", wantSuccess: true},
		{name: "running rejected", initial: "running", wantSuccess: false},
		{name: "done rejected", initial: "done", wantSuccess: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, db, now := newTaskDAGTestStore()
			runID := db.runs["run-1"].ID
			key := dagRunNodeKey("dag-1", "node-1", runID)
			db.nodes[key] = sqlc.TaskDagNode{
				ID:        42,
				DagKey:    "dag-1",
				NodeKey:   "node-1",
				RunID:     sqlc.Int8ValuePtr(&runID),
				Title:     "n1",
				Status:    tc.initial,
				DependsOn: []byte(`[]`),
				Config:    []byte(`{}`),
				Result:    []byte(`{}`),
				CreatedAt: timestamptzValue(now),
				UpdatedAt: timestamptzValue(now),
			}

			node, err := store.UpdateRunningNodeStatus(context.Background(), RunningNodeStatusUpdate{
				Status:   "running",
				Result:   json.RawMessage(`{}`),
				WakeupID: 99,
				DagKey:   "dag-1",
				NodeKey:  "node-1",
				RunID:    runID,
			})
			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("UpdateRunningNodeStatus(%s) error = %v, want success", tc.initial, err)
				}
				if node == nil || node.Status != "running" {
					t.Fatalf("node = %+v, want status=running", node)
				}
				persisted := db.nodes[key]
				if persisted.Status != "running" {
					t.Fatalf("persisted status = %q, want running", persisted.Status)
				}
				return
			}
			// Failure path: fence reject => store returns wrapped error and
			// the underlying node row must NOT mutate.
			if err == nil {
				t.Fatalf("UpdateRunningNodeStatus(%s) error = nil, want fence rejection", tc.initial)
			}
			persisted := db.nodes[key]
			if persisted.Status != tc.initial {
				t.Fatalf("persisted status mutated from %q to %q on rejected fence", tc.initial, persisted.Status)
			}
		})
	}
}
