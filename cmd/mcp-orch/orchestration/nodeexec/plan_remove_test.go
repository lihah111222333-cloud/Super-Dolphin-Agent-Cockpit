package nodeexec

import (
	"errors"
	"strings"
	"testing"
)

func TestPlanRemoveNodes_LeafHappy(t *testing.T) {
	t.Parallel()

	adj := map[string][]string{
		"root": nil,
		"leaf": {"root"},
	}
	existing := []ExistingNodeFull{
		{NodeKey: "root", Status: string(NodeStatusPending)},
		{NodeKey: "leaf", Status: string(NodeStatusReady), DependsOn: []string{"root"}},
	}

	pruned, changes, err := PlanRemoveNodes(Ops{OpRemoveNode{NodeKey: "leaf"}}, existing, adj)
	if err != nil {
		t.Fatalf("PlanRemoveNodes err = %v, want nil", err)
	}
	if len(changes) != 1 || changes[0].NodeKey != "leaf" {
		t.Fatalf("changes = %#v, want leaf", changes)
	}
	if _, ok := pruned["leaf"]; ok {
		t.Fatalf("pruned adjacency still contains leaf: %#v", pruned)
	}
	if _, ok := adj["leaf"]; !ok {
		t.Fatalf("input adjacency was mutated: %#v", adj)
	}
}

func TestPlanRemoveNodes_NodeKeyRequired(t *testing.T) {
	t.Parallel()

	_, _, err := PlanRemoveNodes(Ops{OpRemoveNode{NodeKey: "   "}}, nil, nil)
	if err == nil {
		t.Fatal("PlanRemoveNodes empty node_key error = nil")
	}
	if !errors.Is(err, ErrRemoveNodePlan) {
		t.Fatalf("err = %v, want errors.Is ErrRemoveNodePlan", err)
	}
	if !strings.Contains(err.Error(), "node_key required") {
		t.Fatalf("err = %v, want node_key required", err)
	}
}

func TestPlanRemoveNodes_NotFoundRejected(t *testing.T) {
	t.Parallel()

	_, _, err := PlanRemoveNodes(Ops{OpRemoveNode{NodeKey: "missing"}}, []ExistingNodeFull{
		{NodeKey: "root", Status: string(NodeStatusPending)},
	}, map[string][]string{"root": nil})
	if err == nil {
		t.Fatal("PlanRemoveNodes missing node error = nil")
	}
	if !errors.Is(err, ErrRemoveNodePlan) {
		t.Fatalf("err = %v, want errors.Is ErrRemoveNodePlan", err)
	}
}

func TestPlanRemoveNodes_RunningStatusRejected(t *testing.T) {
	t.Parallel()

	_, _, err := PlanRemoveNodes(Ops{OpRemoveNode{NodeKey: "n1"}}, []ExistingNodeFull{
		{NodeKey: "n1", Status: string(NodeStatusRunning)},
	}, map[string][]string{"n1": nil})
	if err == nil {
		t.Fatal("PlanRemoveNodes running node error = nil")
	}
	if !errors.Is(err, ErrRemoveNodePlan) {
		t.Fatalf("err = %v, want errors.Is ErrRemoveNodePlan", err)
	}
	if !strings.Contains(err.Error(), "not removable") {
		t.Fatalf("err = %v, want not removable", err)
	}
}

func TestPlanRemoveNodes_DependedOnRejected(t *testing.T) {
	t.Parallel()

	_, _, err := PlanRemoveNodes(Ops{OpRemoveNode{NodeKey: "root"}}, []ExistingNodeFull{
		{NodeKey: "root", Status: string(NodeStatusPending)},
		{NodeKey: "child", Status: string(NodeStatusPending), DependsOn: []string{"root"}},
	}, map[string][]string{
		"root":  nil,
		"child": {"root"},
	})
	if err == nil {
		t.Fatal("PlanRemoveNodes depended-on node error = nil")
	}
	if !errors.Is(err, ErrRemoveNodePlan) {
		t.Fatalf("err = %v, want errors.Is ErrRemoveNodePlan", err)
	}
	if !strings.Contains(err.Error(), "depended on") {
		t.Fatalf("err = %v, want depended on", err)
	}
}

func TestPlanRemoveNodes_DuplicateRemoveRejected(t *testing.T) {
	t.Parallel()

	_, _, err := PlanRemoveNodes(Ops{
		OpRemoveNode{NodeKey: "leaf"},
		OpRemoveNode{NodeKey: "leaf"},
	}, []ExistingNodeFull{
		{NodeKey: "leaf", Status: string(NodeStatusPending)},
	}, map[string][]string{"leaf": nil})
	if err == nil {
		t.Fatal("PlanRemoveNodes duplicate remove error = nil")
	}
	if !errors.Is(err, ErrRemoveNodePlan) {
		t.Fatalf("err = %v, want errors.Is ErrRemoveNodePlan", err)
	}
	if !strings.Contains(err.Error(), "both remove") {
		t.Fatalf("err = %v, want both remove", err)
	}
}
