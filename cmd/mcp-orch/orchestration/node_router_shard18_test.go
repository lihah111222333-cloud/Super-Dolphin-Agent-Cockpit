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

// =====================================================
// dispatcher-wiring closure：RunContext 三端口预填的单测覆盖。
// =====================================================

// stubRouterPrevReader 是 SharedFileReader 的最小测试假实现；记录调用次数。
type stubRouterPrevReader struct {
	contents map[string]string
	calls    int
}

func (s *stubRouterPrevReader) ReadSharedFile(_ context.Context, path string) (string, bool, error) {
	s.calls++
	c, ok := s.contents[path]
	return c, ok, nil
}

// stubRouterPrevWriter 是 SharedFileWriter 的最小测试假实现；记录写入。
type stubRouterPrevWriter struct {
	writes []struct {
		path    string
		content string
	}
	err error
}

func (s *stubRouterPrevWriter) WriteSharedFile(_ context.Context, path, content string) error {
	if s.err != nil {
		return s.err
	}
	s.writes = append(s.writes, struct {
		path    string
		content string
	}{path: path, content: content})
	return nil
}

// recordingAgentLauncher 是 launcher 测试假实现 — 暴露最后一次 LaunchRequest
// 内含的 Prompt，让我们能断言 inputs prefix 真被注入。
type recordingAgentLauncher struct {
	threadID string
	calls    []contract.LaunchRequest
}

func (l *recordingAgentLauncher) LaunchAgent(_ context.Context, req contract.LaunchRequest) (string, error) {
	l.calls = append(l.calls, req)
	return l.threadID, nil
}

// TestNodeExecutorRouter_PrefetchPrevResults_FromNodes_NonEmpty:
// cfg.Inputs.FromNodes 非空 → prefetchPrevResults 真填上游 done 节点 result，
// AgentExecutor 通过 RunContext.PrevResults 把内容注入到 LaunchRequest.Prompt。
func TestNodeExecutorRouter_PrefetchPrevResults_FromNodes_NonEmpty(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-1"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: json.RawMessage(`{"summary":"upstream done"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7), NodeType: "agent",
				Title: "downstream", Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7),
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
	prompt := launcher.calls[0].Prompt
	if !strings.Contains(prompt, "[inputs.from_nodes]") {
		t.Fatalf("prompt missing inputs.from_nodes marker: %q", prompt)
	}
	if !strings.Contains(prompt, "upstream done") {
		t.Fatalf("prompt missing upstream result content: %q", prompt)
	}
}

