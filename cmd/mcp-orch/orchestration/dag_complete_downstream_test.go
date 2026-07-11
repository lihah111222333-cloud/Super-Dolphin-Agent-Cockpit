package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type stubNodeFlowStore struct {
	taskdag.OrchestrationStore // nil 嵌入：未覆盖方法 panic 暴露遗漏

	updateCalls   []taskdag.NodeStatusUpdate
	completeCalls []taskdag.CompleteNodeInput
	failCalls     []taskdag.FailNodeInput
	completeReply *taskdag.CompleteNodeWithDownstreamResult
	completeErr   error
	failReply     *taskdag.FailNodeResult
	failErr       error
	listRunCalls  []int64
	renewCalls    []taskdag.RenewWorkerLeaseInput
	renewRows     int64
	renewRowsSet  bool
	renewErr      error

	fromStatus string
	assignedTo string
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
	assignedTo := s.assignedTo
	if assignedTo == "" {
		assignedTo = "agent-A"
	}
	return []taskdag.Node{{DagKey: dagKey, NodeKey: "A", RunID: runID, Status: status, AssignedTo: assignedTo}}
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

func (s *stubNodeFlowStore) FailNodeAndCancelDownstream(_ context.Context, input taskdag.FailNodeInput) (*taskdag.FailNodeResult, error) {
	s.failCalls = append(s.failCalls, input)
	if s.failErr != nil {
		return nil, s.failErr
	}
	if s.failReply != nil {
		return s.failReply, nil
	}
	return &taskdag.FailNodeResult{
		OldStatus: s.fromStatus,
		Node:      &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, RunID: &input.RunID, Status: "failed"},
	}, nil
}

func (s *stubNodeFlowStore) AcquireWorkerLease(context.Context, taskdag.AcquireWorkerLeaseInput) (int64, error) {
	return 0, errors.New("not used in this test")
}

func (s *stubNodeFlowStore) RenewWorkerLease(_ context.Context, input taskdag.RenewWorkerLeaseInput) (int64, error) {
	s.renewCalls = append(s.renewCalls, input)
	if s.renewErr != nil {
		return 0, s.renewErr
	}
	if s.renewRowsSet {
		return s.renewRows, nil
	}
	return 1, nil
}

func (s *stubNodeFlowStore) ReleaseWorkerLease(context.Context, taskdag.ReleaseWorkerLeaseInput) error {
	return errors.New("not used in this test")
}

func makeServiceWithStub(stub taskdag.OrchestrationStore) *service {
	return newDAGTestService(dagControllerParams{DAGStore: stub})
}

func taskUpdateNodeTestContext() context.Context {
	return mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "agent-A"})
}

// TestUpdateNodeStatusDone_RoutesToCompleteNodeAndScheduleDownstream 验证 done 状态走下游调度完成路径。
// status="done" 不应走普通 UpdateNodeStatus。
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
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
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

func TestUpdateNodeStatusDonePublishesTaskNodeStatusChanged(t *testing.T) {
	dispatcher := event.NewDispatcher()
	events := make(chan taskdto.TaskNodeStatusChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev taskdto.TaskNodeStatusChanged) {
		events <- ev
	})
	defer cancel()

	runID := int64(42)
	stub := &stubNodeFlowStore{
		completeReply: &taskdag.CompleteNodeWithDownstreamResult{
			Node: &taskdag.Node{DagKey: "dag-1", NodeKey: "A", RunID: &runID, Status: "done"},
		},
	}
	s := newDAGTestService(dagControllerParams{DAGStore: stub, EventBus: dispatcher})
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   runID,
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("UpdateNodeStatus done err = %v", err)
	}

	select {
	case got := <-events:
		if got.DagKey != "dag-1" || got.NodeKey != "A" || got.RunID != runID {
			t.Fatalf("event identity = %s/%s/%d, want dag-1/A/%d", got.DagKey, got.NodeKey, got.RunID, runID)
		}
		if got.OldStatus != "running" || got.NewStatus != "done" {
			t.Fatalf("event status = %q -> %q, want running -> done", got.OldStatus, got.NewStatus)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TaskNodeStatusChanged")
	}
}

// TestUpdateNodeStatusNonDone_UsesCASUpdate 验证非 done 状态保留普通 UpdateNodeStatus 路径。
// 该路径不应触发 CompleteNodeAndScheduleDownstream。
func TestUpdateNodeStatusNonDone_UsesCASUpdate(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "ready"} // ready → running 合法
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
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
		t.Fatalf("updateCalls = %d, want 1 (ordinary CAS path)", len(stub.updateCalls))
	}
	if len(stub.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 (no spawn for non-done)", len(stub.completeCalls))
	}
	if got := stub.updateCalls[0]; got.RunID != 43 {
		t.Fatalf("UpdateNodeStatus RunID = %d, want 43", got.RunID)
	}
	if got := stub.updateCalls[0]; got.ExpectedStatus != "ready" {
		t.Fatalf("UpdateNodeStatus ExpectedStatus = %q, want ready", got.ExpectedStatus)
	}
}

func TestUpdateNodeStatusFailed_RoutesLegalSourcesToFailCascade(t *testing.T) {
	for _, fromStatus := range []string{"running", "retrying"} {
		t.Run(fromStatus, func(t *testing.T) {
			stub := &stubNodeFlowStore{fromStatus: fromStatus}
			s := makeServiceWithStub(stub)
			_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
				DagKey:  "dag-1",
				NodeKey: "A",
				RunID:   47,
				Status:  "failed",
				Result:  json.RawMessage(`{"error":"operator failed"}`),
			})
			if err != nil {
				t.Fatalf("UpdateNodeStatus failed from %s err = %v", fromStatus, err)
			}
			requireFailedCascadeCall(t, stub, 47)
		})
	}
}

