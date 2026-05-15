package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// F4.5 dag.status='running' 不变量单测。覆盖矩阵：
//   - draft DAG（默认 stub.dagStatus 空字符串 = "draft"）：update_node + add_node 仍 happy
//   - running DAG + update_node → 拒为 ErrApplyOpsInvalid
//   - running DAG + add_node depends_on 指向 pending 节点 → 拒
//   - running DAG + add_node depends_on 指向 done 节点 → 通过
//   - running DAG + add_node 无 depends_on → 通过（depends_on 为空集天然满足条件）

// draft DAG 下 update_node 仍 happy —— 验证不变量只对 running 触发，不动 happy path。
func TestApplyOpsF45DraftUpdateNodeHappy(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagStatus:      "", // 默认 -> draft
		nodes: []taskdag.Node{
			{NodeKey: "n1", Title: "old", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`)},
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
	if _, err := s.ApplyOps(context.Background(), req); err != nil {
		t.Fatalf("draft DAG update_node should be happy, got err = %v", err)
	}
}

// running DAG 下 update_node 必须被拒。
func TestApplyOpsF45RunningUpdateNodeRejected(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagStatus:      "running",
		nodes: []taskdag.Node{
			{NodeKey: "n1", Title: "old", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`)},
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
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG + update_node: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("running DAG + update_node: err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "running") {
		t.Errorf("err should mention running: %v", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Errorf("running DAG + update_node: should not upsert, got %d calls", len(stub.upsertCalls))
	}
}

// running DAG + add_node depends_on 指向 pending 节点 → 拒。
func TestApplyOpsF45RunningAddNodeDependsOnPendingRejected(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagStatus:      "running",
		nodes: []taskdag.Node{
			{NodeKey: "n0", Title: "n0", NodeType: "agent", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"N1","node_type":"agent","depends_on":["n0"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG + add_node deps on pending: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "done node") {
		t.Errorf("err should mention 'done node', got %v", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Errorf("should not upsert when validation fails, got %d calls", len(stub.upsertCalls))
	}
}

// running DAG + add_node depends_on 指向 done 节点 → 通过。
func TestApplyOpsF45RunningAddNodeDependsOnDoneHappy(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagStatus:      "running",
		nodes: []taskdag.Node{
			{NodeKey: "n0", Title: "n0", NodeType: "agent", Status: "done", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"N1","node_type":"agent","depends_on":["n0"]}}
		]`),
	}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("running DAG + add_node deps on done: err = %v, want nil", err)
	}
	if resp.NewVersion != 2 {
		t.Errorf("NewVersion = %d, want 2", resp.NewVersion)
	}
	if len(stub.upsertCalls) != 1 || stub.upsertCalls[0].NodeKey != "n1" {
		t.Errorf("upsertCalls = %v, want one node 'n1'", stub.upsertCalls)
	}
}

// running DAG + add_node 无 depends_on → 通过（空集天然满足「全部指向 done」条件）。
func TestApplyOpsF45RunningAddNodeNoDepsHappy(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		dagStatus:      "running",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"new","title":"NEW","node_type":"agent"}}
		]`),
	}
	if _, err := s.ApplyOps(context.Background(), req); err != nil {
		t.Fatalf("running DAG + add_node no deps: err = %v, want nil", err)
	}
	if len(stub.upsertCalls) != 1 {
		t.Errorf("upsertCalls = %d, want 1", len(stub.upsertCalls))
	}
}

// running DAG + add_node depends_on 指向同批新节点（status 默认 pending）→ 拒。
// 这是蓝图 v2 §5 显式的限制：新节点不能互相等。
func TestApplyOpsF45RunningAddNodeDependsOnSameBatchRejected(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 0,
		dagStatus:      "running",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"a","title":"A","node_type":"agent"}},
			{"op":"add_node","node":{"node_key":"b","title":"B","node_type":"agent","depends_on":["a"]}}
		]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG + add_node deps on same-batch new node: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
}
