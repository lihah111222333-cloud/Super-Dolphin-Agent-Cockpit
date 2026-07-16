package turn

import (
	"context"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

type interruptClaimGateStore struct {
	turnTrackerStore
	switchTurn func()
	once       sync.Once
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

	_, accepted, err := svc.InterruptTurnForTarget(context.Background(), session, "user", "local-1")
	if err != nil || accepted || interruptCalls != 0 {
		t.Fatalf("InterruptTurnForTarget() accepted=%v error=%v calls=%d, want target changed with zero provider calls", accepted, err, interruptCalls)
	}
}

func TestInterruptProviderAcceptedRemainsAcceptedWhenTrackerAlreadyTerminal(t *testing.T) {
	handle := newStubTurnHandle("local-accepted", "provider-accepted")
	interruptCalls := 0
	var svc *service
	session := &stubSession{
		threadID: "thread-accepted",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			interruptCalls++
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

	_, accepted, err := svc.InterruptTurnForTarget(context.Background(), session, "user", "local-accepted")
	if err != nil || !accepted || interruptCalls != 1 {
		t.Fatalf("InterruptTurnForTarget() accepted=%v error=%v calls=%d, want provider acceptance preserved", accepted, err, interruptCalls)
	}
}
