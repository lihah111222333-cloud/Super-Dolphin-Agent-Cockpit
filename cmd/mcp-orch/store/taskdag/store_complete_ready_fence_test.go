//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// TestCompleteTaskDagNode_FenceAcceptsReadyRunningAwaitingVerify locks in the
// ADR-017 v1.2 搂2.3 fence relaxation. CompleteTaskDagNode previously matched
// IN ('running','awaiting_verify'); subscriber race A (TurnCompleted before
// dispatchAgent flips ready鈫抮unning) silently produced 0 rows. The fix
// extends the fence to IN ('ready','running','awaiting_verify').
//
// This test asserts:
//   - ready 鈫?done succeeds (new path, race A);
//   - running 鈫?done succeeds (legacy path, unchanged);
//   - awaiting_verify 鈫?done succeeds (legacy path, unchanged);
//   - pending 鈫?done is still rejected (fence retains lower bound);
//   - done 鈫?done is still rejected (fence retains upper bound).
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
