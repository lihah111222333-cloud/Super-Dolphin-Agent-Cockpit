//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCompleteNodeAndScheduleDownstream_BlocksAssignedNodeMissingCWD(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	setRuntimeNodeConfig(t, db, completeDownstreamRunID, "B", json.RawMessage(`{"exec":{"agent_key":"beta"}}`))

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   completeDownstreamRunID,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if got := scheduledKeys(res.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("scheduled = %v, want [] for missing exec.cwd", got)
	}
	if c := pendingForNode(db, "B"); c != 0 {
		t.Fatalf("B wakeup count = %d, want 0 for missing exec.cwd", c)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)].Status; got != "ready" {
		t.Fatalf("B status = %q, want ready with diagnostic event", got)
	}
	assertRunHasDispatchBlockedEvent(t, db, "run-complete", "B", "exec.cwd")
}
