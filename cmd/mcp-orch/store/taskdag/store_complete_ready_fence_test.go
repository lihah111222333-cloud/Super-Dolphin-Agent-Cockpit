//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// TestCompleteTaskDagNode_FenceAcceptsReadyRunningAwaitingVerify 锁定 CompleteTaskDagNode 的状态围栏。
// TurnCompleted 可能先于 dispatchAgent 把 ready 切到 running，因此 done 写入必须接受
// ready、running 和 awaiting_verify；pending、done、failed 仍要被拒绝，避免越过生命周期边界。
//
// 该表驱动测试同时覆盖 ready/running/awaiting_verify 到 done 的成功路径，
// 以及 pending、done、failed 被围栏拒绝的下界和上界。
func TestCompleteTaskDagNode_FenceAcceptsReadyRunningAwaitingVerify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		initial     string
		wantSuccess bool
	}{
		{name: "ready to done", initial: "ready", wantSuccess: true},
		{name: "running to done", initial: "running", wantSuccess: true},
		{name: "awaiting_verify to done", initial: "awaiting_verify", wantSuccess: true},
		{name: "pending rejected", initial: "pending", wantSuccess: false},
		{name: "done rejected", initial: "done", wantSuccess: false},
		{name: "failed rejected", initial: "failed", wantSuccess: false},
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

			node, err := store.CompleteNode(context.Background(), CompleteNodeInput{
				Status:  "done",
				Result:  json.RawMessage(`{}`),
				DagKey:  "dag-1",
				NodeKey: "node-1",
				RunID:   runID,
			})
			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("CompleteNode(%s) error = %v, want success", tc.initial, err)
				}
				if node == nil || node.Status != "done" {
					t.Fatalf("node = %+v, want status=done", node)
				}
				return
			}
			if err == nil {
				t.Fatalf("CompleteNode(%s) error = nil, want fence rejection", tc.initial)
			}
			persisted := db.nodes[key]
			if persisted.Status != tc.initial {
				t.Fatalf("persisted status mutated from %q to %q on rejected fence", tc.initial, persisted.Status)
			}
		})
	}
}
