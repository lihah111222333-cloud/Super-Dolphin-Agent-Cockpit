//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// TestUpdateRunningNodeStatus_FenceAcceptsPendingAndReady locks in the W4 SQL
// fence relax (cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql).
//
// Before the fix, UpdateRunningTaskDagNodeStatus' WHERE clause was
// `status IN ('pending')` 鈥?so once F6.3 promote_single_node_pending_to_ready
// flipped a root node to 'ready', the dispatcher's subsequent attempt to push
// it into 'running' silently matched 0 rows and the node was wedged forever.
//
// The fix relaxes the fence to `status IN ('pending', 'ready')`. This test
// exercises both legal starting points and asserts terminal status becomes
// running, plus that an already-running / terminal node is rejected by the
// fence (no regression on the upper bound).
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
