package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/fxadapter"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// stubRouterStore 实现 taskdag.Store 所需的最小方法。
// 其它方法保留 nil 嵌入，路由器测试不会触达这些持久化路径。
type stubRouterStore struct {
	taskdag.Store
	nodes   []taskdag.Node
	dag     *taskdag.DAG
	listErr error
	// 记录 ready 到 running 的推进，避免测试绕过节点启动前的状态边界。
	runningStatusErr   error                             // 默认 nil（成功路径）
	runningStatusCalls []taskdag.RunningNodeStatusUpdate // 记录调用详情
	completeErr        error
	completeCalls      []taskdag.CompleteNodeInput
}

func (s *stubRouterStore) GetDAG(_ context.Context, dagKey string) (*taskdag.DAG, error) {
	if s.dag != nil {
		return s.dag, nil
	}
	return &taskdag.DAG{DagKey: dagKey}, nil
}

func (s *stubRouterStore) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]taskdag.Node, len(s.nodes))
	copy(out, s.nodes)
	return out, nil
}

func (s *stubRouterStore) ListRunNodes(_ context.Context, _ string, runID int64) ([]taskdag.Node, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]taskdag.Node, 0, len(s.nodes))
	for i := range s.nodes {
		node := s.nodes[i]
		if node.RunID == nil || *node.RunID != runID {
			continue
		}
		out = append(out, node)
	}
	return out, nil
}

func routerTestRunID(id int64) *int64 { return &id }

// UpdateRunningNodeStatus 覆盖 taskdag.Store 嵌入，避免 advanceAgentNodeToRunning
// 触发 nil-embedding panic；记录调用并允许注入状态推进错误。
func (s *stubRouterStore) UpdateRunningNodeStatus(_ context.Context, input taskdag.RunningNodeStatusUpdate) (*taskdag.Node, error) {
	s.runningStatusCalls = append(s.runningStatusCalls, input)
	if s.runningStatusErr != nil {
		return nil, s.runningStatusErr
	}
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: input.Status}, nil
}

func (s *stubRouterStore) CompleteNodeAndScheduleDownstream(_ context.Context, input taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	s.completeCalls = append(s.completeCalls, input)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	return &taskdag.CompleteNodeWithDownstreamResult{
		Node: &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, RunID: routerTestRunID(input.RunID), Status: input.Status, Result: input.Result},
	}, nil
}

// stubAgentLauncher 是 nodeexec.AgentLauncher 的最小实现，不启动真实进程。
// 测试只关心路由传入的请求、threadID 和可注入错误。
type stubAgentLauncher struct {
	threadID string
	err      error
	errs     []error
	calls    []contract.LaunchRequest
}

func (l *stubAgentLauncher) LaunchAgent(_ context.Context, req contract.LaunchRequest) (string, error) {
	l.calls = append(l.calls, req)
	if len(l.errs) > 0 {
		err := l.errs[0]
		l.errs = l.errs[1:]
		return l.threadID, err
	}
	return l.threadID, l.err
}

// TestNodeExecutorRouter_RoutesAgentNode 验证 agent 节点会经由 AgentExecutor
// 执行，并把唤醒请求转换为 launcher 可见的启动参数。
func TestNodeExecutorRouter_RoutesAgentNode(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-x"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Title:    "n1",
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
			Status:   "ready",
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID:      1,
		DagKey:  "dag-1",
		NodeKey: "n1",
		RunID:   routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done; class=%s summary=%q", outcome.Status, outcome.FailureClass, outcome.ErrorSummary)
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
	agentExec := newTestAgentExecutor(launcher, nodeexec.WithHooks(recordingLifecycleHooks(&events)))
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Title:    "n1",
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
			Status:   string(nodeexec.NodeStatusReady),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID:      101,
		DagKey:  "dag-1",
		NodeKey: "n1",
		RunID:   routerTestRunID(7),
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
	agentExec, err := ProvideAgentExecutor(&stubAgentLauncher{}, noopNodeSpawnRecorder{}, hooks)
	if err != nil {
		t.Fatalf("ProvideAgentExecutor() error = %v, want nil", err)
	}
	if got := agentExec.Hooks(); got[nodeexec.HookBeforeExecute] == nil || got[nodeexec.HookOnFailure] == nil {
		t.Fatalf("agent hooks = %v, want production lifecycle hooks wired", got)
	}
	if _, err := ProvideAgentExecutor(&stubAgentLauncher{}, nil, hooks); err == nil {
		t.Fatalf("ProvideAgentExecutor(nil recorder) error = nil, want fail-fast")
	}
	autoExec := ProvideAutomationExecutor(stubAutomationCmdGetter{}, stubAutomationCmdRunner{}, hooks)
	if got := autoExec.Hooks(); got[nodeexec.HookBeforeExecute] == nil || got[nodeexec.HookOnFailure] == nil {
		t.Fatalf("automation hooks = %v, want production lifecycle hooks wired", got)
	}
}

