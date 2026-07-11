package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// running 状态 DAG 的 apply_ops 不变量单测。覆盖矩阵：
//   - draft DAG（默认 stub.dagStatus 空字符串 = "draft"）：update_node + add_node 仍走正常路径
//   - running DAG + update_node → 拒为 ErrApplyOpsInvalid
//   - running DAG + add_node depends_on 指向 pending 节点 → 当前先 fail-fast 拒绝
//   - running DAG + add_node depends_on 指向 done 节点 → 当前先 fail-fast 拒绝
//   - running DAG + add_node 无 depends_on → 当前先 fail-fast 拒绝

// draft DAG 下 update_node 仍走正常路径，验证不变量只对 running 触发。
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

// running DAG + add_node depends_on 指向 pending 节点同样不能只写模板节点。
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
	if !strings.Contains(err.Error(), "runtime append") {
		t.Errorf("err should mention runtime append, got %v", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Errorf("should not upsert when validation fails, got %d calls", len(stub.upsertCalls))
	}
}

// running DAG + add_node 目前只会写模板节点，当前 run 不会出现或调度新节点。
// 在 runtime append 真闭环接入前必须 fail-fast。
func TestApplyOpsF45RunningAddNodeDependsOnDoneRejectedUntilRuntimeAppendExists(t *testing.T) {
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
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG + add_node deps on done: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "runtime append") {
		t.Fatalf("err = %v, want mention runtime append", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Errorf("upsertCalls = %v, want no template upsert for running add_node", stub.upsertCalls)
	}
}

// running DAG + add_node 无 depends_on 同样不能只写模板节点。
func TestApplyOpsF45RunningAddNodeNoDepsRejectedUntilRuntimeAppendExists(t *testing.T) {
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
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG + add_node no deps: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Errorf("upsertCalls = %d, want 0", len(stub.upsertCalls))
	}
}

func TestApplyOpsF45ActiveRunAddNodeRejectedUntilRuntimeAppendExists(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagStatus:      "ready",
		activeRuns:     1,
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
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("active run + add_node: want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "active running run") || !strings.Contains(err.Error(), "runtime append") {
		t.Fatalf("err = %v, want active running run and runtime append", err)
	}
	if len(stub.upsertCalls) != 0 {
		t.Errorf("upsertCalls = %v, want no template upsert for active run add_node", stub.upsertCalls)
	}
}

func TestApplyOpsF45RunningAddNodeRejectsBeforeListNodes(t *testing.T) {
	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagStatus:      "running",
		listErr:        errors.New("list nodes unavailable"),
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"add_node","node":{"node_key":"n1","title":"N1","node_type":"agent"}}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG + add_node: want fail-fast error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "runtime append") {
		t.Fatalf("err = %v, want mention runtime append", err)
	}
	if stub.listCalls != 0 {
		t.Fatalf("ListNodes calls = %d, want 0 for running add_node fail-fast", stub.listCalls)
	}
}

// running DAG + add_node depends_on 指向同批新节点（status 默认 pending）→ 拒。
// 同批新增节点还没有稳定的 runtime 顺序，当前实现不允许新节点互相依赖。
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
