package orchestration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// dispatcherStubStore �?Phase 3.1/3.2 单测用的 taskdag.Store 假实现，
// 关注 ClaimDueWakeups + MarkWakeupSent + RetryWakeup + FailWakeup 行为�?
// 记录入参 + 按预设结果返回。其余方法返回安�?zero / 错误，避�?
// dispatcher 误用未接通的 store 接口面�?
type dispatcherStubStore struct {
	taskdag.Store // 默认 nil 嵌入，调用任何未覆盖方法都会 panic（暴露遗漏）

	claimCalls []taskdag.ClaimDueWakeupsInput
	claimReply []taskdag.Wakeup
	claimErr   error

	markSentCalls []taskdag.MarkWakeupSentInput
	markSentRows  int64
	markSentErr   error

	retryCalls []taskdag.RetryWakeupInput
	retryRows  int64 // <0 = 模拟 SQL attempt_count >= 8 触发的兜�?fail
	retryErr   error

	failCalls   []taskdag.FailWakeupInput
	failRows    int64
	failRowsSet bool
	failErr     error

	// Phase 3.5w: DAG-aware 决策需要的 stub 字段。默�?dagReply=nil �?
	// resolveDAGRetryPolicy 退化到�?RetryWakeup 路径，老用例不受影响�?
	dagReply         *taskdag.DAG
	dagErr           error
	nodesReply       []taskdag.Node
	nodesErr         error
	failNodeCalls    []taskdag.FailNodeInput
	failNodeReply    *taskdag.FailNodeResult
	failNodeErr      error
	completeCalls    []taskdag.CompleteNodeInput
	completeReply    *taskdag.CompleteNodeWithDownstreamResult
	completeErr      error
	patchConfigCalls []taskdag.NodeConfigPatchInput
	patchConfigReply *taskdag.Node
	patchConfigErr   error
	runningCalls     []taskdag.RunningNodeStatusUpdate
	runningErr       error
}

func (s *dispatcherStubStore) GetDAG(_ context.Context, _ string) (*taskdag.DAG, error) {
	return s.dagReply, s.dagErr
}

func (s *dispatcherStubStore) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	return s.nodesReply, s.nodesErr
}

func (s *dispatcherStubStore) ListRunNodes(_ context.Context, _ string, runID int64) ([]taskdag.Node, error) {
	if s.nodesErr != nil {
		return nil, s.nodesErr
	}
	out := make([]taskdag.Node, len(s.nodesReply))
	for i := range s.nodesReply {
		out[i] = s.nodesReply[i]
		if out[i].RunID == nil {
			id := runID
			out[i].RunID = &id
		}
	}
	return out, nil
}

func (s *dispatcherStubStore) FailNodeAndCancelDownstream(_ context.Context, input taskdag.FailNodeInput) (*taskdag.FailNodeResult, error) {
	s.failNodeCalls = append(s.failNodeCalls, input)
	if s.failNodeErr != nil {
		return nil, s.failNodeErr
	}
	if s.failNodeReply != nil {
		return s.failNodeReply, nil
	}
	return &taskdag.FailNodeResult{}, nil
}

func (s *dispatcherStubStore) CompleteNodeAndScheduleDownstream(_ context.Context, input taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	s.completeCalls = append(s.completeCalls, input)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	if s.completeReply != nil {
		return s.completeReply, nil
	}
	return &taskdag.CompleteNodeWithDownstreamResult{}, nil
}

func (s *dispatcherStubStore) PatchNodeConfigIfUnchanged(_ context.Context, input taskdag.NodeConfigPatchInput) (*taskdag.Node, error) {
	s.patchConfigCalls = append(s.patchConfigCalls, input)
	if s.patchConfigErr != nil {
		return nil, s.patchConfigErr
	}
	if s.patchConfigReply != nil {
		return s.patchConfigReply, nil
	}
	return &taskdag.Node{
		DagKey:  input.DagKey,
		NodeKey: input.NodeKey,
		Config:  input.Config,
	}, nil
}

