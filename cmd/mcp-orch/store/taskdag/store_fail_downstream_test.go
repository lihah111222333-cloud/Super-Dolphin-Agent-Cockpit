package taskdag

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
)

func TestFailNodeAndCancelDownstream_FailFastFalse_OnlyMarksCurrent(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
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
	if got := db.nodes[dagNodeKey("dag-1", "B")].Status; got != "pending" {
		t.Fatalf("B status = %q, want pending (no fail-fast cascade)", got)
	}
	if got := db.nodes[dagNodeKey("dag-1", "C")].Status; got != "pending" {
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

func TestFailNodeAndCancelDownstream_FailFastTrue_CascadesTransitivePending(t *testing.T) {
	t.Parallel()

	// A → B → C ; A → D ; D done already (must not be touched).
	// Failing A with fail_fast=true must cascade-fail B + C, leave D done.
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"A"}, status: "done", agent: "agent-d"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		Reason:   "launch transient exhausted",
		FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "failed" {
		t.Fatalf("res.Node = %+v, want status=failed", res.Node)
	}
	gotKeys := canceledKeys(res.CanceledDownstream)
	if want := []string{"B", "C"}; !equalStrings(gotKeys, want) {
		t.Fatalf("canceled = %v, want %v", gotKeys, want)
	}
	if got := db.nodes[dagNodeKey("dag-1", "B")].Status; got != "failed" {
		t.Fatalf("B status = %q, want failed", got)
	}
	if got := db.nodes[dagNodeKey("dag-1", "C")].Status; got != "failed" {
		t.Fatalf("C status = %q, want failed", got)
	}
	// D was already done — fail-fast must not rewrite terminal nodes.
	if got := db.nodes[dagNodeKey("dag-1", "D")].Status; got != "done" {
		t.Fatalf("D status = %q, want done (terminal must not be rewritten)", got)
	}
	// Cascade reason on B should reference A as the originator.
	var reason failNodeReason
	if err := json.Unmarshal(db.nodes[dagNodeKey("dag-1", "B")].Result, &reason); err != nil {
		t.Fatalf("unmarshal B result: %v", err)
	}
	if reason.Kind != "cascade" || reason.CausedByNode != "A" {
		t.Fatalf("B reason = %+v, want kind=cascade caused_by=A", reason)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_DiamondCascade(t *testing.T) {
	t.Parallel()

	// A → {B, C} → D ; D depends on both B and C.
	// Failing A with fail-fast must cascade B, C, AND D (transitive closure).
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey: "dag-1", NodeKey: "A", Reason: "boom", FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	gotKeys := canceledKeys(res.CanceledDownstream)
	if want := []string{"B", "C", "D"}; !equalStrings(gotKeys, want) {
		t.Fatalf("canceled = %v, want %v", gotKeys, want)
	}
	if got := db.nodes[dagNodeKey("dag-1", "D")].Status; got != "failed" {
		t.Fatalf("D status = %q, want failed", got)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_RunningDownstreamNotTouched(t *testing.T) {
	t.Parallel()

	// Once a downstream node is already running it must not be rewritten —
	// the running attempt finishes on its own; cascade only affects pending.
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "running", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey: "dag-1", NodeKey: "A", Reason: "boom", FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	gotKeys := canceledKeys(res.CanceledDownstream)
	if want := []string{"C"}; !equalStrings(gotKeys, want) {
		t.Fatalf("canceled = %v, want %v (B was running, must skip; C still cascaded transitively)", gotKeys, want)
	}
	if got := db.nodes[dagNodeKey("dag-1", "B")].Status; got != "running" {
		t.Fatalf("B status = %q, want running (must not be rewritten)", got)
	}
	if got := db.nodes[dagNodeKey("dag-1", "C")].Status; got != "failed" {
		t.Fatalf("C status = %q, want failed", got)
	}
}

func canceledKeys(items []CanceledDownstreamNode) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.NodeKey)
	}
	sort.Strings(keys)
	return keys
}
