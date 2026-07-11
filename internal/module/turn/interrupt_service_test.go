package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestInterruptTurnReturnsEnvelope(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-1", "provider-1")
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			time.AfterFunc(20*time.Millisecond, func() {
				handle.complete(errors.New("turn aborted"))
			})
			return nil
		},
	}

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-1",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	status, err := svc.InterruptTurn(context.Background(), session, "user")
	if err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	envelope := status.interruptEnvelope()
	if !envelope.confirmed || envelope.mode != "interrupt_confirmed" {
		t.Fatalf("interrupt envelope = %#v, want confirmed interrupt", envelope)
	}
	if !envelope.interruptSent || envelope.stateBefore != "running" || envelope.stateAfter != "idle" {
		t.Fatalf("interrupt envelope = %#v, want sent/running->idle", envelope)
	}
	if envelope.waitedMS <= 0 || !envelope.activeObserved {
		t.Fatalf("interrupt envelope = %#v, want waitedMS>0 and activeObserved", envelope)
	}
}

func TestInterruptTurnNoActiveReturnsEnvelope(t *testing.T) {
	t.Parallel()

	interruptCalls := 0
	session := &stubSession{
		threadID: "thread-2",
		interrupt: func(context.Context, dto.InterruptRequest) error {
			interruptCalls++
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})

	status, err := svc.InterruptTurn(context.Background(), session, "user")
	if err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	if interruptCalls != 0 {
		t.Fatalf("Interrupt() calls = %d, want 0 for no active turn", interruptCalls)
	}
	envelope := status.interruptEnvelope()
	if envelope.confirmed || envelope.mode != "no_active_turn" || envelope.interruptSent {
		t.Fatalf("interrupt envelope = %#v, want no_active_turn without send", envelope)
	}
	if envelope.stateBefore != "idle" || envelope.stateAfter != "idle" {
		t.Fatalf("interrupt envelope = %#v, want idle->idle", envelope)
	}
}

func TestInterruptTurnSettleTimeoutReturnsEnvelope(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-timeout", "provider-timeout")
	session := &stubSession{
		threadID: "thread-timeout",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error { return nil },
	}

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}).(*service)
	svc.interruptSettleTimeout = 25 * time.Millisecond
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-timeout",
		ThreadID: "thread-timeout",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	status, err := svc.InterruptTurn(context.Background(), session, "user")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InterruptTurn() error = %v, want deadline exceeded with timeout envelope", err)
	}
	envelope := status.interruptEnvelope()
	if !envelope.confirmed || envelope.mode != "interrupt_timeout" || !envelope.interruptSent {
		t.Fatalf("interrupt envelope = %#v, want confirmed timeout envelope", envelope)
	}
	if envelope.stateBefore != "running" || envelope.stateAfter != "running" {
		t.Fatalf("interrupt envelope = %#v, want running->running timeout state", envelope)
	}
	if envelope.waitedMS < 20 || !envelope.activeObserved {
		t.Fatalf("interrupt envelope = %#v, want waitedMS>=20 and activeObserved", envelope)
	}
}