func (s *dispatcherStubStore) RetryWakeupWithNodeConfigPatch(ctx context.Context, input taskdag.RetryWakeupWithNodeConfigPatchInput) (int64, error) {
	rows, err := s.RetryWakeup(ctx, input.RetryWakeup)
	if err != nil || rows == 0 {
		return rows, err
	}
	_, err = s.PatchNodeConfigIfUnchanged(ctx, input.NodeConfig)
	if err != nil {
		return 0, err
	}
	return rows, nil
}

func (s *dispatcherStubStore) UpdateRunningNodeStatus(_ context.Context, input taskdag.RunningNodeStatusUpdate) (*taskdag.Node, error) {
	s.runningCalls = append(s.runningCalls, input)
	if s.runningErr != nil {
		return nil, s.runningErr
	}
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, RunID: &input.RunID, Status: input.Status}, nil
}

func (s *dispatcherStubStore) ClaimDueWakeups(_ context.Context, input taskdag.ClaimDueWakeupsInput) ([]taskdag.Wakeup, error) {
	s.claimCalls = append(s.claimCalls, input)
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return s.claimReply, nil
}

func (s *dispatcherStubStore) MarkWakeupSent(_ context.Context, input taskdag.MarkWakeupSentInput) (int64, error) {
	s.markSentCalls = append(s.markSentCalls, input)
	if s.markSentErr != nil {
		return 0, s.markSentErr
	}
	if s.markSentRows == 0 {
		return 1, nil
	}
	return s.markSentRows, nil
}

func (s *dispatcherStubStore) RetryWakeup(_ context.Context, input taskdag.RetryWakeupInput) (int64, error) {
	s.retryCalls = append(s.retryCalls, input)
	if s.retryErr != nil {
		return 0, s.retryErr
	}
	// 默认返回 1（重试成功写入）；显式设 retryRows<0 模拟 SQL 兜底（attempt 上限）�?
	if s.retryRows == 0 {
		return 1, nil
	}
	if s.retryRows < 0 {
		return 0, nil
	}
	return s.retryRows, nil
}

func (s *dispatcherStubStore) FailWakeup(_ context.Context, input taskdag.FailWakeupInput) (int64, error) {
	s.failCalls = append(s.failCalls, input)
	if s.failErr != nil {
		return 0, s.failErr
	}
	if s.failRowsSet {
		return s.failRows, nil
	}
	if s.failRows == 0 {
		return 1, nil
	}
	return s.failRows, nil
}

// dispatcherStubLauncher �?WakeupLauncher 的假实现：记录每�?LaunchAgent
// 的入�?+ 按预设错误队列返回。空队列时一律成功�?
type dispatcherStubLauncher struct {
	calls []LaunchRequest
	errs  []error // FIFO；超出长度后默认 nil
}

func (l *dispatcherStubLauncher) LaunchAgent(_ context.Context, req LaunchRequest) error {
	l.calls = append(l.calls, req)
	if len(l.errs) == 0 {
		return nil
	}
	err := l.errs[0]
	l.errs = l.errs[1:]
	return err
}

type recordingDispatchRetryAlertSink struct {
	mu    sync.Mutex
	calls []DispatchRetryAlert
	err   error
	block chan struct{}
}

func (s *recordingDispatchRetryAlertSink) AlertDispatchRetry(_ context.Context, alert DispatchRetryAlert) error {
	if s.block != nil {
		<-s.block
	}
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, alert)
	return nil
}

func (s *recordingDispatchRetryAlertSink) snapshot() []DispatchRetryAlert {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DispatchRetryAlert, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *recordingDispatchRetryAlertSink) waitForCalls(t *testing.T, want int) []DispatchRetryAlert {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls := s.snapshot()
		if len(calls) >= want {
			return calls
		}
		time.Sleep(10 * time.Millisecond)
	}
	calls := s.snapshot()
	t.Fatalf("alert calls = %d, want %d", len(calls), want)
	return nil
}

