package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// stubDispatchStore 是 task_dispatch_node 单测的最小 DispatchNodeStore 桩。
// 不嵌入 taskdag.OrchestrationStore — 接口面已经把所需方法穷尽（4 个）。
type stubDispatchStore struct {
	nodes        []taskdag.Node
	listErr      error
	upsertErr    error
	upserted     *taskdag.Node
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

func (s *stubDispatchStore) UpsertNode(_ context.Context, node taskdag.Node) (*taskdag.Node, error) {
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	clone := node
	s.upserted = &clone
	return &clone, nil
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

func newServiceForDispatch(store taskdag.DispatchNodeStore) *service {
	return &service{dispatchStore: store}
}

// TestDispatchNode_HappyPath_ReadyNode_AssignsAndEnqueues：节点 ready/无 assignee
// 时应被 upsert assigned_to + enqueue manual_dispatch wakeup。
func TestDispatchNode_HappyPath_ReadyNode_AssignsAndEnqueues(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Title: "n1", Status: "ready"}},
	}
	svc := newServiceForDispatch(stub)
	resp, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		AssignedTo: "agent-alpha",
	})
	if err != nil {
		t.Fatalf("DispatchNode err = %v", err)
	}
	if !resp.Enqueued || resp.WakeupID != 42 {
		t.Fatalf("resp = %+v, want Enqueued=true WakeupID=42", resp)
	}
	if stub.upserted == nil || stub.upserted.AssignedTo != "agent-alpha" {
		t.Fatalf("upserted = %+v, want AssignedTo=agent-alpha", stub.upserted)
	}
	if len(stub.enqueued) != 1 {
		t.Fatalf("enqueued len = %d, want 1", len(stub.enqueued))
	}
	got := stub.enqueued[0]
	if got.WakeupKind != "manual_dispatch" || got.TargetAgentID != "agent-alpha" {
		t.Fatalf("enqueue input = %+v", got)
	}
	if !strings.HasPrefix(got.IdempotencyKey, "manual_dispatch:dag-1:n1:agent-alpha") {
		t.Fatalf("idempotency key = %q", got.IdempotencyKey)
	}
}

// TestDispatchNode_PendingAccepted: pending 节点同样允许 dispatch（F6.4 跳 enqueue
// 后节点仍在 pending、依赖满足；本工具是唯一推进路径）。
func TestDispatchNode_PendingAccepted(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "pending"}},
	}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		AssignedTo: "agent-a",
	})
	if err != nil {
		t.Fatalf("DispatchNode pending err = %v", err)
	}
}

// TestDispatchNode_RejectsRunning：running 节点不允许 dispatch — 防止误覆盖
// active runtime。
func TestDispatchNode_RejectsRunning(t *testing.T) {
	t.Parallel()
	stub := &stubDispatchStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "running"}},
	}
	svc := newServiceForDispatch(stub)
	_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     "dag-1",
		NodeKey:    "n1",
		AssignedTo: "agent-a",
	})
	if err == nil || !errors.Is(err, ErrDispatchNodeIneligible) {
		t.Fatalf("DispatchNode(running) err = %v, want ErrDispatchNodeIneligible", err)
	}
	if stub.upserted != nil {
		t.Fatalf("UpsertNode called on rejected dispatch: %+v", stub.upserted)
	}
}

// TestDispatchNode_RejectsTerminalDone：done/failed/cancelled 都不让 dispatch。
func TestDispatchNode_RejectsTerminalDone(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"done", "failed", "cancelled", "skipped"} {
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			stub := &stubDispatchStore{
				nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: status}},
			}
			svc := newServiceForDispatch(stub)
			_, err := svc.DispatchNode(context.Background(), contract.DispatchNodeRequest{
				DagKey:     "dag-1",
				NodeKey:    "n1",
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
		DagKey: "dag-1", NodeKey: "n1", AssignedTo: "agent-a",
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
		{DagKey: "", NodeKey: "n", AssignedTo: "a"},
		{DagKey: "d", NodeKey: "  ", AssignedTo: "a"},
		{DagKey: "d", NodeKey: "n", AssignedTo: " "},
	}
	for i, req := range cases {
		_, err := svc.DispatchNode(context.Background(), req)
		if err == nil {
			t.Errorf("case %d: expected error for blank input %+v", i, req)
		}
	}
}