func requireFailedCascadeCall(t *testing.T, stub *stubNodeFlowStore, runID int64) {
	t.Helper()
	if len(stub.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(stub.failCalls))
	}
	if len(stub.updateCalls) != 0 || len(stub.completeCalls) != 0 {
		t.Fatalf("unexpected legacy calls: update=%d complete=%d", len(stub.updateCalls), len(stub.completeCalls))
	}
	got := stub.failCalls[0]
	if got.DagKey != "dag-1" || got.NodeKey != "A" || got.RunID != runID {
		t.Fatalf("FailNodeInput identity = %+v, want dag-1/A run_id=%d", got, runID)
	}
	if !got.FailFast {
		t.Fatalf("FailNodeInput.FailFast = false, want true for task_update_node failed cascade")
	}
	if !strings.Contains(got.Reason, "operator failed") {
		t.Fatalf("FailNodeInput.Reason = %q, want request result", got.Reason)
	}
}

func TestUpdateNodeStatusFailed_RejectsPendingBeforeCascade(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "pending"}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   48,
		Status:  "failed",
	})
	if err == nil {
		t.Fatal("UpdateNodeStatus pending→failed err = nil, want transition rejection")
	}
	if len(stub.failCalls) != 0 || len(stub.updateCalls) != 0 || len(stub.completeCalls) != 0 {
		t.Fatalf("store should not be called when pending→failed is rejected: fail=%d update=%d complete=%d",
			len(stub.failCalls), len(stub.updateCalls), len(stub.completeCalls))
	}
}

func TestTaskUpdateNodeRejectsNonLeaseHolder(t *testing.T) {
	stub := &stubNodeFlowStore{
		fromStatus:   "ready",
		assignedTo:   "agent-A",
		renewRowsSet: true,
		renewRows:    0,
	}
	s := makeServiceWithStub(stub)
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "agent-B"})
	_, err := s.UpdateNodeStatus(ctx, UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   49,
		Status:  "running",
	})
	if err == nil || !strings.Contains(err.Error(), "lease") {
		t.Fatalf("UpdateNodeStatus err = %v, want lease rejection", err)
	}
	if len(stub.updateCalls) != 0 || len(stub.completeCalls) != 0 || len(stub.failCalls) != 0 {
		t.Fatalf("store update should not run after lease rejection: update=%d complete=%d fail=%d",
			len(stub.updateCalls), len(stub.completeCalls), len(stub.failCalls))
	}
	if len(stub.renewCalls) != 1 {
		t.Fatalf("RenewWorkerLease calls = %d, want 1", len(stub.renewCalls))
	}
	got := stub.renewCalls[0]
	if got.TargetAgentID != "agent-A" || got.OwnerID != "agent-B" {
		t.Fatalf("RenewWorkerLease input = %+v, want target agent-A owner agent-B", got)
	}
}

func TestUpdateNodeStatusRequiresRunID(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "ready"}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
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

// TestUpdateNodeStatus_RejectsIllegalTransition 验证非法转移会在写 store 前被拒绝。
// pending → done 属于跳态，不应触达下游 store。
func TestUpdateNodeStatus_RejectsIllegalTransition(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "pending"} // pending → done 非法
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
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

// TestUpdateNodeStatus_RejectsTerminalSourceTransition 验证终态节点不能重新进入 ready。
// done → ready 是终态出态，必须被拒。
func TestUpdateNodeStatus_RejectsTerminalSourceTransition(t *testing.T) {
	stub := &stubNodeFlowStore{fromStatus: "done"}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   45,
		Status:  "ready",
	})
	if err == nil {
		t.Fatalf("expected terminal-source rejection for done→ready, got nil")
	}
}

type nonFlowStub struct {
	taskdag.OrchestrationStore // nil
	updateCalls                []taskdag.NodeStatusUpdate
	renewCalls                 []taskdag.RenewWorkerLeaseInput
}

func (s *nonFlowStub) ListNodes(_ context.Context, dagKey string) ([]taskdag.Node, error) {
	return []taskdag.Node{{DagKey: dagKey, NodeKey: "A", Status: "running", AssignedTo: "agent-A"}}, nil
}

func (s *nonFlowStub) ListRunNodes(_ context.Context, dagKey string, runID int64) ([]taskdag.Node, error) {
	return []taskdag.Node{{DagKey: dagKey, NodeKey: "A", RunID: &runID, Status: "running", AssignedTo: "agent-A"}}, nil
}

func (s *nonFlowStub) UpdateNodeStatus(_ context.Context, input taskdag.NodeStatusUpdate) (*taskdag.Node, error) {
	s.updateCalls = append(s.updateCalls, input)
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: input.Status}, nil
}

func (s *nonFlowStub) AcquireWorkerLease(context.Context, taskdag.AcquireWorkerLeaseInput) (int64, error) {
	return 0, errors.New("not used in this test")
}

func (s *nonFlowStub) RenewWorkerLease(_ context.Context, input taskdag.RenewWorkerLeaseInput) (int64, error) {
	s.renewCalls = append(s.renewCalls, input)
	return 1, nil
}

func (s *nonFlowStub) ReleaseWorkerLease(context.Context, taskdag.ReleaseWorkerLeaseInput) error {
	return errors.New("not used in this test")
}

func TestUpdateNodeStatusDone_FallsBackWhenStoreLacksNodeFlowStore(t *testing.T) {
	stub := &nonFlowStub{}
	s := makeServiceWithStub(stub)
	_, err := s.UpdateNodeStatus(taskUpdateNodeTestContext(), UpdateNodeStatusRequest{
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
