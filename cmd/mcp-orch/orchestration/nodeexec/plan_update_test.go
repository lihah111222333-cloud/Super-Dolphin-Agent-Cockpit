package nodeexec

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// PlanUpdateNodes 纯函数单测覆盖 ops 与 existing 的投影结果，
// 确保 adjacency、变更列表和错误边界保持一致。

// ---- 正向路径 ----

// 单条 update：改 title，不动 depends_on。adjacency 应与 existing 一致。
func TestPlanUpdateNodes_TitleOnly_Happy(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", DependsOn: nil, Status: "pending"},
		{NodeKey: "n2", DependsOn: []string{"n1"}, Status: "pending"},
	}
	newTitle := "new title"
	ops := Ops{OpUpdateNode{NodeKey: "n1", Patch: NodePatch{Title: &newTitle}}}

	adj, changes, err := PlanUpdateNodes(ops, existing)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	if changes[0].NodeKey != "n1" {
		t.Errorf("changes[0].NodeKey = %q, want n1", changes[0].NodeKey)
	}
	if changes[0].Patch.Title == nil || *changes[0].Patch.Title != "new title" {
		t.Errorf("title patch lost: %+v", changes[0].Patch.Title)
	}
	// adjacency 不变（沿用 existing depends_on）
	if !reflect.DeepEqual(adj["n2"], []string{"n1"}) {
		t.Errorf("adj[n2] = %v, want [n1]", adj["n2"])
	}
}

// 改 depends_on 不引入环：n2 原依赖 n1，改为依赖空 → DAG 仍合法。
func TestPlanUpdateNodes_DependsOnRewire_Happy(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", DependsOn: nil, Status: "ready"},
		{NodeKey: "n2", DependsOn: []string{"n1"}, Status: "ready"},
	}
	empty := []string{}
	ops := Ops{OpUpdateNode{NodeKey: "n2", Patch: NodePatch{DependsOn: &empty}}}

	adj, changes, err := PlanUpdateNodes(ops, existing)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	// adjacency 已应用 patch：n2 → []
	if len(adj["n2"]) != 0 {
		t.Errorf("adj[n2] after rewire = %v, want []", adj["n2"])
	}
	// n1 不动
	if len(adj["n1"]) != 0 {
		t.Errorf("adj[n1] = %v, want []", adj["n1"])
	}
}

// 改 assigned_to + config 同时。
func TestPlanUpdateNodes_AssignedToAndConfig_Happy(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", DependsOn: nil, Status: "pending"},
	}
	assigned := "writer"
	cfg := json.RawMessage(`{"x":1}`)
	ops := Ops{OpUpdateNode{NodeKey: "n1", Patch: NodePatch{AssignedTo: &assigned, Config: cfg}}}

	_, changes, err := PlanUpdateNodes(ops, existing)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if changes[0].Patch.AssignedTo == nil || *changes[0].Patch.AssignedTo != "writer" {
		t.Errorf("assigned_to patch lost: %+v", changes[0].Patch.AssignedTo)
	}
	if string(changes[0].Patch.Config) != `{"x":1}` {
		t.Errorf("config patch lost: %s", changes[0].Patch.Config)
	}
}

// ---- 负面：节点不存在 ----

func TestPlanUpdateNodes_NodeNotFound(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", Status: "pending"},
	}
	t2 := "X"
	ops := Ops{OpUpdateNode{NodeKey: "nope", Patch: NodePatch{Title: &t2}}}

	_, _, err := PlanUpdateNodes(ops, existing)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !errors.Is(err, ErrUpdateNodePlan) {
		t.Errorf("err = %v, want errors.Is ErrUpdateNodePlan", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, should mention 'not found'", err)
	}
}

// ---- 负面：自环 ----