func TestNewWakeupDispatcherRejectsNilStore(t *testing.T) {
	if _, err := NewWakeupDispatcher(nil, nil, nil, WakeupDispatcherConfig{}); err == nil {
		t.Fatalf("err = nil, want error for nil store")
	}
}

func TestWakeupDispatcherFailsDAGWakeupMissingRunID(t *testing.T) {
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-a"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	d.WithNodeRouter(NewNodeExecutorRouter(store, nil, nil, nil, nil, nil))

	d.handleClaimed(context.Background(), &taskdag.Wakeup{
		ID:            77,
		DagKey:        "dag-1",
		NodeKey:       "n1",
		TargetAgentID: "agent-a",
		ClaimedBy:     "worker-a",
	})

	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(store.failCalls))
	}
	if !strings.Contains(store.failCalls[0].LastError, "run_id") {
		t.Fatalf("FailWakeup LastError = %q, want run_id", store.failCalls[0].LastError)
	}
	if len(store.retryCalls) != 0 || len(store.markSentCalls) != 0 {
		t.Fatalf("retry=%d markSent=%d, want 0/0 for malformed DAG wakeup", len(store.retryCalls), len(store.markSentCalls))
	}
}

func TestWakeupDispatcherTickEmptyClaimReturnsZero(t *testing.T) {
	store := &dispatcherStubStore{} // claimReply 默认 nil �?�?slice 等价
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	if n != 0 {
		t.Fatalf("Tick claimed = %d, want 0 on empty due-set", n)
	}
	if len(store.claimCalls) != 1 {
		t.Fatalf("ClaimDueWakeups called %d times, want 1", len(store.claimCalls))
	}
}

func TestWakeupDispatcherTickPassesConfigToClaim(t *testing.T) {
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{
		ClaimedBy:     "test-dispatcher",
		LeaseInterval: "00:01:00",
		BatchLimit:    5,
	})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	if _, err := d.Tick(context.Background()); err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	got := store.claimCalls[0]
	if got.ClaimedBy != "test-dispatcher" {
		t.Fatalf("claimedBy = %q, want test-dispatcher", got.ClaimedBy)
	}
	if got.LeaseInterval != "00:01:00" {
		t.Fatalf("leaseInterval = %q, want 00:01:00", got.LeaseInterval)
	}
	if got.Limit != 5 {
		t.Fatalf("limit = %d, want 5", got.Limit)
	}
	if d.ClaimedBy() != "test-dispatcher" {
		t.Fatalf("ClaimedBy() = %q, want test-dispatcher", d.ClaimedBy())
	}
}

func TestWakeupDispatcherTickFillsDefaultsForZeroConfig(t *testing.T) {
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	if _, err := d.Tick(context.Background()); err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	got := store.claimCalls[0]
	if got.Limit != defaultWakeupClaimBatchLimit {
		t.Fatalf("default limit = %d, want %d", got.Limit, defaultWakeupClaimBatchLimit)
	}
	if got.LeaseInterval != defaultWakeupLeaseInterval {
		t.Fatalf("default leaseInterval = %q, want %q", got.LeaseInterval, defaultWakeupLeaseInterval)
	}
	if !strings.HasPrefix(got.ClaimedBy, "mcp-orch-dispatcher-") {
		t.Fatalf("default claimedBy = %q, want mcp-orch-dispatcher-N prefix", got.ClaimedBy)
	}
}

