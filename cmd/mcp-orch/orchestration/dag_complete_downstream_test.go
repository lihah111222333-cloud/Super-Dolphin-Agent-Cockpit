package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// stubNodeFlowStore 实现 NodeFlowStore + OrchestrationStore 必需方法，
// 验证 service.UpdateNodeStatus 的 Phase 3.5w 接通逻辑：status="done" 时
// 应该走 CompleteNodeAndScheduleDownstream 而不是普通 UpdateNodeStatus。
// S7.2 后补上 ListNodes，让 service 先取 from-status 走状态机校验。
type stubNodeFlowStore struct {
	taskdag.OrchestrationStore // nil 嵌入：未覆盖方法 panic 暴露遗漏

	updateCalls   []taskdag.NodeStatusUpdate
	completeCalls []taskdag.CompleteNodeInput
	completeReply *taskdag.CompleteNodeWithDownstreamResult
	completeErr   error
	listRunCalls  []int64

	// fromStatus 是 ListNodes 返回的当前 node.status（供 S7.2 转移校验取用）。
	// 未设默认 "running" 让 done 转移合法，适配多数测试不需显式设置。
	fromStatus string
}

func (s *stubNodeFlowStore) ListNodes(_ context.Context, dagKey string) ([]taskdag.Node, error) {
	return s.nodeList(dagKey, nil), nil
}

func (s *stubNodeFlowStore) ListRunNodes(_ context.Context, dagKey string, runID int64) ([]taskdag.Node, error) {
	s.listRunCalls = append(s.listRunCalls, runID)
	return s.nodeList(dagKey, &runID), nil
}

func (s *stubNodeFlowStore) nodeList(dagKey string, runID *int64) []taskdag.Node {
	status := s.fromStatus
	if status == "" {
		status = "running"
	}
	return []taskdag.Node{{DagKey: dagKey, NodeKey: "A", RunID: runID, Status: status}}
}

func (s *stubNodeFlowStore) UpdateNodeStatus(_ context.Context, input taskdag.NodeStatusUpdate) (*taskdag.Node, error) {
	s.updateCalls = append(s.updateCalls, input)
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: input.Status}, nil
}

func (s *stubNodeFlowStore) CompleteNodeAndScheduleDownstream(_ context.Context, input taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	s.completeCalls = append(s.completeCalls, input)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	if s.completeReply != nil {
		return s.completeReply, nil
	}
	return &taskdag.CompleteNodeWithDownstreamResult{
		Node: &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: input.Status},
	}, nil
}

func (s *stubNodeFlowStore) FailNodeAndCancelDownstream(_ context.Context, _ taskdag.FailNodeInput) (*taskdag.FailNodeResult, error) {
	return nil, errors.New("not used in this test")
}

func (s *stubNodeFlowStore) UpdateNodeStatusFlexible(_ context.Context, _ taskdag.FlexibleNodeStatusUpdate) (*taskdag.Node, error) {
	return nil, errors.New("not used in this test")
}

// makeServiceWithStub 构造一个绑了 stubNodeFlowStore 的最小 service 用于
// 测 dag.go::UpdateNodeStatus 的 Phase 3.5w 分支逻辑。
func makeServiceWithStub(stub taskdag.OrchestrationStore) *service {
	return &service{dagStore: stub}
}

