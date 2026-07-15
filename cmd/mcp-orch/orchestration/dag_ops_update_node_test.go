package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// F4.2 update_node 真实业务实现单测。覆盖矩阵：
//   - happy: update title / assigned_to / config / depends_on
//   - happy: 与 F4.1 add_node 在同批 ops 共存
//   - cycle: update depends_on 引入二节点对环 / 自环（自环在 plan 阶段先拒）
//   - OCC: base_version stale 返 ErrVersionConflict
//   - banned key: status / node_key / node_type / agent_key patch 顶层出现 → 拒
//   - 节点 status=running / done / cancelled 时 update → 拒
//   - 节点不存在 → 拒

// ---- happy ----

func TestApplyOps_UpdateNode_TitleHappy(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 1,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "n1", Title: "old", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"title":"new"}}
		]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if resp.NewVersion != 2 {
		t.Errorf("NewVersion = %d, want 2", resp.NewVersion)
	}
	if len(stub.upsertCalls) != 1 {
		t.Fatalf("upsertCalls = %d, want 1", len(stub.upsertCalls))
	}
	got := stub.upsertCalls[0]
	if got.NodeKey != "n1" || got.Title != "new" {
		t.Errorf("upsert got node_key=%q title=%q, want n1/new", got.NodeKey, got.Title)
	}
	// node_type 沿用旧值
	if got.NodeType != "agent" {
		t.Errorf("upsert NodeType = %q, want 'agent' (preserved)", got.NodeType)
	}
}

func TestApplyOps_UpdateNode_AssignedToHappy(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "n1", Title: "t", NodeType: "agent", AssignedTo: "old", Status: "ready", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"assigned_to":"writer"}}
		]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.NewVersion != 1 {
		t.Errorf("NewVersion = %d, want 1", resp.NewVersion)
	}
	if stub.upsertCalls[0].AssignedTo != "writer" {
		t.Errorf("AssignedTo = %q, want writer", stub.upsertCalls[0].AssignedTo)
	}
	// title 保留
	if stub.upsertCalls[0].Title != "t" {
		t.Errorf("Title = %q, want preserved 't'", stub.upsertCalls[0].Title)
	}
}

func TestApplyOps_UpdateNode_ConfigHappy(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "n1", Title: "t", NodeType: "agent", Status: "pending",
				DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"},"first_turn":"old"}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: testRawConfig(t, `[
			{"op":"update_node","node_key":"n1","patch":{"config":{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"},"first_turn":"updated"}}}
		]`),
	}
	if _, err := s.ApplyOps(context.Background(), req); err != nil {
		t.Fatalf("err = %v", err)
	}
	gotCfg := string(stub.upsertCalls[0].Config)
	if !strings.Contains(gotCfg, `"first_turn":"updated"`) {
		t.Errorf("Config = %s, want updated first_turn", gotCfg)
	}
}

func TestApplyOps_UpdateNodeRejectsInvalidExecutableConfigBeforePersistence(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "auto1", Title: "auto", NodeType: "automation", Status: "pending",
				DependsOn: json.RawMessage(`[]`), Config: json.RawMessage(`{"exec":{"kind":"command_card","command_ref":"old"}}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"auto1","patch":{"config":{"exec":{"kind":"command_card"}}}}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("ApplyOps err = nil, want invalid executable config error")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "command_ref") {
		t.Fatalf("ApplyOps err = %v, want command_ref diagnostic", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Fatalf("upsertCalls = %d, want 0 before invalid config persistence", len(stub.upsertCalls))
	}
	if len(stub.bumpCalls) != 0 {
		t.Fatalf("bumpCalls = %v, want none before invalid config persistence", stub.bumpCalls)
	}
}

func TestApplyOps_UpdateNode_HybridVerifierConfigAccepted(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "verify", Title: "Verify", NodeType: "hybrid", Status: "pending",
				DependsOn: json.RawMessage(`[]`), Config: json.RawMessage(`{"exec":{"automation":{"command_ref":"old"},"verifier":{"agent_key":"old"}}}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"verify","patch":{"config":{
				"exec":{
					"automation":{"kind":"command_card","command_ref":"test_app"},
					"verifier":{"provider":"claude","model":"opus","agent_key":"reviewer","prompt_key":"main/reviewer","cwd":"/repo/app"}
				},
				"outputs":{"to_sharedfile":{"path":"reports/verify.md","lock_mode":"exclusive"},"to_node_result":true}
			}}}
		]`),
	}
	if _, err := s.ApplyOps(context.Background(), req); err != nil {
		t.Fatalf("err = %v", err)
	}
	gotCfg := string(stub.upsertCalls[0].Config)
	for _, want := range []string{`"command_ref":"test_app"`, `"agent_key":"reviewer"`, `"prompt_key":"main/reviewer"`, `"to_node_result":true`} {
		if !strings.Contains(gotCfg, want) {
			t.Errorf("Config = %s, want contains %s", gotCfg, want)
		}
	}
}

