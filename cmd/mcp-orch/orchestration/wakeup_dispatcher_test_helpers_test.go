package orchestration

import (
	"context"
	"sync"
	"testing"
	"time"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

type dispatcherStubStore struct {
	taskdag.Store

	claimCalls []taskdag.ClaimDueWakeupsInput
	claimReply []taskdag.Wakeup
	claimErr   error
	claimSeen  chan struct{}

	renewCalls []taskdag.RenewWakeupLeaseInput
	renewRows  int64
	renewErr   error

	markSentCalls   []taskdag.MarkWakeupSentInput
	markSentRows    int64
	markSentRowsSet bool
	markSentErr     error

	retryCalls []taskdag.RetryWakeupInput
	retryRows  int64
	retryErr   error

	failCalls   []taskdag.FailWakeupInput
	failRows    int64
	failRowsSet bool
	failErr     error

	atomicFailCalls []atomicWakeupNodeFailCall

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

// receiver alias 按 dispatcher fixture 角色拆分导出方法，同时保留旧字段和 composite literal 写法。
type dispatcherStubDAGReadFixture = dispatcherStubStore
type dispatcherStubNodeFlowFixture = dispatcherStubStore
type dispatcherStubWakeupFixture = dispatcherStubStore
type dispatcherStubAtomicFailFixture = dispatcherStubStore

type atomicWakeupNodeFailCall struct {
	Wakeup taskdag.FailWakeupInput
	Node   taskdag.FailNodeInput
}

func (s *dispatcherStubDAGReadFixture) GetDAG(_ context.Context, _ string) (*taskdag.DAG, error) {
	return s.dagReply, s.dagErr
}

func (s *dispatcherStubDAGReadFixture) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	return s.nodesReply, s.nodesErr
}

func (s *dispatcherStubDAGReadFixture) ListRunNodes(_ context.Context, _ string, runID int64) ([]taskdag.Node, error) {
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

func (s *dispatcherStubNodeFlowFixture) FailNodeAndCancelDownstream(_ context.Context, input taskdag.FailNodeInput) (*taskdag.FailNodeResult, error) {
	s.failNodeCalls = append(s.failNodeCalls, input)
	if s.failNodeErr != nil {
		return nil, s.failNodeErr
	}
	if s.failNodeReply != nil {
		return s.failNodeReply, nil
	}
	return &taskdag.FailNodeResult{}, nil
}

func (s *dispatcherStubAtomicFailFixture) FailWakeupAndFailNodeAndCancelDownstream(ctx context.Context, wakeup taskdag.FailWakeupInput, node taskdag.FailNodeInput) (int64, *taskdag.FailNodeResult, error) {
	s.atomicFailCalls = append(s.atomicFailCalls, atomicWakeupNodeFailCall{Wakeup: wakeup, Node: node})
	rows, err := s.FailWakeup(ctx, wakeup)
	if err != nil || rows == 0 {
		return rows, nil, err
	}
	res, err := s.FailNodeAndCancelDownstream(ctx, node)
	if err != nil {
		s.failCalls = s.failCalls[:len(s.failCalls)-1]
		return 0, nil, err
	}
	return rows, res, nil
}

func (s *dispatcherStubNodeFlowFixture) CompleteNodeAndScheduleDownstream(_ context.Context, input taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	s.completeCalls = append(s.completeCalls, input)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	if s.completeReply != nil {
		return s.completeReply, nil
	}
	return &taskdag.CompleteNodeWithDownstreamResult{}, nil
}

func (s *dispatcherStubNodeFlowFixture) PatchNodeConfigIfUnchanged(_ context.Context, input taskdag.NodeConfigPatchInput) (*taskdag.Node, error) {
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

func (s *dispatcherStubNodeFlowFixture) RetryWakeupWithNodeConfigPatch(ctx context.Context, input taskdag.RetryWakeupWithNodeConfigPatchInput) (int64, error) {
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

func (s *dispatcherStubNodeFlowFixture) UpdateRunningNodeStatus(_ context.Context, input taskdag.RunningNodeStatusUpdate) (*taskdag.Node, error) {
	s.runningCalls = append(s.runningCalls, input)
	if s.runningErr != nil {
		return nil, s.runningErr
	}
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, RunID: &input.RunID, Status: input.Status}, nil
}

func (s *dispatcherStubWakeupFixture) ClaimDueWakeups(_ context.Context, input taskdag.ClaimDueWakeupsInput) ([]taskdag.Wakeup, error) {
	s.claimCalls = append(s.claimCalls, input)
	if s.claimSeen != nil {
		select {
		case s.claimSeen <- struct{}{}:
		default:
		}
	}
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return s.claimReply, nil
}

func (s *dispatcherStubWakeupFixture) RenewWakeupLease(_ context.Context, input taskdag.RenewWakeupLeaseInput) (*taskdag.Wakeup, int64, error) {
	s.renewCalls = append(s.renewCalls, input)
	if s.renewErr != nil {
		return nil, 0, s.renewErr
	}
	if s.renewRows < 0 {
		return nil, 0, nil
	}
	for i := range s.claimReply {
		if s.claimReply[i].ID != input.ID {
			continue
		}
		renewed := s.claimReply[i]
		renewed.ClaimedAt = &input.ClaimedAt
		renewed.ClaimedBy = input.ClaimedBy
		renewed.LeaseExpiresAt = &input.LeaseExpiresAt
		if s.renewRows == 0 {
			return &renewed, 1, nil
		}
		return &renewed, s.renewRows, nil
	}
	return nil, 0, nil
}

func (s *dispatcherStubWakeupFixture) MarkWakeupSent(_ context.Context, input taskdag.MarkWakeupSentInput) (int64, error) {
	s.markSentCalls = append(s.markSentCalls, input)
	if s.markSentErr != nil {
		return 0, s.markSentErr
	}
	if s.markSentRowsSet {
		return s.markSentRows, nil
	}
	if s.markSentRows == 0 {
		return 1, nil
	}
	return s.markSentRows, nil
}

func (s *dispatcherStubWakeupFixture) RetryWakeup(_ context.Context, input taskdag.RetryWakeupInput) (int64, error) {
	s.retryCalls = append(s.retryCalls, input)
	if s.retryErr != nil {
		return 0, s.retryErr
	}
	if s.retryRows == 0 {
		return 1, nil
	}
	if s.retryRows < 0 {
		return 0, nil
	}
	return s.retryRows, nil
}

func (s *dispatcherStubWakeupFixture) FailWakeup(_ context.Context, input taskdag.FailWakeupInput) (int64, error) {
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

type dispatcherStubLauncher struct {
	calls []LaunchRequest
	errs  []error
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
