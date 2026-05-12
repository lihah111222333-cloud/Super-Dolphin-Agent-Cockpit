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
)

// stubRouterStore 实现 taskdag.Store 所需的最小方法。其它方法走 nil 嵌入会
// panic — 路由器测试不会调到它们。
type stubRouterStore struct {
	taskdag.Store
	nodes   []taskdag.Node
	listErr error
}

func (s *stubRouterStore) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]taskdag.Node, len(s.nodes))
	copy(out, s.nodes)
	return out, nil
}

// stubAgentLauncher 是 nodeexec.AgentLauncher 的最小实现 — 不真起 process，
// 只回 threadID + 可选 error。
type stubAgentLauncher struct {
	threadID string
	err      error
	calls    []contract.LaunchRequest
}

func (l *stubAgentLauncher) LaunchAgent(_ context.Context, req contract.LaunchRequest) (string, error) {
	l.calls = append(l.calls, req)
	return l.threadID, l.err
}

// TestNodeExecutorRouter_RoutesAgentNode：node_type=agent 的 wakeup 应通过
// AgentExecutor.Execute → 注入的 launcher。
func TestNodeExecutorRouter_RoutesAgentNode(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-x"}
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "agent",
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:   "ready",
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID:      1,
		DagKey:  "dag-1",
		NodeKey: "n1",
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1", len(launcher.calls))
	}
	if launcher.calls[0].AgentKey != "alpha" {
		t.Fatalf("launcher.AgentKey = %q, want alpha", launcher.calls[0].AgentKey)
	}
}

// TestNodeExecutorRouter_EmptyNodeTypeDefaultsToAgent：F1.0 dogfood DAG 兼容 —
// node_type 为空时兜底当 agent，避免 dogfood DAG 节点全被判 validation 失败。
func TestNodeExecutorRouter_EmptyNodeTypeDefaultsToAgent(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-y"}
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "", // 空
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"a"},"first_turn":"x"}`),
			Status:   "ready",
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1",
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done (default-agent fallback)", outcome.Status)
	}
}

// TestNodeExecutorRouter_HybridReturnsValidationFailure：F3.1 未落地，hybrid
// 暂留为 validation 类失败，由 dispatcher 走 permanent fail（ADR-008 对齐）。
func TestNodeExecutorRouter_HybridReturnsValidationFailure(t *testing.T) {
	t.Parallel()
	store := &stubRouterStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", NodeType: "hybrid"}},
	}
	router := NewNodeExecutorRouter(store, nil, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1",
	})
	if err != nil {
		t.Fatalf("RouteByWakeup hybrid err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusFailed || outcome.FailureClass != nodeexec.FailureClassValidation {
		t.Fatalf("outcome = %+v, want failed+validation", outcome)
	}
	if !strings.Contains(outcome.ErrorSummary, "hybrid") {
		t.Fatalf("summary missing hybrid hint: %q", outcome.ErrorSummary)
	}
}

// TestNodeExecutorRouter_NodeNotFoundIsFrameworkError：节点不在 DAG 中应
// 返 error（framework fault）让 dispatcher 走 retry — 可能是临时 DB 同步漂移。
func TestNodeExecutorRouter_NodeNotFoundIsFrameworkError(t *testing.T) {
	t.Parallel()
	store := &stubRouterStore{nodes: []taskdag.Node{}}
	router := NewNodeExecutorRouter(store, nil, nil, nil, nil, nil)
	_, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "ghost",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not-found framework error", err)
	}
}

// TestNodeExecutorRouter_StoreListErrorPropagates：store ListNodes 失败应作为
// framework error 透出，给 dispatcher transient retry 机会。
func TestNodeExecutorRouter_StoreListErrorPropagates(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("db reset")
	store := &stubRouterStore{listErr: sentinel}
	router := NewNodeExecutorRouter(store, nil, nil, nil, nil, nil)
	_, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1",
	})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapping sentinel", err)
	}
}

// TestNodeExecutorRouter_MissingDagInfoIsFrameworkError：dag_key/node_key 任一
// 空都该 framework-fail；正常生产 enqueue 不会触发，但防御性测试守住该约定。
func TestNodeExecutorRouter_MissingDagInfoIsFrameworkError(t *testing.T) {
	t.Parallel()
	router := NewNodeExecutorRouter(&stubRouterStore{}, nil, nil, nil, nil, nil)
	_, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "", NodeKey: "n1",
	})
	if err == nil {
		t.Fatalf("expected error for missing dag_key")
	}
}

// TestServiceAgentLauncher_AdapterReturnsThreadIDOnSuccess: serviceAgentLauncher
// 适配器把 service.LaunchAgentSnapshot 的 (AgentSnapshot, error) 转 (threadID, error)。
// 不易直接构造 *service 因依赖众多 — 但 NewServiceAgentLauncher(nil) 应安全。
func TestServiceAgentLauncher_NilReceiverSafe(t *testing.T) {
	t.Parallel()
	adapter := NewServiceAgentLauncher(nil)
	if adapter == nil {
		t.Fatalf("NewServiceAgentLauncher(nil) returned nil adapter")
	}
	_, err := adapter.LaunchAgent(context.Background(), contract.LaunchRequest{})
	if err == nil {
		t.Fatalf("expected error when service nil, got nil")
	}
}

// TestStoreNodeSpawnRecorder_NilStoreReturnsNilAdapter：nil store 输入应
// 给出 nil adapter，让 AgentExecutor 跳过写回（F1.5 silent skip 语义）。
func TestStoreNodeSpawnRecorder_NilStoreReturnsNilAdapter(t *testing.T) {
	t.Parallel()
	if got := NewStoreNodeSpawnRecorder(nil); got != nil {
		t.Fatalf("NewStoreNodeSpawnRecorder(nil) = %T, want nil", got)
	}
}
