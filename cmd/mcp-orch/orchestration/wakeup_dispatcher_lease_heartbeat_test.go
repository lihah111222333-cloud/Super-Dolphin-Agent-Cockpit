package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestWakeupDispatcherRenewsLeaseDuringLongAutomationAndMarksLatestFence(t *testing.T) {
	t.Parallel()

	store, dispatcher, runner := newHeartbeatDispatcherFixture(t, 0, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := runHeartbeatBatch(ctx, dispatcher)
	waitHeartbeatRunnerStarted(t, runner)
	if !store.waitForRenewCalls(3, 600*time.Millisecond) {
		runner.release()
		<-result
		t.Fatalf("RenewWakeupLease calls = %d, want at least 3 while route is blocked", store.renewCount())
	}
	runner.release()

	got := <-result
	if got.err != nil {
		t.Fatalf("ProcessBatch err = %v", got.err)
	}
	if got.handled != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", got.handled)
	}
	if len(store.markSentCalls) != 1 {
		t.Fatalf("MarkWakeupSent calls = %d, want 1", len(store.markSentCalls))
	}
	latest := store.latestWakeup()
	if gotFence := store.markSentCalls[0].LeaseExpiresAt; latest.LeaseExpiresAt == nil || !gotFence.Equal(*latest.LeaseExpiresAt) {
		t.Fatalf("MarkWakeupSent lease_expires_at = %s, want latest renewed fence %v", gotFence, latest.LeaseExpiresAt)
	}
}

func TestWakeupDispatcherRenewErrorCancelsRouteAndSkipsCommit(t *testing.T) {
	t.Parallel()

	renewErr := errors.New("renew lease unavailable")
	store, dispatcher, runner := newHeartbeatDispatcherFixture(t, 2, renewErr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result := runHeartbeatBatch(ctx, dispatcher)
	waitHeartbeatRunnerStarted(t, runner)

	select {
	case <-runner.canceled:
	case <-time.After(600 * time.Millisecond):
		runner.release()
		<-result
		t.Fatal("automation route was not canceled after wakeup lease renewal failed")
	}
	got := <-result
	if got.err != nil {
		t.Fatalf("ProcessBatch err = %v", got.err)
	}
	if got.handled != 0 {
		t.Fatalf("ProcessBatch handled = %d, want 0 after lease renewal failure", got.handled)
	}
	if len(store.markSentCalls) != 0 {
		t.Fatalf("MarkWakeupSent calls = %d, want 0 after lease renewal failure", len(store.markSentCalls))
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("RetryWakeup calls = %d, want 0 after lease renewal failure", len(store.retryCalls))
	}
	if len(store.failCalls) != 0 {
		t.Fatalf("FailWakeup calls = %d, want 0 after lease renewal failure", len(store.failCalls))
	}
	if len(store.completeCalls) != 0 {
		t.Fatalf("CompleteNodeAndScheduleDownstream calls = %d, want 0 after route cancellation", len(store.completeCalls))
	}
}

type heartbeatBatchResult struct {
	handled int
	err     error
}

func runHeartbeatBatch(ctx context.Context, dispatcher *WakeupDispatcher) <-chan heartbeatBatchResult {
	result := make(chan heartbeatBatchResult, 1)
	safego.Go(ctx, nil, "test.wakeupDispatcherLeaseHeartbeat", func(runCtx context.Context) {
		handled, err := dispatcher.ProcessBatch(runCtx)
		result <- heartbeatBatchResult{handled: handled, err: err}
	})
	return result
}

type heartbeatAutomationGetter struct{}

func (heartbeatAutomationGetter) GetCommandCard(context.Context, string) (nodeexec.AutomationCommandCard, error) {
	return nodeexec.AutomationCommandCard{
		CardKey:         "heartbeat-block",
		CommandTemplate: "heartbeat-block",
		Enabled:         true,
	}, nil
}

type heartbeatAutomationRunner struct {
	started     chan struct{}
	releaseCh   chan struct{}
	canceled    chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
	cancelOnce  sync.Once
}

func newHeartbeatAutomationRunner() *heartbeatAutomationRunner {
	return &heartbeatAutomationRunner{
		started:   make(chan struct{}),
		releaseCh: make(chan struct{}),
		canceled:  make(chan struct{}),
	}
}

func (r *heartbeatAutomationRunner) RunCommandCard(ctx context.Context, _ nodeexec.AutomationCommandCard, _ json.RawMessage, _ ...nodeexec.AutomationCommandRunOptions) (nodeexec.AutomationCommandResult, error) {
	r.startedOnce.Do(func() { close(r.started) })
	select {
	case <-r.releaseCh:
		return nodeexec.AutomationCommandResult{CardKey: "heartbeat-block", ExitCode: 0}, nil
	case <-ctx.Done():
		r.cancelOnce.Do(func() { close(r.canceled) })
		return nodeexec.AutomationCommandResult{}, ctx.Err()
	}
}

func (r *heartbeatAutomationRunner) release() {
	r.releaseOnce.Do(func() { close(r.releaseCh) })
}

type heartbeatDispatcherStore struct {
	*dispatcherStubStore

	renewMu      sync.Mutex
	latest       taskdag.Wakeup
	renewInputs  []taskdag.RenewWakeupLeaseInput
	renewErrAt   int
	renewErr     error
	renewSignals chan struct{}
}

func (s *heartbeatDispatcherStore) RenewWakeupLease(_ context.Context, input taskdag.RenewWakeupLeaseInput) (*taskdag.Wakeup, int64, error) {
	s.renewMu.Lock()
	defer s.renewMu.Unlock()

	s.renewInputs = append(s.renewInputs, input)
	select {
	case s.renewSignals <- struct{}{}:
	default:
	}
	if s.renewErrAt > 0 && len(s.renewInputs) == s.renewErrAt {
		return nil, 0, s.renewErr
	}
	if s.latest.LeaseExpiresAt == nil || !input.LeaseExpiresAt.Equal(*s.latest.LeaseExpiresAt) {
		return nil, 0, errors.New("renew used stale lease fence")
	}
	nextLease := input.LeaseExpiresAt.Add(time.Second)
	renewed := s.latest
	renewed.ClaimedAt = heartbeatTimePtr(input.ClaimedAt)
	renewed.ClaimedBy = input.ClaimedBy
	renewed.LeaseExpiresAt = &nextLease
	s.latest = renewed
	copy := renewed
	return &copy, 1, nil
}

func (s *heartbeatDispatcherStore) renewCount() int {
	s.renewMu.Lock()
	defer s.renewMu.Unlock()
	return len(s.renewInputs)
}

func (s *heartbeatDispatcherStore) latestWakeup() taskdag.Wakeup {
	s.renewMu.Lock()
	defer s.renewMu.Unlock()
	return s.latest
}

func (s *heartbeatDispatcherStore) waitForRenewCalls(want int, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if s.renewCount() >= want {
			return true
		}
		select {
		case <-s.renewSignals:
		case <-timer.C:
			return s.renewCount() >= want
		}
	}
}