func TestNodeExecutorRouter_PrefetchPrevResults_StaysInsideRun(t *testing.T) {
	t.Parallel()
	runA := int64(1001)
	runB := int64(1002)
	launcher := &recordingAgentLauncher{threadID: "thr-run-b"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: &runA, NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: json.RawMessage(`{"summary":"wrong run"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: &runA, NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go A"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: &runB, NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: json.RawMessage(`{"summary":"right run"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: &runB, NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go B"}`),
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "downstream", RunID: &runB,
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
	prompt := launcher.calls[0].Prompt
	if !strings.Contains(prompt, "right run") {
		t.Fatalf("prompt missing run B upstream result: %q", prompt)
	}
	if strings.Contains(prompt, "wrong run") {
		t.Fatalf("prompt leaked run A upstream result: %q", prompt)
	}
}

// TestNodeExecutorRouter_PrefetchPrevResults_FiltersNonDoneUpstream:
// 上游节点 status != done → 不入 PrevResults map；nodeexec.loadFromNodes 因此
// 报 validation "references unknown node_key"，保 fail-loud。
func TestNodeExecutorRouter_PrefetchPrevResults_FiltersNonDoneUpstream(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-2"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusRunning), // 未 done
				Result: json.RawMessage(`{"x":1}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v, want node-level validation outcome", err)
	}
	if outcome.Status != nodeexec.NodeStatusFailed || outcome.FailureClass != nodeexec.FailureClassValidation {
		t.Fatalf("outcome = %+v, want failed+validation", outcome)
	}
	if !strings.Contains(outcome.ErrorSummary, "upstream") {
		t.Fatalf("outcome.ErrorSummary = %q, want upstream validation detail", outcome.ErrorSummary)
	}
	if len(launcher.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 after validation failure", len(launcher.calls))
	}
}

// TestNodeExecutorRouter_PrefetchPrevResults_EmptyResultPlaceholder:
// 上游 done 但 Result NULL → nodeexec.loadFromNodes 走 "(empty)" 占位分支；
// 不抛 validation。
func TestNodeExecutorRouter_PrefetchPrevResults_EmptyResultPlaceholder(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-3"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: nil, // NULL
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done (empty placeholder)", outcome.Status)
	}
	if len(launcher.calls) != 1 || !strings.Contains(launcher.calls[0].Prompt, "(empty)") {
		t.Fatalf("prompt should contain (empty) placeholder: %q", launcher.calls[0].Prompt)
	}
}

// TestNodeExecutorRouter_SharedFileReader_Injected:
// cfg.Inputs.FromSharedfiles 非空 + SharedFileReader 注入 → reader 被调用，
// 内容拼进 LaunchRequest.Prompt。
func TestNodeExecutorRouter_SharedFileReader_Injected(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-4"}
	agentExec := newTestAgentExecutor(launcher, nil)
	reader := &stubRouterPrevReader{contents: map[string]string{"plan.md": "plan content"}}
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7), NodeType: "agent",
			Status: string(nodeexec.NodeStatusReady),
			Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_sharedfiles":["plan.md"]},"first_turn":"go"}`),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, reader, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.calls)
	}
	if !strings.Contains(launcher.calls[0].Prompt, "plan content") {
		t.Fatalf("prompt missing sharedfile content: %q", launcher.calls[0].Prompt)
	}
}

// TestNodeExecutorRouter_SharedFileWriter_Injected:
// node_type=automation + outputs.to_sharedfile 配置 + SharedFileWriter 注入 →
// AutomationExecutor 通过 RunContext.SharedFileWriter 写入。
func TestNodeExecutorRouter_SharedFileWriter_Injected(t *testing.T) {
	t.Parallel()
	autoExec := nodeexec.NewAutomationExecutor(
		stubAutomationCmdGetter{},
		stubAutomationCmdRunner{stdout: "build ok"},
	)
	writer := &stubRouterPrevWriter{}
	store := &stubRouterAutoStore{
		stubRouterStore: stubRouterStore{
			nodes: []taskdag.Node{{
				DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7), NodeType: "automation",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"command_ref":"build"},"outputs":{"to_sharedfile":{"path":"reports/out.log","lock_mode":"exclusive"}}}`),
			}},
		},
	}
	router := NewNodeExecutorRouter(store, nil, autoExec, nil, writer, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v err summary %q", outcome.Status, outcome.ErrorSummary)
	}
	if len(writer.writes) != 1 {
		t.Fatalf("writer writes = %d, want 1", len(writer.writes))
	}
	if writer.writes[0].path != "reports/out.log" {
		t.Fatalf("write path = %q, want reports/out.log", writer.writes[0].path)
	}
	if writer.writes[0].content != "build ok" {
		t.Fatalf("write content = %q, want build ok", writer.writes[0].content)
	}
}

func TestNodeExecutorRouter_AutomationLifecycleHooks(t *testing.T) {
	events := []string{}
	autoExec := nodeexec.NewAutomationExecutor(
		stubAutomationCmdGetter{},
		stubAutomationCmdRunner{stdout: "build ok"},
		nodeexec.WithAutomationHooks(recordingLifecycleHooks(&events)),
	)
	store := &stubRouterAutoStore{
		stubRouterStore: stubRouterStore{
			nodes: []taskdag.Node{{
				DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7), NodeType: "automation",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"command_ref":"build"},"outputs":{"to_node_result":true}}`),
			}},
		},
	}
	router := NewNodeExecutorRouter(store, nil, autoExec, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	want := []string{
		"before_execute:auto1:",
		"after_execute:auto1:done",
		"on_state_change:auto1:done",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// TestNodeExecutorRouter_EmptyConfig_PortsStillNonNil:
// 空 cfg → 三端口字段保持 nil/empty 不报错；向后兼容 F1.0 dogfood DAG。
func TestNodeExecutorRouter_EmptyConfig_PortsStillNonNil(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-empty"}
	agentExec := newTestAgentExecutor(launcher, nil)
	reader := &stubRouterPrevReader{}
	writer := &stubRouterPrevWriter{}
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7), NodeType: "agent",
			Status: string(nodeexec.NodeStatusReady),
			Config: json.RawMessage(`{"exec":{"agent_key":"a"},"first_turn":"go"}`),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, reader, writer, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	// 空 cfg.Inputs → reader / writer 不被调用
	if reader.calls != 0 {
		t.Fatalf("reader.calls = %d, want 0 (no inputs.from_sharedfiles)", reader.calls)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("writer.writes = %d, want 0 (no outputs.to_sharedfile)", len(writer.writes))
	}
}

// TestNodeExecutorRouter_ListNodesErrorPropagatesAsFrameworkErr:
// prefetchPrevResults 内 store.ListNodes 报错 → framework err（让 dispatcher
// 走 transient retry，不是节点级 validation）。
// 注意：本测试用的 store 命中 prefetch（cfg.Inputs.FromNodes 非空）后才走
// ListNodes 二次调用；首次 lookupTargetNode 用的是预存 nodes 列表。
func TestNodeExecutorRouter_ListNodesErrorPropagatesAsFrameworkErr(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-err"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterFlipFailStore{
		stubRouterStore: stubRouterStore{
			nodes: []taskdag.Node{{
				DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			}},
		},
		failAfter: 1, // 首次 lookupTargetNode 成功，prefetch 调用失败
	}
	_, err := router_helper_New(store, agentExec).RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err == nil {
		t.Fatalf("expected framework err from prefetch ListNodes failure, got nil")
	}
	if !strings.Contains(err.Error(), "prefetch prev results") {
		t.Fatalf("err = %v, want wrapping prefetch prev results", err)
	}
}

