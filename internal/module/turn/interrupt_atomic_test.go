package turn

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type interruptClaimGateStore struct {
	turnTrackerStore
	switchTurn func()
	once       sync.Once
}

type interruptAttemptResult struct {
	accepted bool
	err      error
}

func (s *interruptClaimGateStore) switchActiveTurn() {
	s.once.Do(s.switchTurn)
}

func (s *interruptClaimGateStore) RangeView(fn func(string, *trackedTurn) bool) {
	s.turnTrackerStore.RangeView(fn)
	s.switchActiveTurn()
}

func (s *interruptClaimGateStore) MutateLatest(match func(*trackedTurn) bool, mutate func(*trackedTurn)) bool {
	s.switchActiveTurn()
	store, ok := s.turnTrackerStore.(interface {
		MutateLatest(func(*trackedTurn) bool, func(*trackedTurn)) bool
	})
	if !ok {
		return false
	}
	return store.MutateLatest(match, mutate)
}

func TestInterruptTargetCompareAndClaimIsAtomic(t *testing.T) {
	oldHandle := newStubTurnHandle("local-1", "provider-1")
	interruptCalls := 0
	session := &stubSession{
		threadID: "thread-atomic",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return oldHandle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			interruptCalls++
			oldHandle.complete(context.Canceled)
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}).(*service)
	if _, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID: "local-1", ThreadID: "thread-atomic", Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	gate := &interruptClaimGateStore{turnTrackerStore: svc.tracker.store}
	svc.tracker.store = gate
	gate.switchTurn = func() {
		svc.tracker.Complete("local-1", true, "")
		newHandle := newStubTurnHandle("local-2", "provider-2")
		svc.tracker.Start("local-2", "provider-2", "thread-atomic")
		svc.tracker.AttachHandle("local-2", newHandle)
		svc.tracker.Update("local-2", StateRunning)
	}

	_, accepted, err := svc.InterruptTurnForTarget(context.Background(), session, "user", "local-1", "request-1")
	if err != nil || accepted || interruptCalls != 0 {
		t.Fatalf("InterruptTurnForTarget() accepted=%v error=%v calls=%d, want target changed with zero provider calls", accepted, err, interruptCalls)
	}
}

func TestInterruptProviderAcceptedRemainsAcceptedWhenTrackerAlreadyTerminal(t *testing.T) {
	handle := newStubTurnHandle("local-accepted", "provider-accepted")
	interruptCalls := 0
	var interruptRequest dto.InterruptRequest
	var svc *service
	session := &stubSession{
		threadID: "thread-accepted",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(_ context.Context, req dto.InterruptRequest) error {
			interruptCalls++
			interruptRequest = req
			svc.tracker.Complete("local-accepted", true, "")
			handle.complete(nil)
			return nil
		},
	}
	svc = NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}).(*service)
	if _, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID: "local-accepted", ThreadID: "thread-accepted", Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	_, accepted, err := svc.InterruptTurnForTarget(context.Background(), session, "user", "local-accepted", "request-accepted")
	if err != nil || !accepted || interruptCalls != 1 {
		t.Fatalf("InterruptTurnForTarget() accepted=%v error=%v calls=%d, want provider acceptance preserved", accepted, err, interruptCalls)
	}
	if interruptRequest.TurnID != "provider-accepted" || interruptRequest.RequestID != "request-accepted" {
		t.Fatalf("provider interrupt request = %#v, want provider turn and accepted request id", interruptRequest)
	}
}

func TestConcurrentInterruptAfterProviderAcceptanceIsIdempotent(t *testing.T) {
	handle := newStubTurnHandle("local-idempotent", "provider-idempotent")
	providerCalls := atomic.Int32{}
	providerCalled := make(chan struct{}, 2)
	session := &stubSession{
		threadID: "thread-idempotent",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			providerCalls.Add(1)
			providerCalled <- struct{}{}
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}).(*service)
	if _, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID: "local-idempotent", ThreadID: "thread-idempotent", Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	firstResult := startInterruptAttempt(svc, session, "turn.test.first-interrupt", "local-idempotent")
	waitForProviderInterrupt(t, providerCalled)
	waitForInterruptingState(t, svc.tracker, "local-idempotent")

	secondResult := startInterruptAttempt(svc, session, "turn.test.second-interrupt", "local-idempotent")
	second, secondReady := waitForDuplicateInterruptDecision(t, providerCalled, secondResult)
	svc.tracker.Complete("local-idempotent", true, "")
	handle.complete(nil)

	first := <-firstResult
	if !secondReady {
		second = <-secondResult
	}
	assertIdempotentInterruptResults(t, first, second, providerCalls.Load())
}

func startInterruptAttempt(svc *service, session contract.Session, name, localID string) <-chan interruptAttemptResult {
	result := make(chan interruptAttemptResult, 1)
	runtimesafe.SafeGo(context.Background(), silentLogger(), name, func(context.Context) {
		_, accepted, err := svc.InterruptTurnForTarget(context.Background(), session, "user", localID, "request-idempotent")
		result <- interruptAttemptResult{accepted: accepted, err: err}
	})
	return result
}

func waitForProviderInterrupt(t *testing.T, called <-chan struct{}) {
	t.Helper()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider interrupt")
	}
}

func waitForDuplicateInterruptDecision(t *testing.T, called <-chan struct{}, result <-chan interruptAttemptResult) (interruptAttemptResult, bool) {
	t.Helper()
	select {
	case <-called:
		return interruptAttemptResult{}, false
	case attempt := <-result:
		return attempt, true
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for duplicate interrupt decision")
		return interruptAttemptResult{}, false
	}
}

func assertIdempotentInterruptResults(t *testing.T, first, second interruptAttemptResult, providerCalls int32) {
	t.Helper()
	if first.err != nil || second.err != nil {
		t.Fatalf("interrupt errors first=%v second=%v", first.err, second.err)
	}
	if !first.accepted || !second.accepted || providerCalls != 1 {
		t.Fatalf("interrupt results first=%+v second=%+v providerCalls=%d, want both accepted with one call", first, second, providerCalls)
	}
}

func waitForInterruptingState(t *testing.T, tracker *turnTracker, localID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		status, ok := tracker.Get(localID)
		if ok && status.State == string(StateInterrupting) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for interrupting state; status=%+v found=%v", status, ok)
		}
		time.Sleep(time.Millisecond)
	}
}
