//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

type outputMaterializationClaimer interface {
	ClaimNodeOutputMaterialization(context.Context, OutputMaterializationClaimInput) (*Node, error)
}

func TestClaimNodeOutputMaterialization_FenceAcceptsReadyRunning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		initial     string
		wantSuccess bool
	}{
		{name: "ready accepted", initial: "ready", wantSuccess: true},
		{name: "running accepted", initial: "running", wantSuccess: true},
		{name: "awaiting_verify rejected as legacy state", initial: "awaiting_verify", wantSuccess: false},
		{name: "pending rejected", initial: "pending", wantSuccess: false},
		{name: "done rejected", initial: "done", wantSuccess: false},
		{name: "failed rejected", initial: "failed", wantSuccess: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store, db, now := newTaskDAGTestStore()
			claimer := store.(outputMaterializationClaimer)
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

			result := json.RawMessage(`{"sharedfile":{"path":"reports/node-1.json"}}`)
			node, err := claimer.ClaimNodeOutputMaterialization(context.Background(), OutputMaterializationClaimInput{
				Result:  result,
				DagKey:  "dag-1",
				NodeKey: "node-1",
				RunID:   runID,
			})
			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("ClaimNodeOutputMaterialization(%s) error = %v, want success", tc.initial, err)
				}
				if node == nil || node.Status != tc.initial {
					t.Fatalf("node = %+v, want status=%s", node, tc.initial)
				}
				if got := string(db.nodes[key].Result); got != string(result) {
					t.Fatalf("persisted result = %s, want %s", got, result)
				}
				return
			}
			if err == nil {
				t.Fatalf("ClaimNodeOutputMaterialization(%s) error = nil, want fence rejection", tc.initial)
			}
			persisted := db.nodes[key]
			if persisted.Status != tc.initial {
				t.Fatalf("persisted status mutated from %q to %q on rejected fence", tc.initial, persisted.Status)
			}
		})
	}
}