func TestApplyOps_UpdateNode_DependsOnHappy(t *testing.T) {
	t.Parallel()
	// 现有：n1（无 dep）, n2（depends [n1]）, n3（无 dep）。
	// patch: n2 → depends [n3] —— rewire 不引入环。
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
			{NodeKey: "n2", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`["n1"]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
			{NodeKey: "n3", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n2","patch":{"depends_on":["n3"]}}
		]`),
	}
	if _, err := s.ApplyOps(context.Background(), req); err != nil {
		t.Fatalf("err = %v", err)
	}
	gotDep := string(stub.upsertCalls[0].DependsOn)
	if gotDep != `["n3"]` {
		t.Errorf("DependsOn = %s, want [\"n3\"]", gotDep)
	}
}

// 同批 add + update：先 add n3 depends n1，再 update n2 depends [n3]。
// 顺序无关，校验 add 与 update 在同一事务内的 plan 综合 adjacency。
func TestApplyOps_AddPlusUpdate_SameBatch(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
			{NodeKey: "n2", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`["n1"]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: testRawConfig(t, `[
			{"op":"add_node","node":{"node_key":"n3","title":"N3","node_type":"agent","depends_on":["n1"],"config":{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}}},
			{"op":"update_node","node_key":"n2","patch":{"depends_on":["n3"]}}
		]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.NewVersion != 1 {
		t.Errorf("NewVersion = %d, want 1", resp.NewVersion)
	}
	if len(stub.upsertCalls) != 2 {
		t.Fatalf("upsert calls = %d, want 2", len(stub.upsertCalls))
	}
}

// ---- cycle ----

// update_node 引入二节点对环：现有 a（无 dep），b（dep a），patch a → dep [b] → 环。
func TestApplyOps_UpdateNode_CycleIntroducedRejected(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "a", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
			{NodeKey: "b", Status: "pending", DependsOn: json.RawMessage(`["a"]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"a","patch":{"depends_on":["b"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("cycle: want err, got nil")
	}
	if !errors.Is(err, nodeexec.ErrDAGCyclic) {
		t.Errorf("cycle: err = %v, want errors.Is ErrDAGCyclic", err)
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Errorf("cycle: err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	// 无 upsert 也无 bump
	if len(stub.upsertCalls) != 0 {
		t.Errorf("cycle: upsert should not have been called")
	}
	if len(stub.bumpCalls) != 0 {
		t.Errorf("cycle: bump should not have been called")
	}
}

// 自环也在 plan 阶段先拒。
func TestApplyOps_UpdateNode_SelfLoopRejected(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"depends_on":["n1"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("self loop: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Errorf("self loop: err = %v, want ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "depends on itself") {
		t.Errorf("self loop: err should mention self-dep, got %v", err)
	}
}

// ---- OCC ----

func TestApplyOps_UpdateNode_OCCStale(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 5,
		nodes: []taskdag.Node{
			{NodeKey: "n1", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 2,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"title":"x"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("OCC stale: want err, got nil")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("err = %v, want ErrVersionConflict", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Errorf("upsert should not be called")
	}
}

// ---- banned key: status / node_key / node_type / agent_key 在 patch 顶层 ----

func TestApplyOps_UpdateNode_BannedFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		op   string
	}{
		{"status", `{"op":"update_node","node_key":"n1","patch":{"status":"done"}}`},
		{"node_key_in_patch", `{"op":"update_node","node_key":"n1","patch":{"node_key":"other"}}`},
		{"node_type", `{"op":"update_node","node_key":"n1","patch":{"node_type":"agent"}}`},
		{"agent_key", `{"op":"update_node","node_key":"n1","patch":{"agent_key":"writer"}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := &stubDAGOpsStore{
				currentVersion: 0,
				nodes: []taskdag.Node{
					{NodeKey: "n1", Status: "pending", DependsOn: json.RawMessage(`[]`)},
				},
			}
			s := makeApplyOpsService(stub)
			req := contract.ApplyOpsRequest{
				DagKey:      "dag-a",
				BaseVersion: 0,
				Ops:         json.RawMessage(`[` + c.op + `]`),
			}
			_, err := s.ApplyOps(context.Background(), req)
			if err == nil {
				t.Fatalf("banned %s: want err, got nil", c.name)
			}
			if !errors.Is(err, ErrApplyOpsInvalid) {
				t.Errorf("banned %s: err = %v, want ErrApplyOpsInvalid", c.name, err)
			}
			// banned key 通过 nodeexec.ErrNodePatchBannedField 链上来
			if !errors.Is(err, nodeexec.ErrNodePatchBannedField) {
				t.Errorf("banned %s: err = %v, want errors.Is ErrNodePatchBannedField", c.name, err)
			}
		})
	}
}

// ---- 节点 status=running / done / cancelled 时 update → 拒 ----

func TestApplyOps_UpdateNode_RunningStatusRejected(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", Status: "running", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"title":"x"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Errorf("err = %v, want ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "status") {
		t.Errorf("err = %v, should mention 'status'", err)
	}
}