// router_helper_New 是只为测试用的 router 构造捷径，避免每处重复写 nil 列表。
func router_helper_New(store taskdag.Store, agentExec *nodeexec.AgentExecutor) *NodeExecutorRouter {
	return NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
}

// stubRouterAutoStore 嵌入 stubRouterStore + 实现 NodeFlowStore，让
// AutomationExecutor done 路径下的 CompleteNodeAndScheduleDownstream 走过去。
type stubRouterAutoStore struct {
	stubRouterStore
	completeErr error
}

func (s *stubRouterAutoStore) CompleteNodeAndScheduleDownstream(_ context.Context, _ taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	return &taskdag.CompleteNodeWithDownstreamResult{}, nil
}

func TestNodeExecutorRouter_AutomationCompleteErrorIsFrameworkError(t *testing.T) {
	t.Parallel()
	autoExec := nodeexec.NewAutomationExecutor(
		stubAutomationCmdGetter{},
		stubAutomationCmdRunner{stdout: "build ok"},
	)
	store := &stubRouterAutoStore{
		stubRouterStore: stubRouterStore{
			nodes: []taskdag.Node{{
				DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7), NodeType: "automation",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"command_ref":"build"},"outputs":{"to_node_result":true}}`),
			}},
		},
		completeErr: errors.New("store complete failed"),
	}
	router := NewNodeExecutorRouter(store, nil, autoExec, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7),
	})
	if err == nil {
		t.Fatal("RouteByWakeup err = nil, want automation complete framework error")
	}
	if !strings.Contains(err.Error(), "automation complete propagate failed") {
		t.Fatalf("err = %v, want automation complete wrapper", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done result alongside framework error", outcome.Status)
	}
}

// stubRouterFlipFailStore 让 ListNodes 第 N 次后开始报错；用于触发 prefetch 路径
// 的 framework err 测试。
type stubRouterFlipFailStore struct {
	stubRouterStore
	failAfter int
	calls     int
}

func (s *stubRouterFlipFailStore) ListNodes(ctx context.Context, dagKey string) ([]taskdag.Node, error) {
	s.calls++
	if s.calls > s.failAfter {
		return nil, errors.New("stub: flip-fail list nodes")
	}
	return s.stubRouterStore.ListNodes(ctx, dagKey)
}

func (s *stubRouterFlipFailStore) ListRunNodes(ctx context.Context, dagKey string, runID int64) ([]taskdag.Node, error) {
	s.calls++
	if s.calls > s.failAfter {
		return nil, errors.New("stub: flip-fail list run nodes")
	}
	return s.stubRouterStore.ListRunNodes(ctx, dagKey, runID)
}