// TestUpdateNodeStatusDone_RoutesToCompleteNodeAndScheduleDownstream:
// status="done" 应该走 CompleteNodeAndScheduleDownstream，不走普通 UpdateNodeStatus。
func TestUpdateNodeStatusDone_RoutesToCompleteNodeAndScheduleDownstream(t *testing.T) {
	stub := &stubNodeFlowStore{
		completeReply: &taskdag.CompleteNodeWithDownstreamResult{
			Node: &taskdag.Node{DagKey: "dag-1", NodeKey: "A", Status: "done"},
			ScheduledDownstream: []taskdag.ScheduledDownstreamWakeup{
				{DagKey: "dag-1", NodeKey: "B", TargetAgentID: "agent-B"},
			},
		},
	}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(context.Background(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   42,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("UpdateNodeStatus done err = %v", err)
	}
	if len(stub.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1 (should route to CompleteNodeAndScheduleDownstream)", len(stub.completeCalls))
	}
	if len(stub.updateCalls) != 0 {
		t.Fatalf("updateCalls = %d, want 0 (legacy UpdateNodeStatus path should be skipped for status=done)", len(stub.updateCalls))
	}
	got := stub.completeCalls[0]
	if got.DagKey != "dag-1" || got.NodeKey != "A" || got.RunID != 42 || got.Status != "done" {
		t.Fatalf("CompleteNodeInput wrong: %+v", got)
	}
	if len(stub.listRunCalls) != 1 || stub.listRunCalls[0] != 42 {
		t.Fatalf("ListRunNodes calls = %v, want [42]", stub.listRunCalls)
	}
}

// TestUpdateNodeStatusNonDone_KeepsLegacyUpdate:
// status != "done" 时仍走旧 UpdateNodeStatus 路径，不应触发 CompleteNodeAndScheduleDownstream。
func TestUpdateNodeStatusNonDone_KeepsLegacyUpdate(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "ready"} // ready → running 合法
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(context.Background(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   43,
		Status:  "running",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("UpdateNodeStatus running err = %v", err)
	}
	if len(stub.updateCalls) != 1 {
		t.Fatalf("updateCalls = %d, want 1 (legacy path)", len(stub.updateCalls))
	}
	if len(stub.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 (no spawn for non-done)", len(stub.completeCalls))
	}
	if got := stub.updateCalls[0]; got.RunID != 43 {
		t.Fatalf("UpdateNodeStatus RunID = %d, want 43", got.RunID)
	}
}

func TestUpdateNodeStatusRequiresRunID(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "ready"}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(context.Background(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		Status:  "running",
	})
	if err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("UpdateNodeStatus err = %v, want run_id required", err)
	}
	if len(stub.updateCalls) != 0 || len(stub.completeCalls) != 0 || len(stub.listRunCalls) != 0 {
		t.Fatalf("store should not be called without run_id: update=%d complete=%d listRun=%d",
			len(stub.updateCalls), len(stub.completeCalls), len(stub.listRunCalls))
	}
}

// TestUpdateNodeStatus_RejectsIllegalTransition:
// S7.2 验收：非法转移（pending → done 跳态）被拒。
func TestUpdateNodeStatus_RejectsIllegalTransition(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "pending"} // pending → done 非法
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(context.Background(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   44,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatalf("expected illegal transition error for pending→done, got nil")
	}
	if len(stub.updateCalls) != 0 || len(stub.completeCalls) != 0 {
		t.Fatalf("store should NOT be called when validation fails: updateCalls=%d completeCalls=%d",
			len(stub.updateCalls), len(stub.completeCalls))
	}
}

// TestUpdateNodeStatus_RejectsTerminalSourceTransition:
// done → ready 是终态出态，必须被拒。
func TestUpdateNodeStatus_RejectsTerminalSourceTransition(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "done"}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(context.Background(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   45,
		Status:  "ready",
	})
	if err == nil {
		t.Fatalf("expected terminal-source rejection for done→ready, got nil")
	}
}

// nonFlowStub 只实现 OrchestrationStore，不实现 NodeFlowStore：模拟测试 mock 或
// 老 store 实现。预期 service 退化到普通 UpdateNodeStatus（type assertion 失败）。
// S7.2 后补 ListNodes 让 service 先走转移校验。
type nonFlowStub struct {
	taskdag.OrchestrationStore // nil
	updateCalls                []taskdag.NodeStatusUpdate
}

func (s *nonFlowStub) ListNodes(_ context.Context, dagKey string) ([]taskdag.Node, error) {
	return []taskdag.Node{{DagKey: dagKey, NodeKey: "A", Status: "running"}}, nil
}

func (s *nonFlowStub) ListRunNodes(_ context.Context, dagKey string, runID int64) ([]taskdag.Node, error) {
	return []taskdag.Node{{DagKey: dagKey, NodeKey: "A", RunID: &runID, Status: "running"}}, nil
}

func (s *nonFlowStub) UpdateNodeStatus(_ context.Context, input taskdag.NodeStatusUpdate) (*taskdag.Node, error) {
	s.updateCalls = append(s.updateCalls, input)
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: input.Status}, nil
}

// TestUpdateNodeStatusDone_FallsBackWhenStoreLacksNodeFlowStore:
// dagStore 不实现 NodeFlowStore 时退化到旧路径，不破坏老 store。
func TestUpdateNodeStatusDone_FallsBackWhenStoreLacksNodeFlowStore(t *testing.T) {
	stub := &nonFlowStub{}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(context.Background(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   46,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("UpdateNodeStatus err = %v", err)
	}
	if len(stub.updateCalls) != 1 {
		t.Fatalf("updateCalls = %d, want 1 (fallback path)", len(stub.updateCalls))
	}
}
