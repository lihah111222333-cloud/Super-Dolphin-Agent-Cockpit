package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// ApplyOps 的顶层校验必须先区分 JSON 解码、op 缺失/非法和 base_version 负数等输入错误。
// 合法 ops 才能进入业务分发，避免坏请求穿透到存储或节点执行层。

// TestApplyOps_UpdateDAGDispatches 验证 update_dag 会进入业务层并推进 DAG 版本。
func TestApplyOps_UpdateDAGDispatches(t *testing.T) {
	t.Parallel()
	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	ops := json.RawMessage(`[
		{"op":"update_dag","patch":{"title":"t"}}
	]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 1, Ops: ops}
	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps err = %v, want nil", err)
	}
	if resp.NewVersion != 2 {
		t.Fatalf("NewVersion = %d, want 2", resp.NewVersion)
	}
}

// TestApplyOps_InvalidOpKind 验证未知 op 会返回输入错误，并在错误文本中保留原始 op。
func TestApplyOps_InvalidOpKind(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`[{"op":"unknown_kind","whatever":1}]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 0, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if errors.Is(err, ErrLifecycleNotImplemented) {
		t.Fatalf("ApplyOps unknown op should NOT return ErrLifecycleNotImplemented; got %v", err)
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
	if !strings.Contains(err.Error(), "unknown_kind") {
		t.Fatalf("ApplyOps err should mention the offending op kind, got: %v", err)
	}
}

// TestApplyOps_MissingOpKind 验证缺少 op 字段时不会落到生命周期 stub。
func TestApplyOps_MissingOpKind(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`[{"node_key":"n1"}]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 0, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
	if errors.Is(err, ErrLifecycleNotImplemented) {
		t.Fatalf("ApplyOps missing op should NOT return ErrLifecycleNotImplemented; got %v", err)
	}
}

// TestApplyOps_UnmarshalFails 验证非法 JSON 会停在参数解码边界。
func TestApplyOps_UnmarshalFails(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`not a json array`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 0, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
}

// TestApplyOps_PreservesNodeexecSentinelErrorChain 验证节点补丁校验错误不会被 ApplyOps 包装丢失。
func TestApplyOps_PreservesNodeexecSentinelErrorChain(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`[{"op":"update_dag","patch":{"status":"running"}}]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: 0, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
	if !errors.Is(err, nodeexec.ErrDAGPatchUnknownField) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, nodeexec.ErrDAGPatchUnknownField)", err)
	}
}

// TestApplyOps_NegativeBaseVersion 验证负 base_version 会在业务分发前失败。
func TestApplyOps_NegativeBaseVersion(t *testing.T) {
	t.Parallel()
	s := &service{}
	ops := json.RawMessage(`[{"op":"remove_node","node_key":"n1"}]`)
	req := contract.ApplyOpsRequest{DagKey: "dag-a", BaseVersion: -1, Ops: ops}
	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatalf("ApplyOps want error, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("ApplyOps err = %v, want errors.Is(err, ErrApplyOpsInvalid)", err)
	}
	if !strings.Contains(err.Error(), "base_version") {
		t.Fatalf("ApplyOps err should mention base_version, got: %v", err)
	}
}

// TestApplyOps_StoreNotConfigured 验证裸构造 service 时返回明确的存储未接线错误。
func TestApplyOps_StoreNotConfigured(t *testing.T) {
	t.Parallel()
	s := &service{}
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 0,
		Ops:         json.RawMessage(`[]`),
	}
	_, err := s.ApplyOps(context.Background(), req)
	if !errors.Is(err, ErrApplyOpsStoreNotConfigured) {
		t.Fatalf("ApplyOps without dag store err = %v, want ErrApplyOpsStoreNotConfigured", err)
	}
}

func TestCreateDAGRejectsInvalidTopologyBeforeStore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		nodes []contract.CreateDAGNodeRequest
		want  string
	}{
		{
			name: "duplicate node key",
			nodes: []contract.CreateDAGNodeRequest{
				{NodeKey: "a", Title: "A"},
				{NodeKey: " a ", Title: "A2"},
			},
			want: "already exists",
		},
		{
			name:  "unknown dependency",
			nodes: []contract.CreateDAGNodeRequest{{NodeKey: "a", Title: "A", DependsOn: []string{"missing"}}},
			want:  "unknown node",
		},
		{
			name: "cycle",
			nodes: []contract.CreateDAGNodeRequest{
				{NodeKey: "a", Title: "A", DependsOn: []string{"b"}},
				{NodeKey: "b", Title: "B", DependsOn: []string{"a"}},
			},
			want: "cycle",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &service{}
			_, err := s.CreateDAG(context.Background(), contract.CreateDAGRequest{
				DagKey:    "dag-a",
				Title:     "DAG A",
				CreatedBy: "agent-a",
				Nodes:     tc.nodes,
			})
			if err == nil {
				t.Fatal("CreateDAG() error = nil, want invalid topology error")
			}
			if strings.Contains(err.Error(), "dag store is not configured") {
				t.Fatalf("CreateDAG() validated after store lookup, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CreateDAG() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestCreateDAGPropagatesDuplicateDagKeyConflict(t *testing.T) {
	t.Parallel()
	stub := &stubCreateDAGStore{upsertErr: platformdb.ErrConflict}
	s := &service{dagStore: stub}

	_, err := s.CreateDAG(context.Background(), contract.CreateDAGRequest{
		DagKey:    "dag-a",
		Title:     "DAG A",
		CreatedBy: "agent-a",
	})
	if err == nil {
		t.Fatal("CreateDAG() error = nil, want duplicate dag_key conflict")
	}
	if !errors.Is(err, platformdb.ErrConflict) {
		t.Fatalf("CreateDAG() error = %v, want platformdb.ErrConflict", err)
	}
	if stub.upsertCalls != 1 {
		t.Fatalf("UpsertDAG calls = %d, want 1 conflict from create-only insert", stub.upsertCalls)
	}
}

type stubCreateDAGStore struct {
	taskdag.OrchestrationStore
	existing    *taskdag.DAG
	upsertErr   error
	upsertCalls int
}

func (s *stubCreateDAGStore) WithTx(ctx context.Context, fn func(taskdag.DAGMutationStore) error) error {
	return fn(s)
}

func (s *stubCreateDAGStore) GetDAG(context.Context, string) (*taskdag.DAG, error) {
	if s.existing != nil {
		return s.existing, nil
	}
	return nil, platformdb.ErrNotFound
}

func (s *stubCreateDAGStore) ListNodes(context.Context, string) ([]taskdag.Node, error) {
	return nil, nil
}

func (s *stubCreateDAGStore) UpsertDAG(_ context.Context, dag taskdag.DAG) (*taskdag.DAG, error) {
	s.upsertCalls++
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	return &dag, nil
}

func (s *stubCreateDAGStore) UpsertNode(_ context.Context, node taskdag.Node) (*taskdag.Node, error) {
	return &node, nil
}

func (s *stubCreateDAGStore) UpdateNodeStatus(context.Context, taskdag.NodeStatusUpdate) (*taskdag.Node, error) {
	return nil, errors.New("unexpected UpdateNodeStatus")
}
