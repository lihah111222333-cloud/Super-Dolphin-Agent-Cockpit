package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// stubDispatchStore 是 task_dispatch_node 单测的最小 DispatchNodeStore 桩。
// 不嵌入 taskdag.OrchestrationStore — 接口面已经把所需方法穷尽。
type stubDispatchStore struct {
	nodes        []taskdag.Node
	listErr      error
	assignErr    error
	assigned     *taskdag.Node
	enqueued     []taskdag.EnqueueWakeupInput
	enqueueID    int64
	enqueueErr   error
	getDAGErr    error
	getDAGReturn *taskdag.DAG
}

func (s *stubDispatchStore) GetDAG(_ context.Context, dagKey string) (*taskdag.DAG, error) {
	if s.getDAGErr != nil {
		return nil, s.getDAGErr
	}
	if s.getDAGReturn != nil {
		return s.getDAGReturn, nil
	}
	return &taskdag.DAG{DagKey: dagKey}, nil
}

func (s *stubDispatchStore) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]taskdag.Node, len(s.nodes))
	copy(out, s.nodes)
	return out, nil
}

func (s *stubDispatchStore) ListRunNodes(_ context.Context, _ string, runID int64) ([]taskdag.Node, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]taskdag.Node, 0, len(s.nodes))
	for i := range s.nodes {
		if s.nodes[i].RunID != nil && *s.nodes[i].RunID == runID {
			out = append(out, s.nodes[i])
		}
	}
	return out, nil
}

func (s *stubDispatchStore) AssignNode(_ context.Context, input taskdag.AssignNodeInput) (*taskdag.Node, error) {
	if s.assignErr != nil {
		return nil, s.assignErr
	}
	clone := taskdag.Node{
		DagKey:     input.DagKey,
		NodeKey:    input.NodeKey,
		RunID:      &input.RunID,
		Status:     "ready",
		AssignedTo: input.AssignedTo,
	}
	for i := range s.nodes {
		if s.nodes[i].NodeKey == input.NodeKey && s.nodes[i].RunID != nil && *s.nodes[i].RunID == input.RunID {
			clone = s.nodes[i]
			clone.AssignedTo = input.AssignedTo
			break
		}
	}
	s.assigned = &clone
	return &clone, nil
}

func (s *stubDispatchStore) AssignNodeAndEnqueueWakeup(_ context.Context, input taskdag.AssignNodeAndEnqueueWakeupInput) (*taskdag.AssignNodeAndEnqueueWakeupResult, error) {
	if s.assignErr != nil {
		return nil, s.assignErr
	}
	if s.enqueueErr != nil {
		return nil, s.enqueueErr
	}
	clone := taskdag.Node{
		DagKey:     input.Assign.DagKey,
		NodeKey:    input.Assign.NodeKey,
		RunID:      &input.Assign.RunID,
		Status:     "ready",
		AssignedTo: input.Assign.AssignedTo,
	}
	for i := range s.nodes {
		if s.nodes[i].NodeKey == input.Assign.NodeKey && s.nodes[i].RunID != nil && *s.nodes[i].RunID == input.Assign.RunID {
			clone = s.nodes[i]
			clone.AssignedTo = input.Assign.AssignedTo
			break
		}
	}
	s.assigned = &clone
	s.enqueued = append(s.enqueued, input.Wakeup)
	wakeupID := s.enqueueID
	if wakeupID == 0 {
		wakeupID = 42
	}
	return &taskdag.AssignNodeAndEnqueueWakeupResult{Node: &clone, WakeupID: wakeupID}, nil
}

func (s *stubDispatchStore) EnqueueWakeup(_ context.Context, input taskdag.EnqueueWakeupInput) (int64, error) {
	if s.enqueueErr != nil {
		return 0, s.enqueueErr
	}
	s.enqueued = append(s.enqueued, input)
	if s.enqueueID == 0 {
		return 42, nil
	}
	return s.enqueueID, nil
}

func (s *stubDispatchStore) MarkDispatchIncompleteIfMissingWakeup(_ context.Context, input taskdag.MarkDispatchIncompleteInput) (*taskdag.MarkDispatchIncompleteResult, error) {
	for i := range s.nodes {
		node := s.nodes[i]
		if node.NodeKey != input.NodeKey || node.RunID == nil || *node.RunID != input.RunID {
			continue
		}
		if strings.TrimSpace(node.AssignedTo) != "" && len(s.enqueued) == 0 {
			node.Status = "dispatch_incomplete"
			s.nodes[i] = node
			return &taskdag.MarkDispatchIncompleteResult{Marked: true, Node: &node}, nil
		}
		return &taskdag.MarkDispatchIncompleteResult{Node: &node, ActiveWakeup: len(s.enqueued) > 0}, nil
	}
	return &taskdag.MarkDispatchIncompleteResult{}, nil
}