func TestWakeupDispatcher_RouterTerminalFailureLifecycleHooks(t *testing.T) {
	events := []string{}
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:            55,
			DagKey:        "dag-1",
			NodeKey:       "auto1",
			RunID:         int64Ptr(7001),
			ClaimedBy:     "worker-a",
			AttemptCount:  1,
			PromptPayload: testRawConfig(t, `{}`),
		}},
		nodesReply: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "auto1",
			NodeType: "automation",
			Status:   string(nodeexec.NodeStatusReady),
			Config:   testRawConfig(t, `{"exec":{"command_ref":"missing-runner"}}`),
		}},
	}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-a"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	autoExec := nodeexec.NewAutomationExecutor(nil, nil, nodeexec.WithAutomationHooks(recordingLifecycleOutcomeHooks(&events)))
	router := NewNodeExecutorRouter(store, nil, autoExec, nil, nil, nil)
	d.WithNodeRouter(router)

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("FailWakeup calls = %d, want 1", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("FailNodeAndCancelDownstream calls = %d, want 1", len(store.failNodeCalls))
	}
	want := []string{
		"before_execute:auto1::",
		"after_execute:auto1:failed:validation",
		"on_state_change:auto1:failed:validation",
		"on_failure:auto1:failed:validation",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestWakeupDispatcher_RouterFrameworkErrorRetriesWakeup(t *testing.T) {
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:           58,
			DagKey:       "dag-1",
			NodeKey:      "n1",
			RunID:        int64Ptr(7003),
			ClaimedBy:    "worker-a",
			AttemptCount: 1,
		}},
		nodesReply: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "agent",
			Status:   string(nodeexec.NodeStatusReady),
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
		}},
		runningErr: errors.New("running status write failed"),
	}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-a"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	agentExec := newTestAgentExecutor(&stubAgentLauncher{threadID: "thread-launched"})
	d.WithNodeRouter(NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("RetryWakeup calls = %d, want 1 for framework error", len(store.retryCalls))
	}
	if len(store.failCalls) != 0 {
		t.Fatalf("FailWakeup calls = %d, want 0 for transient framework error", len(store.failCalls))
	}
	if len(store.markSentCalls) != 0 {
		t.Fatalf("MarkWakeupSent calls = %d, want 0 when framework error is surfaced", len(store.markSentCalls))
	}
	if !strings.Contains(store.retryCalls[0].LastError, "ready->running write failed") {
		t.Fatalf("RetryWakeup LastError = %q, want ready->running write failed", store.retryCalls[0].LastError)
	}
}

