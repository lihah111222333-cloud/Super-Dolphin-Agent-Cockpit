package taskdag

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"
)

func TestFailNodeAndCancelDownstream_FailFastFalse_OnlyMarksCurrent(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "launch transient exhausted",
		FailFast: false,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "failed" {
		t.Fatalf("res.Node = %+v, want status=failed", res.Node)
	}
	if len(res.CanceledDownstream) != 0 {
		t.Fatalf("canceled = %v, want []", res.CanceledDownstream)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "pending" {
		t.Fatalf("B status = %q, want pending (no fail-fast cascade)", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "C", runID)].Status; got != "pending" {
		t.Fatalf("C status = %q, want pending (no fail-fast cascade)", got)
	}
	// Primary failure result must be tagged as exhausted_retries.
	var reason failNodeReason
	if err := json.Unmarshal(res.Node.Result, &reason); err != nil {
		t.Fatalf("unmarshal A result: %v", err)
	}
	if reason.Kind != "exhausted_retries" || reason.Reason != "launch transient exhausted" {
		t.Fatalf("primary reason = %+v, want kind=exhausted_retries", reason)
	}
}

func TestFailNodeAndCancelDownstream_PrimaryTerminalFence(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "done", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})

	_, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "late materialization failure",
		FailFast: true,
	})
	if err == nil {
		t.Fatalf("FailNodeAndCancelDownstream on done primary error = nil, want fence rejection")
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "A", runID)].Status; got != "done" {
		t.Fatalf("A status = %q, want done (terminal primary must not be rewritten)", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "pending" {
		t.Fatalf("B status = %q, want pending (cascade must not run after primary fence rejection)", got)
	}
}

func TestFailNodeAndCancelDownstream_CascadeTerminalRaceSkips(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	db.beforeFailNonTerminal = func(dagKey, nodeKey string) {
		if dagKey != "dag-1" || nodeKey != "B" {
			return
		}
		key := dagRunNodeKey(dagKey, nodeKey, runID)
		row := db.nodes[key]
		row.Status = "done"
		db.nodes[key] = row
	}

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "retry exhausted",
		FailFast: true,
	})
	if err != nil {
		t.Fatalf("FailNodeAndCancelDownstream error = %v, want nil when cascade row races terminal", err)
	}
	if res.Node == nil || res.Node.Status != "failed" {
		t.Fatalf("primary node = %+v, want failed", res.Node)
	}
	if len(res.CanceledDownstream) != 0 {
		t.Fatalf("CanceledDownstream = %+v, want empty for raced terminal downstream", res.CanceledDownstream)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "done" {
		t.Fatalf("B status = %q, want done (cascade race must not rewrite terminal)", got)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_CascadesTransitivePending(t *testing.T) {
	t.Parallel()

	// A → B → C ; A → D ; D done already (must not be touched).
	// Failing A with fail_fast=true must cascade-fail B + C, leave D done.
	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"A"}, status: "done", agent: "agent-d"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "launch transient exhausted",
		FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	requireFailNodeStatus(t, res, "failed")
	requireCanceledNodeKeys(t, res.CanceledDownstream, []string{"B", "C"})
	requireRunNodeStatus(t, db, runID, "B", "failed")
	requireRunNodeStatus(t, db, runID, "C", "failed")
	// D was already done — fail-fast must not rewrite terminal nodes.
	requireRunNodeStatus(t, db, runID, "D", "done")
	// Cascade reason on B should reference A as the originator.
	requireCascadeReason(t, db, runID, "B", "A")
}

func requireFailNodeStatus(t *testing.T, res *FailNodeResult, want string) {
	t.Helper()
	if res.Node == nil || res.Node.Status != want {
		t.Fatalf("res.Node = %+v, want status=%s", res.Node, want)
	}
}

func requireCanceledNodeKeys(t *testing.T, canceled []CanceledDownstreamNode, want []string) {
	t.Helper()
	if got := canceledKeys(canceled); !equalStrings(got, want) {
		t.Fatalf("canceled = %v, want %v", got, want)
	}
}

func requireRunNodeStatus(t *testing.T, db *fakeTaskDAGDB, runID int64, nodeKey, want string) {
	t.Helper()
	if got := db.nodes[dagRunNodeKey("dag-1", nodeKey, runID)].Status; got != want {
		t.Fatalf("%s status = %q, want %s", nodeKey, got, want)
	}
}

func requireCascadeReason(t *testing.T, db *fakeTaskDAGDB, runID int64, nodeKey, causedBy string) {
	t.Helper()
	var reason failNodeReason
	if err := json.Unmarshal(db.nodes[dagRunNodeKey("dag-1", nodeKey, runID)].Result, &reason); err != nil {
		t.Fatalf("unmarshal %s result: %v", nodeKey, err)
	}
	if reason.Kind != "cascade" || reason.CausedByNode != causedBy {
		t.Fatalf("%s reason = %+v, want kind=cascade caused_by=%s", nodeKey, reason, causedBy)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_DiamondCascade(t *testing.T) {
	t.Parallel()

	// A → {B, C} → D ; D depends on both B and C.
	// Failing A with fail-fast must cascade B, C, AND D (transitive closure).
	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: runID, Reason: "boom", FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	gotKeys := canceledKeys(res.CanceledDownstream)
	if want := []string{"B", "C", "D"}; !equalStrings(gotKeys, want) {
		t.Fatalf("canceled = %v, want %v", gotKeys, want)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "D", runID)].Status; got != "failed" {
		t.Fatalf("D status = %q, want failed", got)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_RunningDownstreamNotTouched(t *testing.T) {
	t.Parallel()

	// Once a downstream node is already running it must not be rewritten —
	// the running attempt finishes on its own; cascade only affects pending.
	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "running", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: runID, Reason: "boom", FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	gotKeys := canceledKeys(res.CanceledDownstream)
	if want := []string{"C"}; !equalStrings(gotKeys, want) {
		t.Fatalf("canceled = %v, want %v (B was running, must skip; C still cascaded transitively)", gotKeys, want)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "running" {
		t.Fatalf("B status = %q, want running (must not be rewritten)", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "C", runID)].Status; got != "failed" {
		t.Fatalf("C status = %q, want failed", got)
	}
}

func seedFailDownstreamRuntimeDAG(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode) int64 {
	t.Helper()
	runID := db.runs["run-1"].ID
	seedDAGRows(t, db, now, nodes, runID)
	return runID
}

func canceledKeys(items []CanceledDownstreamNode) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.NodeKey)
	}
	sort.Strings(keys)
	return keys
}