func TestApplyOps_UpdateNode_DoneStatusRejected(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", Status: "done", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"title":"x"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("done: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Errorf("err = %v, want ErrApplyOpsInvalid", err)
	}
}

// ---- 节点不存在 ----

func TestApplyOps_UpdateNode_NodeNotFound(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"nope","patch":{"title":"x"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("not found: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Errorf("err = %v, want ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, should mention 'not found'", err)
	}
}

// ---- duplicate update_node within batch (R2 P1) ----

// TestApplyOps_UpdateNode_DuplicateKeyWithinBatch 同批两个 update_node 指同
// node_key → reject，不许后写覆盖。R2 P1 设计决策：fail-fast 避免「隐式合并
// patch」语义歧义。用两种「判定接口」：errors.Is ErrApplyOpsInvalid +
// errors.Is ErrDuplicateOpForNode。
func TestApplyOps_UpdateNode_DuplicateKeyWithinBatch(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{NodeKey: "n1", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"title":"first"}},
			{"op":"update_node","node_key":"n1","patch":{"title":"second"}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("duplicate update within batch: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Errorf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !errors.Is(err, ErrDuplicateOpForNode) {
		t.Errorf("err = %v, want errors.Is ErrDuplicateOpForNode", err)
	}
	if !strings.Contains(err.Error(), "n1") {
		t.Errorf("err = %v, should mention 'n1'", err)
	}
	// 同时必须没走 store 写入（fail-fast 在事务前）。
	if len(stub.upsertCalls) != 0 {
		t.Errorf("dup update: store 不该被调用，upsertCalls=%d", len(stub.upsertCalls))
	}
}

// TestApplyOps_UpdateNode_DistinctKeysInBatch 防误伤：两个不同 node_key 的
// update_node 必须能共存。与 dup 检测配对吃到一起越界。
func TestApplyOps_UpdateNode_DistinctKeysInBatch(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "n1", Title: "old1", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
			{DagKey: "dag-a", NodeKey: "n2", Title: "old2", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`), Config: testRawConfig(t, `{"exec":{"agent_key":"worker","cwd":"/tmp/node-cwd"}}`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"n1","patch":{"title":"new1"}},
			{"op":"update_node","node_key":"n2","patch":{"title":"new2"}}
		]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("distinct keys: err = %v, want nil", err)
	}
	if resp.NewVersion != 1 {
		t.Errorf("NewVersion = %d, want 1", resp.NewVersion)
	}
	if len(stub.upsertCalls) != 2 {
		t.Errorf("distinct keys: upsertCalls=%d, want 2", len(stub.upsertCalls))
	}
}

// _ = nodeexec import 保留：上面测试已在用 nodeexec.ErrNodePatchBannedField。
var _ = nodeexec.ErrNodePatchBannedField