func TestWakeupDispatcher_RetryExhaustedLifecycleHooksKeepFailureClass(t *testing.T) {
	events := []string{}
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:           56,
			DagKey:       "dag-1",
			NodeKey:      "n1",
			RunID:        int64Ptr(7002),
			ClaimedBy:    "worker-a",
			AttemptCount: 1,
		}},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-1",
			Metadata: testRawConfig(t, `{"schedule":{"default_retry":0}}`),
		},
		nodesReply: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "agent",
			Status:   string(nodeexec.NodeStatusReady),
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
		}},
	}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-a"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	launcher := &stubAgentLauncher{err: errors.New("context_length_exceeded")}
	agentExec := newTestAgentExecutor(launcher, nodeexec.WithHooks(recordingLifecycleOutcomeHooks(&events)))
	d.WithNodeRouter(NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("FailNodeAndCancelDownstream calls = %d, want 1", len(store.failNodeCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("FailWakeup calls = %d, want 1", len(store.failCalls))
	}
	if store.failNodeCalls[0].Reason != store.failCalls[0].LastError {
		t.Fatalf("FailNode reason = %q, want same as FailWakeup last_error %q", store.failNodeCalls[0].Reason, store.failCalls[0].LastError)
	}
	want := []string{
		"before_execute:n1::",
		"after_execute:n1:failed:quota",
		"on_state_change:n1:failed:quota",
		"on_failure:n1:failed:quota",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestWakeupDispatcher_SQLRetryHardCapInvokesLifecycleHooks(t *testing.T) {
	events := []string{}
	now := time.Date(2026, 5, 14, 15, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(57, "dag-1", "n1", "agent-W", 8, now)},
		retryRows:  -1,
		nodesReply: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "agent",
			Status:   string(nodeexec.NodeStatusReady),
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
		}},
	}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-a"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	launcher := &stubAgentLauncher{err: errors.New("connection refused")}
	agentExec := newTestAgentExecutor(launcher, nodeexec.WithHooks(recordingLifecycleOutcomeHooks(&events)))
	d.WithNodeRouter(NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("RetryWakeup calls = %d, want 1 before hard-cap fallback", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("FailWakeup calls = %d, want 1", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("FailNodeAndCancelDownstream calls = %d, want 1", len(store.failNodeCalls))
	}
	if !strings.Contains(store.failNodeCalls[0].Reason, "retry attempts exhausted") {
		t.Fatalf("FailNode reason = %q, want exhausted prefix", store.failNodeCalls[0].Reason)
	}
	want := []string{
		"before_execute:n1::",
		"after_execute:n1:failed:transient",
		"on_state_change:n1:failed:transient",
		"on_failure:n1:failed:transient",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestWakeupDispatcherTickReturnsClaimCountAndPreservesFence(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	leaseAt := now.Add(30 * time.Second)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{
			{
				ID:             1,
				DagKey:         "dag-a",
				NodeKey:        "node-a",
				WakeupKind:     "start",
				TargetAgentID:  "agent-1",
				Status:         "dispatching",
				AttemptCount:   1,
				ClaimedAt:      &now,
				ClaimedBy:      "mcp-orch-dispatcher-7",
				LeaseExpiresAt: &leaseAt,
			},
			{
				ID:             2,
				DagKey:         "dag-a",
				NodeKey:        "node-b",
				WakeupKind:     "start",
				TargetAgentID:  "agent-2",
				Status:         "dispatching",
				AttemptCount:   2,
				ClaimedAt:      &now,
				ClaimedBy:      "mcp-orch-dispatcher-7",
				LeaseExpiresAt: &leaseAt,
			},
		},
	}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{ClaimedBy: "mcp-orch-dispatcher-7"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	if n != 2 {
		t.Fatalf("Tick claimed = %d, want 2", n)
	}
	if got := store.claimCalls[0].ClaimedBy; got != "mcp-orch-dispatcher-7" {
		t.Fatalf("claimedBy fence not propagated: got %q", got)
	}
}

func TestWakeupDispatcherTickWrapsStoreError(t *testing.T) {
	store := &dispatcherStubStore{claimErr: errors.New("boom")}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.Tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want wrapped 'boom'", err)
	}
	if n != 0 {
		t.Fatalf("claimed = %d, want 0 on error", n)
	}
}

func TestWakeupDispatcherTickHandlesNilContextSafely(t *testing.T) {
	// dispatcher 不应�?panic �?caller �?nil ctx——内部回退�?Background�?
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	if _, err := d.Tick(nil); err != nil { //nolint:staticcheck // intentionally testing nil-ctx fallback
		t.Fatalf("Tick(nil) err = %v, want graceful fallback", err)
	}
}

func TestWakeupDispatcherConfigOrDefaultsKeepsExplicitValues(t *testing.T) {
	cfg := WakeupDispatcherConfig{
		ClaimedBy:     "explicit-1",
		LeaseInterval: "00:00:45",
		BatchLimit:    7,
	}.ConfigOrDefaults()
	if cfg.ClaimedBy != "explicit-1" || cfg.LeaseInterval != "00:00:45" || cfg.BatchLimit != 7 {
		t.Fatalf("ConfigOrDefaults overrode explicit values: %+v", cfg)
	}
}

// === Phase 3.2 tests · 主循�?+ launch + 状态推�?=====================

func makeClaimedWakeup(id int64, agentID string, attempt int32, now time.Time) taskdag.Wakeup {
	lease := now.Add(30 * time.Second)
	return taskdag.Wakeup{
		ID:             id,
		WakeupKind:     "start",
		TargetAgentID:  agentID,
		Status:         "dispatching",
		AttemptCount:   attempt,
		ClaimedAt:      &now,
		ClaimedBy:      "mcp-orch-dispatcher-test",
		LeaseExpiresAt: &lease,
	}
}
