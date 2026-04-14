package claudecli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func newSilentTurnTestSession(t *testing.T, tr *transport) (*session, chan dto.BusRawProviderEvent, chan turndto.TurnCompleted) {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	rawEvents := make(chan dto.BusRawProviderEvent, 4)
	turnCompleted := make(chan turndto.TurnCompleted, 4)
	cancelRaw := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) { rawEvents <- ev })
	cancelCompleted := event.Subscribe(bus, func(ev turndto.TurnCompleted) { turnCompleted <- ev })
	t.Cleanup(cancelRaw)
	t.Cleanup(cancelCompleted)
	return &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		publicThreadID:  "thread-public",
		sessionID:       "thread-1",
		transport:       tr,
		eventDispatcher: dispatcher,
	}, rawEvents, turnCompleted
}

func currentActiveTurnID(s *session) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return currentTurnID(s.activeTurn)
}

func TestSendKeepaliveNormalFlow(t *testing.T) {
	stdin := &recordingWriteCloser{writes: make(chan string, 1)}
	tr := &transport{stdin: stdin, stderr: newLimitedBuffer(stderrLimitBytes), done: make(chan struct{})}
	s, rawEvents, turnCompleted := newSilentTurnTestSession(t, tr)
	errCh := make(chan error, 1)
	go func() { errCh <- s.SendKeepalive(context.Background()) }()
	select {
	case write := <-stdin.writes:
		if !strings.Contains(write, "CACHE-KEEPALIVE") {
			t.Fatalf("SendKeepalive() payload = %q, want keepalive prompt", write)
		}
	case <-time.After(time.Second):
		t.Fatal("SendKeepalive() did not write payload")
	}
	turnID := currentActiveTurnID(s)
	if turnID == "" {
		t.Fatal("SendKeepalive() did not create active silent turn")
	}
	s.applyRaw(tr, dto.RawProviderEvent{EventType: "turn:complete", Data: map[string]any{"turn_id": turnID, "success": true}})
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("SendKeepalive() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SendKeepalive() did not finish after turn:complete")
	}
	if got := currentActiveTurnID(s); got != "" {
		t.Fatalf("activeTurn = %q, want cleared", got)
	}
	select {
	case raw := <-rawEvents:
		if raw.Event.EventType != "turn:complete" {
			t.Fatalf("raw.EventType = %q, want turn:complete", raw.Event.EventType)
		}
		if got := dataString(raw.Event.Data, "turn_id"); got != turnID {
			t.Fatalf("raw turn_id = %q, want %q", got, turnID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected raw keepalive completion event")
	}
	select {
	case completed := <-turnCompleted:
		if !completed.Success {
			t.Fatalf("turnCompleted.Success = false, want true (error=%q)", completed.Error)
		}
		if completed.TurnID != turnID {
			t.Fatalf("turnCompleted.TurnID = %q, want %q", completed.TurnID, turnID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected translated keepalive completion event")
	}
}

func TestSendKeepaliveTimeout(t *testing.T) {
	tr := newInterruptTestTransport(t, "while :; do sleep 1; done")
	s := &session{transport: tr}
	s.mu.Lock()
	_, turnID, handle, err := s.prepareSilentTurnLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("prepareSilentTurnLocked() error = %v", err)
	}
	if err := s.timeoutSilentTurn(turnID); err == nil || !strings.Contains(err.Error(), "keepalive timeout") {
		t.Fatalf("timeoutSilentTurn() error = %v, want keepalive timeout", err)
	}
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("timeoutSilentTurn() did not finish active handle")
	}
	if got := handle.Err(); got == nil || !strings.Contains(got.Error(), "killed transport") {
		t.Fatalf("handle.Err() = %v, want killed transport error", got)
	}
	if got := currentActiveTurnID(s); got != "" {
		t.Fatalf("activeTurn = %q, want cleared", got)
	}
	if tr.readyForSend() {
		t.Fatal("transport remained ready after keepalive timeout kill")
	}
}

func TestSendKeepaliveLockModel(t *testing.T) {
	writer := &blockingWriteCloser{started: make(chan struct{}), release: make(chan struct{}), writes: make(chan string, 1)}
	tr := &transport{stdin: writer, stderr: newLimitedBuffer(stderrLimitBytes), done: make(chan struct{})}
	s := &session{transport: tr}
	errCh := make(chan error, 1)
	go func() { errCh <- s.SendKeepalive(context.Background()) }()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("SendKeepalive() did not enter transport.Send")
	}
	lockAcquired := make(chan struct{})
	go func() {
		s.mu.Lock()
		_ = s.activeTurn
		s.mu.Unlock()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
		t.Fatal("session mutex unlocked before SendKeepalive transport.Send completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(writer.release)
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("session mutex remained locked after SendKeepalive transport.Send completed")
	}
	turnID := currentActiveTurnID(s)
	if turnID == "" {
		t.Fatal("SendKeepalive() did not preserve active silent turn after send")
	}
	s.applyRaw(tr, dto.RawProviderEvent{EventType: "turn:complete", Data: map[string]any{"turn_id": turnID, "success": true}})
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("SendKeepalive() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SendKeepalive() did not finish after write release")
	}
}
