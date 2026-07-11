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

// remove_node 单测覆盖 DAG 模板删除的边界：
//   - 删除没有下游依赖的 leaf node 时推进 DAG version。
//   - 被其他节点 depends_on 的节点必须拒绝，且不写入。
//   - active run 或 running DAG 禁止模板改写。
//   - 目标节点不存在或同批先改后删时保持 fail-fast。

func TestApplyOps_RemoveNode_LeafHappy(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 4,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "root", Status: "pending", DependsOn: json.RawMessage(`[]`)},
			{DagKey: "dag-a", NodeKey: "leaf", Status: "pending", DependsOn: json.RawMessage(`["root"]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 4,
		Ops: json.RawMessage(`[
			{"op":"remove_node","node_key":"leaf"}
		]`),
	}

	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps remove leaf err = %v, want nil", err)
	}
	if resp.NewVersion != 5 {
		t.Fatalf("NewVersion = %d, want 5", resp.NewVersion)
	}
	if got := stub.deleteCalls; len(got) != 1 || got[0] != "leaf" {
		t.Fatalf("deleteCalls = %v, want [leaf]", got)
	}
	if len(stub.upsertCalls) != 0 {
		t.Fatalf("remove_node should not upsert, got %d calls", len(stub.upsertCalls))
	}
}

func TestApplyOps_RemoveNode_ActiveRunRejectedEvenWhenDAGDraft(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 4,
		activeRuns:     1,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "leaf", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 4,
		Ops: json.RawMessage(`[
			{"op":"remove_node","node_key":"leaf"}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("active run remove_node: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "running") {
		t.Fatalf("err = %v, want mention running/active run", err)
	}
	if len(stub.deleteCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("deleteCalls=%v bumpCalls=%v, want no writes", stub.deleteCalls, stub.bumpCalls)
	}
}

func TestApplyOps_RemoveNode_DeleteLostStatusRaceRejected(t *testing.T) {
	t.Parallel()

	zeroRows := int64(0)
	stub := &stubDAGOpsStore{
		currentVersion: 4,
		deleteRows:     &zeroRows,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "leaf", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 4,
		Ops: json.RawMessage(`[
			{"op":"remove_node","node_key":"leaf"}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("delete race returned zero rows: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if len(stub.deleteCalls) != 1 {
		t.Fatalf("deleteCalls = %v, want one attempted delete", stub.deleteCalls)
	}
	if len(stub.bumpCalls) != 0 {
		t.Fatalf("bumpCalls = %v, want no version bump after failed delete", stub.bumpCalls)
	}
}

func TestApplyOps_RemoveNode_SameBatchUpdateThenRemoveRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 2,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "leaf", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 2,
		Ops: json.RawMessage(`[
			{"op":"update_node","node_key":"leaf","patch":{"title":"new"}},
			{"op":"remove_node","node_key":"leaf"}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("same-batch update+remove: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !errors.Is(err, ErrDuplicateOpForNode) {
		t.Fatalf("err = %v, want errors.Is ErrDuplicateOpForNode", err)
	}
	if len(stub.upsertCalls) != 0 || len(stub.deleteCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("upsertCalls=%v deleteCalls=%v bumpCalls=%v, want fail-fast before writes", stub.upsertCalls, stub.deleteCalls, stub.bumpCalls)
	}
}

func TestApplyOps_RemoveNode_DependedOnRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 0,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "root", Status: "pending", DependsOn: json.RawMessage(`[]`)},
			{DagKey: "dag-a", NodeKey: "child", Status: "pending", DependsOn: json.RawMessage(`["root"]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops: json.RawMessage(`[
			{"op":"remove_node","node_key":"root"}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("remove depended-on node: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "depended on") {
		t.Fatalf("err = %v, want mention depended on", err)
	}
	if len(stub.deleteCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("deleteCalls=%v bumpCalls=%v, want no writes", stub.deleteCalls, stub.bumpCalls)
	}
}

func TestApplyOps_RemoveNode_RunningDAGRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagStatus:      "running",
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "leaf", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"remove_node","node_key":"leaf"}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG remove_node: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "running") {
		t.Fatalf("err = %v, want mention running", err)
	}
	if len(stub.deleteCalls) != 0 {
		t.Fatalf("deleteCalls = %v, want no writes", stub.deleteCalls)
	}
}

func TestApplyOps_RemoveNode_NotFoundRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 2,
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "existing", Status: "pending", DependsOn: json.RawMessage(`[]`)},
		},
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 2,
		Ops: json.RawMessage(`[
			{"op":"remove_node","node_key":"missing"}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("remove missing node: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want mention not found", err)
	}
	if len(stub.deleteCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("deleteCalls=%v bumpCalls=%v, want no writes", stub.deleteCalls, stub.bumpCalls)
	}
}