func newServiceForDispatch(store taskdag.DispatchNodeStore) *service {
	return newDAGTestService(dagControllerParams{DispatchStore: store})
}

func dispatchTestRunID(id int64) *int64 {
	return &id
}

// TestDispatchNode_HappyPath_ReadyNode_AssignsAndEnqueues：节点 ready/无 assignee
// 时应被 upsert assigned_to + enqueue manual_dispatch wakeup。
func TestDispatchNode_HappyPath_ReadyNode_AssignsAndEnqueues(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", RunID: dispatchTestRunID(7), Title: "n1", NodeType: "agent", Status: "ready", Config: testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"}}`)}},
	}
	svc := newServiceForDispatch(stub)
	resp, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		RunID:      7,
		AssignedTo: "agent-alpha",
	})
	if err != nil {
		t.Fatalf("DispatchNode err = %v", err)
	}
	assertDispatchReadyNode(t, stub, resp)
}

func assertDispatchReadyNode(t *testing.T, stub *stubDispatchStore, resp contract.DispatchNodeResponse) {
	t.Helper()
	assertDispatchResponse(t, resp)
	assertDispatchAssignment(t, stub.assigned)
	assertDispatchWakeup(t, stub.enqueued)
}

func assertDispatchResponse(t *testing.T, resp contract.DispatchNodeResponse) {
	t.Helper()
	if !resp.Enqueued || resp.WakeupID != 42 {
		t.Fatalf("resp = %+v, want Enqueued=true WakeupID=42", resp)
	}
}

func assertDispatchAssignment(t *testing.T, assigned *taskdag.Node) {
	t.Helper()
	if assigned == nil || assigned.AssignedTo != "agent-alpha" || assigned.RunID == nil || *assigned.RunID != 7 {
		t.Fatalf("assigned = %+v, want run_id=7 AssignedTo=agent-alpha", assigned)
	}
}

func assertDispatchWakeup(t *testing.T, enqueued []taskdag.EnqueueWakeupInput) {
	t.Helper()
	if len(enqueued) != 1 {
		t.Fatalf("enqueued len = %d, want 1", len(enqueued))
	}
	got := enqueued[0]
	if got.WakeupKind != "manual_dispatch" || got.TargetAgentID != "agent-alpha" {
		t.Fatalf("enqueue input = %+v", got)
	}
	if got.RunID != 7 {
		t.Fatalf("wakeup run_id = %d, want 7", got.RunID)
	}
	if !strings.HasPrefix(got.IdempotencyKey, "manual_dispatch:dag-1:7:n1:agent-alpha") {
		t.Fatalf("idempotency key = %q", got.IdempotencyKey)
	}
}

// TestDispatchNode_PendingAccepted 验证依赖已满足但尚未自动入队的 pending 节点仍可手动派发。
// 这个入口是人工补派的推进路径，不能被 ready-only 判断拦住。
func TestDispatchNode_PendingAccepted(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", RunID: dispatchTestRunID(7), NodeType: "agent", Status: "pending", Config: testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"}}`)}},
	}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		RunID:      7,
		AssignedTo: "agent-a",
	})
	if err != nil {
		t.Fatalf("DispatchNode pending err = %v", err)
	}
}

func TestDispatchNode_RejectsAgentNodeMissingCwdBeforeAssignAndEnqueue(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    dispatchTestRunID(7),
			NodeType: "agent",
			Status:   "ready",
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha"}}`),
		}},
	}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		RunID:      7,
		AssignedTo: "agent-a",
	})
	if !errors.Is(err, contract.ErrLaunchCWDRequired) {
		t.Fatalf("DispatchNode missing cwd err = %v, want ErrLaunchCWDRequired", err)
	}
	if !strings.Contains(err.Error(), "exec.cwd") {
		t.Fatalf("DispatchNode missing cwd err = %v, want exec.cwd guidance", err)
	}
	if stub.assigned != nil {
		t.Fatalf("AssignNode called on missing cwd: %+v", stub.assigned)
	}
	if len(stub.enqueued) != 0 {
		t.Fatalf("EnqueueWakeup called on missing cwd: %+v", stub.enqueued)
	}
}

