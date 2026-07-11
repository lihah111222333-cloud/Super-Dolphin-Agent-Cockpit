package nodeevents

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

func TestBuildRejectsInvalidIdentityWithoutPanic(t *testing.T) {
	runID := int64(0)
	cases := []struct {
		name string
		node *taskdag.Node
	}{
		{name: "missing dag", node: &taskdag.Node{NodeKey: "node", RunID: ptrInt64(1), Status: "done"}},
		{name: "missing node", node: &taskdag.Node{DagKey: "dag", RunID: ptrInt64(1), Status: "done"}},
		{name: "missing status", node: &taskdag.Node{DagKey: "dag", NodeKey: "node", RunID: ptrInt64(1)}},
		{name: "missing run", node: &taskdag.Node{DagKey: "dag", NodeKey: "node", Status: "done"}},
		{name: "zero run", node: &taskdag.Node{DagKey: "dag", NodeKey: "node", RunID: &runID, Status: "done"}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := build("running", tt.node); ok {
				t.Fatalf("build() ok = true, want false")
			}
		})
	}
}

func TestBuildTrimsIdentityAndCarriesOptionalFields(t *testing.T) {
	activeTurnID := " turn-1 "
	activeWakeupID := int64(99)
	node := &taskdag.Node{
		DagKey:         " dag-1 ",
		NodeKey:        " node-1 ",
		RunID:          ptrInt64(42),
		Status:         " done ",
		AssignedTo:     " agent-1 ",
		ActiveTurnID:   &activeTurnID,
		ActiveWakeupID: &activeWakeupID,
	}

	got, ok := build(" running ", node)
	if !ok {
		t.Fatal("build() ok = false, want true")
	}
	if got.DagKey != "dag-1" || got.NodeKey != "node-1" || got.RunID != 42 {
		t.Fatalf("identity = %q/%q/%d, want dag-1/node-1/42", got.DagKey, got.NodeKey, got.RunID)
	}
	if got.OldStatus != "running" || got.NewStatus != "done" || got.AssignedTo != "agent-1" {
		t.Fatalf("status/assignee = %q -> %q / %q", got.OldStatus, got.NewStatus, got.AssignedTo)
	}
	if got.ActiveTurnID != "turn-1" || got.ActiveWakeupID != activeWakeupID {
		t.Fatalf("active fields = %q/%d, want turn-1/%d", got.ActiveTurnID, got.ActiveWakeupID, activeWakeupID)
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
