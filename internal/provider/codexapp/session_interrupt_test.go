package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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
