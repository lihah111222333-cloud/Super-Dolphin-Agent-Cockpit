package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
	// ADR-017 v1.2 §2.4：dispatchAgent 调 UpdateRunningNodeStatus 推 ready→running。
	runningStatusErr   error                             // 默认 nil（成功路径）
	runningStatusCalls []taskdag.RunningNodeStatusUpdate // 记录调用详情
}

func (s *stubRouterStore) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]taskdag.Node, len(s.nodes))
	copy(out, s.nodes)
	return out, nil
}

// UpdateRunningNodeStatus 覆盖 taskdag.Store 嵌入，避免 advanceAgentNodeToRunning
// nil-embedding panic。记录调用 + 返 runningStatusErr（默认 nil = success）。
func (s *stubRouterStore) UpdateRunningNodeStatus(_ context.Context, input taskdag.RunningNodeStatusUpdate) (*taskdag.Node, error) {
	s.runningStatusCalls = append(s.runningStatusCalls, input)
	if s.runningStatusErr != nil {
		return nil, s.runningStatusErr
	}
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: input.Status}, nil
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

func TestNodeExecutorRouter_AgentLifecycleHooks(t *testing.T) {
	events := []string{}
	launcher := &stubAgentLauncher{threadID: "thread-hook"}
	agentExec := nodeexec.NewAgentExecutor(launcher, nodeexec.WithHooks(recordingLifecycleHooks(&events)))
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "agent",
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:   string(nodeexec.NodeStatusReady),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID:      101,
		DagKey:  "dag-1",
		NodeKey: "n1",
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	want := []string{
		"before_execute:n1:",
		"after_execute:n1:done",
		"on_state_change:n1:running",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestProvideExecutorsWireLifecycleHooks(t *testing.T) {
	hooks := ProvideNodeLifecycleHooks(discardLogger())
	agentExec := ProvideAgentExecutor(&stubAgentLauncher{}, nil, hooks)
	if got := agentExec.Hooks(); got[nodeexec.HookBeforeExecute] == nil || got[nodeexec.HookOnFailure] == nil {
		t.Fatalf("agent hooks = %v, want production lifecycle hooks wired", got)
	}
	autoExec := ProvideAutomationExecutor(stubAutomationCmdGetter{}, stubAutomationCmdRunner{}, hooks)
	if got := autoExec.Hooks(); got[nodeexec.HookBeforeExecute] == nil || got[nodeexec.HookOnFailure] == nil {
		t.Fatalf("automation hooks = %v, want production lifecycle hooks wired", got)
	}
}

func TestNodeExecutorRouter_LifecycleHookTimeoutDoesNotBlockDispatch(t *testing.T) {
	canceled := make(chan struct{})
	launcher := &stubAgentLauncher{threadID: "thread-hook-timeout"}
	agentExec := nodeexec.NewAgentExecutor(launcher, nodeexec.WithHooks(map[nodeexec.HookPoint]nodeexec.HookHandler{
		nodeexec.HookBeforeExecute: blockingLifecycleHook{canceled: canceled},
	}))
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "agent",
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:   string(nodeexec.NodeStatusReady),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	started := time.Now()
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID:      102,
		DagKey:  "dag-1",
		NodeKey: "n1",
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	if elapsed > time.Second {
		t.Fatalf("RouteByWakeup blocked on hook for %s, want bounded under 1s", elapsed)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1", len(launcher.calls))
	}
	select {
	case <-canceled:
	case <-time.After(lifecycleHookExecutionTimeout + lifecycleHookDispatchWait):
		t.Fatal("slow hook context was not canceled")
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
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: json.RawMessage(`{"summary":"upstream done"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", NodeType: "agent",
				Title: "downstream", Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "downstream",
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

// TestNodeExecutorRouter_PrefetchPrevResults_FiltersNonDoneUpstream:
// 上游节点 status != done → 不入 PrevResults map；nodeexec.loadFromNodes 因此
// 报 validation "references unknown node_key"，保 fail-loud。
func TestNodeExecutorRouter_PrefetchPrevResults_FiltersNonDoneUpstream(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-2"}
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", NodeType: "agent",
				Status: string(nodeexec.NodeStatusRunning), // 未 done
				Result: json.RawMessage(`{"x":1}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "downstream",
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusFailed || outcome.FailureClass != nodeexec.FailureClassValidation {
		t.Fatalf("outcome = %+v, want failed+validation", outcome)
	}
}

// TestNodeExecutorRouter_PrefetchPrevResults_EmptyResultPlaceholder:
// 上游 done 但 Result NULL → nodeexec.loadFromNodes 走 "(empty)" 占位分支；
// 不抛 validation。
func TestNodeExecutorRouter_PrefetchPrevResults_EmptyResultPlaceholder(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-3"}
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: nil, // NULL
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "downstream",
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
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	reader := &stubRouterPrevReader{contents: map[string]string{"plan.md": "plan content"}}
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey: "dag-1", NodeKey: "n1", NodeType: "agent",
			Status: string(nodeexec.NodeStatusReady),
			Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_sharedfiles":["plan.md"]},"first_turn":"go"}`),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, reader, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1",
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
				DagKey: "dag-1", NodeKey: "auto1", NodeType: "automation",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"command_ref":"build"},"outputs":{"to_sharedfile":{"path":"reports/out.log","lock_mode":"exclusive"}}}`),
			}},
		},
	}
	router := NewNodeExecutorRouter(store, nil, autoExec, nil, writer, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "auto1",
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
				DagKey: "dag-1", NodeKey: "auto1", NodeType: "automation",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"command_ref":"build"},"outputs":{"to_node_result":true}}`),
			}},
		},
	}
	router := NewNodeExecutorRouter(store, nil, autoExec, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "auto1",
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
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	reader := &stubRouterPrevReader{}
	writer := &stubRouterPrevWriter{}
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey: "dag-1", NodeKey: "n1", NodeType: "agent",
			Status: string(nodeexec.NodeStatusReady),
			Config: json.RawMessage(`{"exec":{"agent_key":"a"},"first_turn":"go"}`),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, reader, writer, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1",
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
	agentExec := nodeexec.NewAgentExecutor(launcher, nil)
	store := &stubRouterFlipFailStore{
		stubRouterStore: stubRouterStore{
			nodes: []taskdag.Node{{
				DagKey: "dag-1", NodeKey: "n1", NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: json.RawMessage(`{"exec":{"agent_key":"a"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
			}},
		},
		failAfter: 1, // 首次 lookupTargetNode 成功，prefetch 调用失败
	}
	_, err := router_helper_New(store, agentExec).RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1",
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
}

func (s *stubRouterAutoStore) CompleteNodeAndScheduleDownstream(_ context.Context, _ taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	return &taskdag.CompleteNodeWithDownstreamResult{}, nil
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

// stubAutomationCmdGetter 是 AutomationExecutor 测试用的 CommandGetter 假实现。
type stubAutomationCmdGetter struct{}

func (stubAutomationCmdGetter) GetCommandCard(_ context.Context, cardKey string) (nodeexec.AutomationCommandCard, error) {
	return nodeexec.AutomationCommandCard{
		CardKey:         cardKey,
		CommandTemplate: "echo ok",
		Enabled:         true,
	}, nil
}

// stubAutomationCmdRunner 是 AutomationExecutor 测试用的 CommandRunner 假实现。
type stubAutomationCmdRunner struct {
	stdout string
}

func (s stubAutomationCmdRunner) RunCommandCard(_ context.Context, card nodeexec.AutomationCommandCard, _ json.RawMessage) (nodeexec.AutomationCommandResult, error) {
	return nodeexec.AutomationCommandResult{
		CardKey:  card.CardKey,
		Stdout:   s.stdout,
		ExitCode: 0,
	}, nil
}

type recordingLifecycleHook struct {
	events *[]string
}

func recordingLifecycleHooks(events *[]string) map[nodeexec.HookPoint]nodeexec.HookHandler {
	hook := recordingLifecycleHook{events: events}
	return map[nodeexec.HookPoint]nodeexec.HookHandler{
		nodeexec.HookBeforeExecute: hook,
		nodeexec.HookAfterExecute:  hook,
		nodeexec.HookOnStateChange: hook,
		nodeexec.HookOnFailure:     hook,
	}
}

func (h recordingLifecycleHook) Handle(_ context.Context, point nodeexec.HookPoint, node nodeexec.Node, outcome nodeexec.NodeOutcome) error {
	*h.events = append(*h.events, string(point)+":"+node.NodeKey+":"+string(outcome.Status))
	return nil
}

type recordingLifecycleOutcomeHook struct {
	events *[]string
}

func recordingLifecycleOutcomeHooks(events *[]string) map[nodeexec.HookPoint]nodeexec.HookHandler {
	hook := recordingLifecycleOutcomeHook{events: events}
	return map[nodeexec.HookPoint]nodeexec.HookHandler{
		nodeexec.HookBeforeExecute: hook,
		nodeexec.HookAfterExecute:  hook,
		nodeexec.HookOnStateChange: hook,
		nodeexec.HookOnFailure:     hook,
	}
}

func (h recordingLifecycleOutcomeHook) Handle(_ context.Context, point nodeexec.HookPoint, node nodeexec.Node, outcome nodeexec.NodeOutcome) error {
	*h.events = append(*h.events, string(point)+":"+node.NodeKey+":"+string(outcome.Status)+":"+string(outcome.FailureClass))
	return nil
}

type slowLifecycleHook struct {
	completed chan<- struct{}
	canceled  chan<- struct{}
}

func (h slowLifecycleHook) Handle(ctx context.Context, _ nodeexec.HookPoint, _ nodeexec.Node, _ nodeexec.NodeOutcome) error {
	select {
	case <-time.After(lifecycleHookDispatchWait * 2):
		close(h.completed)
		return nil
	case <-ctx.Done():
		close(h.canceled)
		return ctx.Err()
	}
}

type blockingLifecycleHook struct {
	canceled chan<- struct{}
}

func (h blockingLifecycleHook) Handle(ctx context.Context, _ nodeexec.HookPoint, _ nodeexec.Node, _ nodeexec.NodeOutcome) error {
	<-ctx.Done()
	close(h.canceled)
	return ctx.Err()
}