func TestDispatchNodeRejectsRelativeCwdBeforeAssignAndEnqueue(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{nodes: []taskdag.Node{{
		DagKey: "dag-1", NodeKey: "n1", RunID: dispatchTestRunID(7), NodeType: "agent", Status: "ready",
		Config: testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"relative/path"}}`),
	}}}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{DagKey: "dag-1", NodeKey: "n1", RunID: 7, AssignedTo: "agent-a"})
	if !errors.Is(err, contract.ErrLaunchCWDInvalid) {
		t.Fatalf("DispatchNode relative cwd error = %v, want ErrLaunchCWDInvalid", err)
	}
	if stub.assigned != nil || len(stub.enqueued) != 0 {
		t.Fatalf("relative cwd reached assignment or queue: assigned=%+v enqueued=%+v", stub.assigned, stub.enqueued)
	}
}

func TestDispatchNodeDoesNotPersistAssignmentWhenWakeupEnqueueFails(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes:      []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", RunID: dispatchTestRunID(7), NodeType: "agent", Status: "ready", Config: testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"}}`)}},
		enqueueErr: errors.New("boom"),
	}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		RunID:      7,
		AssignedTo: "agent-alpha",
	})
	if err == nil {
		t.Fatal("DispatchNode() error = nil, want enqueue failure")
	}
	if stub.assigned != nil {
		t.Fatalf("DispatchNode() persisted assignment after enqueue failure: %+v", stub.assigned)
	}
}

// TestDispatchNode_RejectsRunning：running 节点不允许 dispatch — 防止误覆盖
// active runtime。
func TestDispatchNode_RejectsRunning(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", RunID: dispatchTestRunID(7), Status: "running"}},
	}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		RunID:      7,
		AssignedTo: "agent-a",
	})
	if err == nil || !errors.Is(err, ErrDispatchNodeIneligible) {
		t.Fatalf("DispatchNode(running) err = %v, want ErrDispatchNodeIneligible", err)
	}
	if stub.assigned != nil {
		t.Fatalf("AssignNode called on rejected dispatch: %+v", stub.assigned)
	}
}

// TestDispatchNode_RejectsTerminalDone：done/failed/cancelled 都不让 dispatch。
func TestDispatchNode_RejectsTerminalDone(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"done", "failed", "cancelled", "skipped"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			stub := &stubDispatchStore{
				nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", RunID: dispatchTestRunID(7), Status: status}},
			}
			svc := newServiceForDispatch(stub)
			_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
				DagKey:     "dag-1",
				NodeKey:    "n1",
				RunID:      7,
				AssignedTo: "agent-a",
			})
			if err == nil || !errors.Is(err, ErrDispatchNodeIneligible) {
				t.Fatalf("status=%s err = %v, want ineligible", status, err)
			}
		})
	}
}

// TestDispatchNode_NodeNotFound: 不存在的 node_key 报清晰错误（防 typo）。
func TestDispatchNode_NodeNotFound(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{nodes: []taskdag.Node{}}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "unknown",
		RunID:      7,
		AssignedTo: "agent-a",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("DispatchNode unknown node err = %v, want \"not found\"", err)
	}
}

// TestDispatchNode_StoreUnset：service 没拿到 dispatchStore 时应返
// ErrDispatchStoreUnset，让上层（HandleDispatchNode）能转双语错误。
func TestDispatchNode_StoreUnset(t *testing.T) {
	t.Parallel()
	svc := &service{}
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey: "dag-1", NodeKey: "n1", RunID: 7, AssignedTo: "agent-a",
	})
	if !errors.Is(err, ErrDispatchStoreUnset) {
		t.Fatalf("err = %v, want ErrDispatchStoreUnset", err)
	}
}

// TestDispatchNode_RejectsBlankFields: trim 后空串拒绝。
func TestDispatchNode_RejectsBlankFields(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{}
	svc := newServiceForDispatch(stub)
	cases := []contract.DispatchNodeRequest{
		{DagKey: "", NodeKey: "n", RunID: 7, AssignedTo: "a"},
		{DagKey: "d", NodeKey: "  ", RunID: 7, AssignedTo: "a"},
		{DagKey: "d", NodeKey: "n", RunID: 7, AssignedTo: " "},
		{DagKey: "d", NodeKey: "n", RunID: 0, AssignedTo: "a"},
	}
	for i, req := range cases {
		_, err := svc.DispatchNode(context.Background(), req)
		if err == nil {
			t.Errorf("case %d: expected error for blank input %+v", i, req)
		}
	}
}