func TestNodeExecutorRouter_LifecycleHookTimeoutDoesNotBlockDispatch(t *testing.T) {
	canceled := make(chan struct{})
	launcher := &stubAgentLauncher{threadID: "thread-hook-timeout"}
	agentExec := newTestAgentExecutor(launcher, nodeexec.WithHooks(map[nodeexec.HookPoint]nodeexec.HookHandler{
		nodeexec.HookBeforeExecute: blockingLifecycleHook{canceled: canceled},
	}))
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
			Status:   string(nodeexec.NodeStatusReady),
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	started := time.Now()
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID:      102,
		DagKey:  "dag-1",
		NodeKey: "n1",
		RunID:   routerTestRunID(7),
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

func TestNodeExecutorRouter_AutomationWritesRunningFenceAndAppliesTimeout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgRaw, err := json.Marshal(nodeexec.AutomationNodeConfig{
		Exec: nodeexec.AutomationExecConfig{
			CommandRef:     "build",
			CWD:            root,
			WorkspaceRoots: []string{root},
		},
		Execution: nodeexec.ExecutionConfig{Timeout: "25ms"},
	})
	if err != nil {
		t.Fatalf("marshal automation config: %v", err)
	}
	store := &stubRouterStore{
		dag: &taskdag.DAG{
			DagKey:   "dag-1",
			Metadata: json.RawMessage(`{"execution":{"timeout":"75ms"}}`),
		},
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "auto-1",
			RunID:    routerTestRunID(7),
			NodeType: "automation",
			Title:    "auto-1",
			Config:   cfgRaw,
			Status:   string(nodeexec.NodeStatusReady),
		}},
	}
	runner := &deadlineAssertingAutomationRunner{store: store}
	autoExec := nodeexec.NewAutomationExecutor(stubAutomationCmdGetter{}, runner)
	router := NewNodeExecutorRouter(store, nil, autoExec, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID:           55,
		DagKey:       "dag-1",
		NodeKey:      "auto-1",
		RunID:        routerTestRunID(7),
		AttemptCount: 3,
	})
	requireAutomationRouteDone(t, outcome, err, runner)
	requireAutomationWakeupFence(t, store, 55, 3)
}

// TestNodeExecutorRouter_EmptyNodeTypeDefaultsToAgent 验证旧 DAG 未写 node_type
// 时仍按 agent 处理，避免历史节点在启动路由上被误判为 validation 失败。
func TestNodeExecutorRouter_EmptyNodeTypeDefaultsToAgent(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-y"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "", // 旧 DAG 可能缺省该字段。
			Title:    "n1",
			Config:   testRawConfig(t, `{"exec":{"agent_key":"a","cwd":"/tmp/node-cwd"},"first_turn":"x"}`),
			Status:   "ready",
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done (default-agent fallback)", outcome.Status)
	}
}

// TestNodeExecutorRouter_HybridReturnsValidationFailure 验证 hybrid 节点当前仍
// 返回 validation 失败，让 dispatcher 按不可重试路径处理。
func TestNodeExecutorRouter_HybridReturnsValidationFailure(t *testing.T) {
	t.Parallel()
	store := &stubRouterStore{
		nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7), NodeType: "hybrid"}},
	}
	router := NewNodeExecutorRouter(store, nil, nil, nil, nil, nil)
	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
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

// TestNodeExecutorRouter_NodeNotFoundIsFrameworkError 验证唤醒指向的节点缺失时
// 透出框架错误，给 dispatcher 保留重试临时同步漂移的机会。
func TestNodeExecutorRouter_NodeNotFoundIsFrameworkError(t *testing.T) {
	t.Parallel()
	store := &stubRouterStore{nodes: []taskdag.Node{}}
	router := NewNodeExecutorRouter(store, nil, nil, nil, nil, nil)
	_, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "ghost", RunID: routerTestRunID(7),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not-found framework error", err)
	}
}

