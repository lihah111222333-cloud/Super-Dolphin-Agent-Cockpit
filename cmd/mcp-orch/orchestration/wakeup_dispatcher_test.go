package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

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
	store := &dispatcherStubStore{} // claimReply 默认 nil，与空 slice 一样表示没有到期 wakeup。
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
			Config:   testRawConfig(t, `{"exec":{"command_ref":"missing-runner","cwd":"/tmp","workspace_roots":["/tmp"]}}`),
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

func TestWakeupDispatcherSkipsSideEffectForReclaimedTerminalNode(t *testing.T) {
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	leaseAt := now.Add(30 * time.Second)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:             66,
			DagKey:         "dag-1",
			NodeKey:        "auto-done",
			RunID:          int64Ptr(7004),
			ClaimedBy:      "worker-b",
			ClaimedAt:      &now,
			LeaseExpiresAt: &leaseAt,
			AttemptCount:   2,
		}},
		nodesReply: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "auto-done",
			NodeType: "automation",
			Status:   string(nodeexec.NodeStatusDone),
			Config:   testRawConfig(t, `{"exec":{"command_ref":"already-ran"}}`),
		}},
	}
	getter := terminalWakeupAutomationGetter{}
	runner := &terminalWakeupAutomationRunner{}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-b"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	d.WithNodeRouter(NewNodeExecutorRouter(store, nil, nodeexec.NewAutomationExecutor(getter, runner), nil, nil, nil))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if runner.calls != 0 {
		t.Fatalf("automation side effects = %d, want 0 for terminal reclaimed node", runner.calls)
	}
	if len(store.completeCalls) != 0 {
		t.Fatalf("CompleteNodeAndScheduleDownstream calls = %d, want 0 for terminal reclaimed node", len(store.completeCalls))
	}
	if len(store.markSentCalls) != 1 {
		t.Fatalf("MarkWakeupSent calls = %d, want 1 to close reclaimed terminal wakeup", len(store.markSentCalls))
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

// TestWakeupLeaseExpiryDoesNotDuplicateChildLaunch verifies router writes carry the claimed wakeup lease fence.
func TestWakeupLeaseExpiryDoesNotDuplicateChildLaunch(t *testing.T) {
	now := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	leaseAt := now.Add(30 * time.Second)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:             61,
			DagKey:         "dag-1",
			NodeKey:        "n1",
			RunID:          int64Ptr(7006),
			ClaimedAt:      &now,
			ClaimedBy:      "worker-a",
			LeaseExpiresAt: &leaseAt,
			AttemptCount:   4,
		}},
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
	launcher := &stubAgentLauncher{threadID: "thread-fenced"}
	agentExec := newTestAgentExecutor(launcher)
	d.WithNodeRouter(NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("launcher calls = %d, want exactly one child launch", len(launcher.calls))
	}
	if len(store.runningCalls) != 1 {
		t.Fatalf("running calls = %d, want one fenced ready->running write", len(store.runningCalls))
	}
	assertRunningWakeupFence(t, store.runningCalls[0].WakeupFence, now, leaseAt)
}

func assertRunningWakeupFence(t *testing.T, fence taskdag.WakeupFence, claimedAt, leaseAt time.Time) {
	t.Helper()
	if fence.WakeupID != 61 {
		t.Fatalf("WakeupID = %d, want 61", fence.WakeupID)
	}
	if fence.WakeupAttempt != 4 {
		t.Fatalf("WakeupAttempt = %d, want 4", fence.WakeupAttempt)
	}
	if fence.ClaimedBy != "worker-a" {
		t.Fatalf("ClaimedBy = %q, want worker-a", fence.ClaimedBy)
	}
	if !fence.ClaimedAt.Equal(claimedAt) {
		t.Fatalf("ClaimedAt = %s, want %s", fence.ClaimedAt, claimedAt)
	}
	if !fence.LeaseExpiresAt.Equal(leaseAt) {
		t.Fatalf("LeaseExpiresAt = %s, want %s", fence.LeaseExpiresAt, leaseAt)
	}
}

func TestWakeupDispatcherRetryWakeupPassesConfiguredMaxAttempts(t *testing.T) {
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:            59,
			WakeupKind:    "start",
			TargetAgentID: "agent-transient",
			ClaimedBy:     "worker-a",
			AttemptCount:  1,
		}},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	d, err := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{
		ClaimedBy:        "worker-a",
		MaxRetryAttempts: 3,
	})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("RetryWakeup calls = %d, want 1", len(store.retryCalls))
	}
	if got := store.retryCalls[0].MaxAttempts; got != 3 {
		t.Fatalf("RetryWakeup MaxAttempts = %d, want 3", got)
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
		dagReply: &taskdag.DAG{
			DagKey:   "dag-1",
			Metadata: dagDefaultRetryMetadata(t, 10, false),
		},
		retryRows: -1,
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

type terminalWakeupAutomationGetter struct{}

func (terminalWakeupAutomationGetter) GetCommandCard(context.Context, string) (nodeexec.AutomationCommandCard, error) {
	return nodeexec.AutomationCommandCard{CardKey: "already-ran", CommandTemplate: "printf duplicate", RiskLevel: "high", Enabled: true}, nil
}

type terminalWakeupAutomationRunner struct {
	calls int
}

func (r *terminalWakeupAutomationRunner) RunCommandCard(context.Context, nodeexec.AutomationCommandCard, json.RawMessage, ...nodeexec.AutomationCommandRunOptions) (nodeexec.AutomationCommandResult, error) {
	r.calls++
	return nodeexec.AutomationCommandResult{CardKey: "already-ran", ExitCode: 0, Stdout: "duplicate"}, nil
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
	// dispatcher 不应因调用方传 nil ctx 而 panic；内部会改用 Background。
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	var nilCtx context.Context
	if _, err := d.Tick(nilCtx); err != nil {
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

// 以下 helper 服务主循环、launch 和状态推进相关测试。

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
