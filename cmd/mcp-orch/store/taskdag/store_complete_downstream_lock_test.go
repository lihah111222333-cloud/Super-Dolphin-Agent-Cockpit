//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCompleteNodeAndScheduleDownstream_LocksRunBeforeCompletion(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
	})

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if got := strings.Join(db.ops, ","); got != "lock_run_for_completion" {
		t.Fatalf("db ops = %q, want run completion lock before node completion", got)
	}
}

func TestCompleteNodeAndScheduleDownstream_InvalidDependsOnFailsFast(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: nil, status: "pending", agent: "agent-b"},
	})
	row := db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)]
	row.DependsOn = []byte(`[1]`)
	db.nodes[dagRunNodeKey("dag-1", "B", completeDownstreamRunID)] = row

	_, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: completeDownstreamRunID, Status: "done", Result: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "decode depends_on") {
		t.Fatalf("complete A err = %v, want depends_on decode failure", err)
	}
}
