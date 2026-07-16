package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestSessionInterruptRequiresActiveTurnIDBeforeTransport(t *testing.T) {
	s := &session{}
	s.setThreadID("thread-1")

	err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"})
	if err == nil || !strings.Contains(err.Error(), "active turn id is required for interrupt") {
		t.Fatalf("Interrupt() error = %v, want active turn id required", err)
	}
}

func TestSessionInterruptSendsRequestTurnID(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.mu.Lock()
	s.activeTurnID = "turn-req"
	s.mu.Unlock()

	err := s.Interrupt(context.Background(), dto.InterruptRequest{
		ThreadID: " thread-1 ",
		TurnID:   " turn-req ",
		Source:   "ui_stop",
	})
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	params := receiveInterruptParams(t, paramsCh)
	if params["turnId"] != "turn-req" {
		t.Fatalf("interrupt turnId = %#v, want turn-req; params=%#v", params["turnId"], params)
	}
}

func TestSessionInterruptTargetChangedSkipsTransport(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.mu.Lock()
	s.activeTurnID = "turn-2"
	s.mu.Unlock()

	err := s.Interrupt(context.Background(), dto.InterruptRequest{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Source:   "ui_stop",
	})
	if !errors.Is(err, contract.ErrInterruptTargetChanged) {
		t.Fatalf("Interrupt() error = %v, want interrupt target changed", err)
	}
	select {
	case params := <-paramsCh:
		t.Fatalf("target-changed interrupt reached transport with params %#v", params)
	default:
	}
}

func TestSessionInterruptFallsBackToActiveTurnID(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.mu.Lock()
	s.activeTurnID = " active-turn "
	s.mu.Unlock()

	err := s.Interrupt(context.Background(), dto.InterruptRequest{
		ThreadID: "thread-1",
		Source:   "ui_stop",
	})
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	params := receiveInterruptParams(t, paramsCh)
	if params["turnId"] != "active-turn" {
		t.Fatalf("interrupt fallback turnId = %#v, want active-turn; params=%#v", params["turnId"], params)
	}
}

func TestSessionInterruptTargetChangesBeforeWireWrite(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.recovery = nil
	s.mu.Lock()
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	s.transport.writeMu.Lock()
	writeLocked := true
	t.Cleanup(func() {
		if writeLocked {
			s.transport.writeMu.Unlock()
		}
	})
	baseline := s.transport.nextID.Load()
	errCh := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), pkglogger.Get(), "codexapp.test.interrupt-target-change", func(context.Context) {
		errCh <- s.Interrupt(context.Background(), dto.InterruptRequest{ThreadID: "thread-1", TurnID: "turn-1", Source: "ui_stop"})
	})
	waitForInterruptCallRegistration(t, s.transport, baseline)

	s.mu.Lock()
	s.activeTurnID = "turn-2"
	s.mu.Unlock()
	s.transport.writeMu.Unlock()
	writeLocked = false

	if err := <-errCh; !errors.Is(err, contract.ErrInterruptTargetChanged) {
		t.Fatalf("Interrupt() error = %v, want interrupt target changed", err)
	}
	select {
	case params := <-paramsCh:
		t.Fatalf("target-changed interrupt reached transport with params %#v", params)
	default:
	}
	if got := s.recoveryCount.Load(); got != 0 {
		t.Fatalf("target-changed interrupt recoveryCount = %d, want 0", got)
	}
}

func waitForInterruptCallRegistration(t *testing.T, tr *transport, baseline int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for tr.nextID.Load() == baseline {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for interrupt call registration")
		}
		time.Sleep(time.Millisecond)
	}
}

func newInterruptTestSession(t *testing.T, paramsCh chan<- map[string]any) *session {
	t.Helper()
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if strings.TrimSpace(msg.Method) == "turn/interrupt" {
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("unmarshal interrupt params: %v", err)
			}
			paramsCh <- params
		}
		return mustJSON(map[string]any{"ok": true})
	})
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	return s
}

func receiveInterruptParams(t *testing.T, paramsCh <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case params := <-paramsCh:
		return params
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn/interrupt params")
		return nil
	}
}
