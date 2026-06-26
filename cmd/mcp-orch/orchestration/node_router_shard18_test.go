package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ----- RunContext 端口预填测试夹具 -----
// 这些测试验证 router 在进入 executor 前填好上游结果、shared file 读写端口和生命周期 hook。

// stubRouterPrevReader 是 SharedFileReader 的最小测试实现；记录调用次数以确认输入端口被使用。
type stubRouterPrevReader struct {
	contents map[string]string
	calls    int
}

func (s *stubRouterPrevReader) ReadSharedFile(_ context.Context, path string) (string, bool, error) {
	s.calls++
	c, ok := s.contents[path]
	return c, ok, nil
}

// stubRouterPrevWriter 是 SharedFileWriter 的最小测试实现；保留写入内容供断言输出端口。
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

// recordingAgentLauncher 记录 AgentExecutor 交给 launcher 的 LaunchRequest。
// 测试只检查 Prompt 中的 inputs 前缀和上游内容，不启动真实 provider。
type recordingAgentLauncher struct {
	threadID string
	calls    []contract.LaunchRequest
}

func (l *recordingAgentLauncher) LaunchAgent(_ context.Context, req contract.LaunchRequest) (string, error) {
	l.calls = append(l.calls, req)
	return l.threadID, nil
}

// TestNodeExecutorRouter_PrefetchPrevResults_FromNodes_NonEmpty 验证 from_nodes 会预取同 run 的 done 结果。
// 预取内容必须通过 RunContext.PrevResults 注入到 agent prompt，缺失时下游 agent 看不到上游产物。
func TestNodeExecutorRouter_PrefetchPrevResults_FromNodes_NonEmpty(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-1"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: testRawConfig(t, `{"summary":"upstream done"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7), NodeType: "agent",
				Title: "downstream", Status: string(nodeexec.NodeStatusReady),
				Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
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
				Result: testRawConfig(t, `{"summary":"wrong run"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: &runA, NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go A"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: &runB, NodeType: "agent",
				Status: string(nodeexec.NodeStatusDone),
				Result: testRawConfig(t, `{"summary":"right run"}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: &runB, NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go B"}`),
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

// TestNodeExecutorRouter_PrefetchPrevResults_FiltersNonDoneUpstream 验证非 done 上游不会进入 PrevResults。
// 下游引用这种节点时必须得到 validation 结果，避免把未完成产物当成有效输入。
func TestNodeExecutorRouter_PrefetchPrevResults_FiltersNonDoneUpstream(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-2"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{
			{
				DagKey: "dag-1", NodeKey: "upstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusRunning), // 不是 done，不能作为 from_nodes 输入。
				Result: testRawConfig(t, `{"x":1}`),
			},
			{
				DagKey: "dag-1", NodeKey: "downstream", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
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

// TestNodeExecutorRouter_PrefetchPrevResults_EmptyResultPlaceholder 验证 done 但结果为空的占位行为。
// 空结果会以 "(empty)" 注入 prompt，表示节点完成但没有输出，而不是 validation 失败。
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
				Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
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

// TestNodeExecutorRouter_SharedFileReader_Injected 验证 from_sharedfiles 通过注入 reader 读取内容。
// router 只负责把读取结果拼进 prompt；真实 shared file 权限和锁由 reader 实现负责。
func TestNodeExecutorRouter_SharedFileReader_Injected(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-4"}
	agentExec := newTestAgentExecutor(launcher, nil)
	reader := &stubRouterPrevReader{contents: map[string]string{"plan.md": "plan content"}}
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7), NodeType: "agent",
			Status: string(nodeexec.NodeStatusReady),
			Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_sharedfiles":["plan.md"]},"first_turn":"go"}`),
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

// TestNodeExecutorRouter_SharedFileWriter_Injected 验证 automation 输出通过注入 writer 写入 shared file。
// 这里锁定 RunContext.SharedFileWriter 的传递路径，不触碰真实文件系统。
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
				Config: testRawConfig(t, `{"exec":{"command_ref":"build"},"outputs":{"to_sharedfile":{"path":"reports/out.log","lock_mode":"exclusive"}}}`),
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

func TestNodeExecutorRouter_AutomationSharedfileOnlyEnvelopeFeedsDownstreamAgent(t *testing.T) {
	t.Parallel()
	autoExec := nodeexec.NewAutomationExecutor(
		stubAutomationCmdGetter{},
		stubAutomationCmdRunner{stdout: "build ok"},
	)
	writer := &stubRouterPrevWriter{}
	launcher := &recordingAgentLauncher{threadID: "thr-downstream"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterAutoStore{
		stubRouterStore: stubRouterStore{
			nodes: []taskdag.Node{
				{
					DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7), NodeType: "automation",
					Status: string(nodeexec.NodeStatusReady),
					Config: testRawConfig(t, `{"exec":{"command_ref":"build"},"outputs":{"to_sharedfile":{"path":"reports/out.log","lock_mode":"exclusive"}}}`),
				},
				{
					DagKey: "dag-1", NodeKey: "agent1", RunID: routerTestRunID(7), NodeType: "agent",
					Status: string(nodeexec.NodeStatusReady),
					Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_nodes":["auto1"]},"first_turn":"continue"}`),
				},
			},
		},
	}
	router := NewNodeExecutorRouter(store, agentExec, autoExec, nil, writer, nil)

	upstream, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "auto1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("upstream RouteByWakeup err = %v", err)
	}
	if upstream.Status != nodeexec.NodeStatusDone {
		t.Fatalf("upstream status = %v, want done", upstream.Status)
	}
	if len(store.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(store.completeCalls))
	}
	if got := string(store.completeCalls[0].Result); !strings.Contains(got, "reports/out.log") {
		t.Fatalf("automation complete result = %s, want sharedfile path envelope", got)
	}

	store.nodes[0].Status = string(nodeexec.NodeStatusDone)
	store.nodes[0].Result = append([]byte(nil), store.completeCalls[0].Result...)
	downstream, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "agent1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("downstream RouteByWakeup err = %v", err)
	}
	if downstream.Status != nodeexec.NodeStatusDone {
		t.Fatalf("downstream status = %v, want done", downstream.Status)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1", len(launcher.calls))
	}
	if !strings.Contains(launcher.calls[0].Prompt, "reports/out.log") {
		t.Fatalf("downstream prompt = %q, want upstream sharedfile path", launcher.calls[0].Prompt)
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
				Config: testRawConfig(t, `{"exec":{"command_ref":"build"},"outputs":{"to_node_result":true}}`),
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

// TestNodeExecutorRouter_EmptyConfig_PortsStillNonNil 验证未声明 inputs/outputs 时端口保持空闲。
// reader/writer 不能被误调用，保证默认配置不会产生隐式 shared file 副作用。
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
			Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"first_turn":"go"}`),
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
	// 没有 inputs/outputs 配置时，reader 和 writer 都必须保持未调用。
	if reader.calls != 0 {
		t.Fatalf("reader.calls = %d, want 0 (no inputs.from_sharedfiles)", reader.calls)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("writer.writes = %d, want 0 (no outputs.to_sharedfile)", len(writer.writes))
	}
}

// TestNodeExecutorRouter_ListNodesErrorPropagatesAsFrameworkErr 验证预取阶段的 store 错误向上冒泡。
// 这种错误属于框架读依赖失败，应交给 dispatcher 重试，而不是落成节点级 validation。
// 测试 store 首次查询供 lookupTargetNode 使用，第二次 from_nodes 预取才触发错误。
func TestNodeExecutorRouter_ListNodesErrorPropagatesAsFrameworkErr(t *testing.T) {
	t.Parallel()
	launcher := &recordingAgentLauncher{threadID: "thr-err"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterFlipFailStore{
		stubRouterStore: stubRouterStore{
			nodes: []taskdag.Node{{
				DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7), NodeType: "agent",
				Status: string(nodeexec.NodeStatusReady),
				Config: testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"inputs":{"from_nodes":["upstream"]},"first_turn":"go"}`),
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

// router_helper_New 是测试专用 router 构造捷径，避免每个用例重复传入空端口。
func router_helper_New(store taskdag.Store, agentExec *nodeexec.AgentExecutor) *NodeExecutorRouter {
	return NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
}

// stubRouterAutoStore 嵌入 stubRouterStore 并实现 NodeFlowStore。
// automation done 路径会调用 CompleteNodeAndScheduleDownstream，测试用它捕获完成结果。
type stubRouterAutoStore struct {
	stubRouterStore
	completeErr   error
	completeCalls []taskdag.CompleteNodeInput
}

func (s *stubRouterAutoStore) CompleteNodeAndScheduleDownstream(_ context.Context, input taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	s.completeCalls = append(s.completeCalls, input)
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
				Config: testRawConfig(t, `{"exec":{"command_ref":"build"},"outputs":{"to_node_result":true}}`),
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

// stubRouterFlipFailStore 在 ListNodes/ListRunNodes 调用超过阈值后返回错误。
// 它用于把 lookup 成功和预取失败拆开，精确覆盖 framework error 路径。
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