func newHeartbeatDispatcherFixture(t *testing.T, renewErrAt int, renewErr error) (*heartbeatDispatcherStore, *WakeupDispatcher, *heartbeatAutomationRunner) {
	t.Helper()

	now := time.Now().UTC()
	leaseExpiresAt := now.Add(90 * time.Millisecond)
	runID := int64(7100)
	wakeup := taskdag.Wakeup{
		ID:             3700,
		DagKey:         "dag-heartbeat",
		NodeKey:        "automation-heartbeat",
		RunID:          &runID,
		Status:         "dispatching",
		AttemptCount:   1,
		ClaimedAt:      &now,
		ClaimedBy:      "dispatcher-heartbeat",
		LeaseExpiresAt: &leaseExpiresAt,
	}
	base := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{wakeup},
		dagReply: &taskdag.DAG{
			DagKey:   wakeup.DagKey,
			Metadata: json.RawMessage(`{}`),
		},
		nodesReply: []taskdag.Node{{
			DagKey:   wakeup.DagKey,
			NodeKey:  wakeup.NodeKey,
			RunID:    &runID,
			NodeType: "automation",
			Status:   string(nodeexec.NodeStatusReady),
			Config: testRawConfig(t,
				`{"exec":{"command_ref":"heartbeat-block","cwd":"/tmp","workspace_roots":["/tmp"]}}`,
			),
		}},
	}
	store := &heartbeatDispatcherStore{
		dispatcherStubStore: base,
		latest:              wakeup,
		renewErrAt:          renewErrAt,
		renewErr:            renewErr,
		renewSignals:        make(chan struct{}, 16),
	}
	dispatcher, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{
		ClaimedBy:     wakeup.ClaimedBy,
		LeaseInterval: "90ms",
	})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	runner := newHeartbeatAutomationRunner()
	autoExec := nodeexec.NewAutomationExecutor(heartbeatAutomationGetter{}, runner)
	dispatcher.WithNodeRouter(NewNodeExecutorRouter(store, nil, autoExec, nil, nil, nil))
	return store, dispatcher, runner
}

func waitHeartbeatRunnerStarted(t *testing.T, runner *heartbeatAutomationRunner) {
	t.Helper()
	select {
	case <-runner.started:
	case <-time.After(600 * time.Millisecond):
		t.Fatal("automation runner did not start")
	}
}

func heartbeatTimePtr(value time.Time) *time.Time {
	copy := value
	return &copy
}