// TestNodeExecutorRouter_StoreListErrorPropagates 验证 store 查询失败会透出，
// 由 dispatcher 按临时框架错误决定是否重试。
func TestNodeExecutorRouter_StoreListErrorPropagates(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("db reset")
	store := &stubRouterStore{listErr: sentinel}
	router := NewNodeExecutorRouter(store, nil, nil, nil, nil, nil)
	_, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapping sentinel", err)
	}
}

// TestNodeExecutorRouter_MissingDagInfoIsFrameworkError 验证 dag_key/node_key
// 缺失会被视为框架错误；正常入队不会触发该路径。
func TestNodeExecutorRouter_MissingDagInfoIsFrameworkError(t *testing.T) {
	t.Parallel()
	router := NewNodeExecutorRouter(&stubRouterStore{}, nil, nil, nil, nil, nil)
	_, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		DagKey: "", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err == nil {
		t.Fatalf("expected error for missing dag_key")
	}
}

func TestNodeExecutorRouter_MissingRunIDIsFrameworkError(t *testing.T) {
	t.Parallel()
	router := NewNodeExecutorRouter(&stubRouterStore{}, nil, nil, nil, nil, nil)
	_, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID: 9, DagKey: "dag-1", NodeKey: "n1",
	})
	if err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("err = %v, want run_id required", err)
	}
}

// TestServiceAgentLauncher_AdapterReturnsThreadIDOnSuccess 覆盖 serviceAgentLauncher
// 的 nil service 边界，避免适配层在依赖未装配时 panic。
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

func TestStoreNodeSpawnRecorder_NilStoreFailsFast(t *testing.T) {
	t.Parallel()
	got, err := fxadapter.NewStoreNodeSpawnRecorder(nil)
	if err == nil {
		t.Fatalf("NewStoreNodeSpawnRecorder(nil) error = nil, want fail-fast")
	}
	if got != nil {
		t.Fatalf("NewStoreNodeSpawnRecorder(nil) = %T, want nil", got)
	}
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

func (s stubAutomationCmdRunner) RunCommandCard(_ context.Context, card nodeexec.AutomationCommandCard, _ json.RawMessage, _ ...nodeexec.AutomationCommandRunOptions) (nodeexec.AutomationCommandResult, error) {
	return nodeexec.AutomationCommandResult{
		CardKey:  card.CardKey,
		Stdout:   s.stdout,
		ExitCode: 0,
	}, nil
}

func requireAutomationRouteDone(t *testing.T, outcome nodeexec.NodeOutcome, err error, runner *deadlineAssertingAutomationRunner) {
	t.Helper()
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done; class=%s summary=%q", outcome.Status, outcome.FailureClass, outcome.ErrorSummary)
	}
	if runner.called != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.called)
	}
	if runner.timeoutBudget <= 0 || runner.timeoutBudget > 100*time.Millisecond {
		t.Fatalf("runner timeout budget = %s, want node execution timeout applied", runner.timeoutBudget)
	}
}

func requireAutomationWakeupFence(t *testing.T, store *stubRouterStore, wakeupID int64, attempt int32) {
	t.Helper()
	if len(store.runningStatusCalls) != 1 {
		t.Fatalf("runningStatusCalls = %d, want 1 before command", len(store.runningStatusCalls))
	}
	if got := store.runningStatusCalls[0].WakeupID; got != wakeupID {
		t.Fatalf("running WakeupID = %d, want %d", got, wakeupID)
	}
	if len(store.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(store.completeCalls))
	}
	if got := store.completeCalls[0].WakeupID; got != wakeupID {
		t.Fatalf("complete WakeupID = %d, want %d", got, wakeupID)
	}
	if got := store.completeCalls[0].WakeupAttempt; got != attempt {
		t.Fatalf("complete WakeupAttempt = %d, want %d", got, attempt)
	}
}

type deadlineAssertingAutomationRunner struct {
	store         *stubRouterStore
	called        int
	timeoutBudget time.Duration
}

func (r *deadlineAssertingAutomationRunner) RunCommandCard(ctx context.Context, card nodeexec.AutomationCommandCard, _ json.RawMessage, _ ...nodeexec.AutomationCommandRunOptions) (nodeexec.AutomationCommandResult, error) {
	r.called++
	if r.store == nil || len(r.store.runningStatusCalls) == 0 {
		return nodeexec.AutomationCommandResult{}, errors.New("automation command ran before ready-to-running fence")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nodeexec.AutomationCommandResult{}, errors.New("automation command missing execution timeout")
	}
	r.timeoutBudget = time.Until(deadline)
	return nodeexec.AutomationCommandResult{CardKey: card.CardKey, Stdout: "ok", ExitCode: 0}, nil
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