func TestPlanUpdateNodes_DependsOnSelf_Rejected(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", Status: "pending"},
	}
	self := []string{"n1"}
	ops := Ops{OpUpdateNode{NodeKey: "n1", Patch: NodePatch{DependsOn: &self}}}

	_, _, err := PlanUpdateNodes(ops, existing)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !errors.Is(err, ErrUpdateNodePlan) {
		t.Errorf("err = %v, want errors.Is ErrUpdateNodePlan", err)
	}
	if !strings.Contains(err.Error(), "depends on itself") {
		t.Errorf("err = %v, should mention 'depends on itself'", err)
	}
}

// ---- 负面：depends_on 引用不存在节点 ----

func TestPlanUpdateNodes_DependsOnUnknown(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", Status: "pending"},
	}
	bad := []string{"nope"}
	ops := Ops{OpUpdateNode{NodeKey: "n1", Patch: NodePatch{DependsOn: &bad}}}

	_, _, err := PlanUpdateNodes(ops, existing)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !errors.Is(err, ErrUpdateNodePlan) {
		t.Errorf("err = %v, want ErrUpdateNodePlan", err)
	}
	if !strings.Contains(err.Error(), "unknown node") {
		t.Errorf("err = %v, should mention 'unknown node'", err)
	}
}

// ---- 负面：节点 status 不允许 update（提前防御）----

func TestPlanUpdateNodes_StatusRunning_Rejected(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", Status: "running"},
	}
	newTitle := "x"
	ops := Ops{OpUpdateNode{NodeKey: "n1", Patch: NodePatch{Title: &newTitle}}}

	_, _, err := PlanUpdateNodes(ops, existing)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !errors.Is(err, ErrUpdateNodePlan) {
		t.Errorf("err = %v, want ErrUpdateNodePlan", err)
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("err = %v, should mention 'status'", err)
	}
}

func TestPlanUpdateNodes_StatusDone_Rejected(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", Status: "done"},
	}
	newTitle := "x"
	ops := Ops{OpUpdateNode{NodeKey: "n1", Patch: NodePatch{Title: &newTitle}}}

	_, _, err := PlanUpdateNodes(ops, existing)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !errors.Is(err, ErrUpdateNodePlan) {
		t.Errorf("err = %v, want ErrUpdateNodePlan", err)
	}
}

// ---- 负面：op 类型错 ----

func TestPlanUpdateNodes_WrongOpKind(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{{NodeKey: "n1", Status: "pending"}}
	ops := Ops{OpAddNode{Node: NodeSpec{NodeKey: "x"}}}

	_, _, err := PlanUpdateNodes(ops, existing)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !errors.Is(err, ErrUpdateNodePlan) {
		t.Errorf("err = %v, want ErrUpdateNodePlan", err)
	}
}

// ---- 空 ops 不改变现有图 ----

func TestPlanUpdateNodes_EmptyOps_Noop(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{
		{NodeKey: "n1", DependsOn: []string{"n0"}, Status: "pending"},
	}
	adj, changes, err := PlanUpdateNodes(nil, existing)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %d, want 0", len(changes))
	}
	if !reflect.DeepEqual(adj["n1"], []string{"n0"}) {
		t.Errorf("adj should mirror existing, got %v", adj["n1"])
	}
}

// ---- key 必填 ----

func TestPlanUpdateNodes_EmptyNodeKey(t *testing.T) {
	t.Parallel()
	existing := []ExistingNodeFull{{NodeKey: "n1", Status: "pending"}}
	title := "x"
	ops := Ops{OpUpdateNode{NodeKey: "  ", Patch: NodePatch{Title: &title}}}

	_, _, err := PlanUpdateNodes(ops, existing)
	if err == nil {
		t.Fatal("want err, got nil")
	}
	if !errors.Is(err, ErrUpdateNodePlan) {
		t.Errorf("err = %v, want ErrUpdateNodePlan", err)
	}
	if !strings.Contains(err.Error(), "node_key required") {
		t.Errorf("err = %v, should mention 'node_key required'", err)
	}
}
