package claudecli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func newSilentTurnTestSession(t *testing.T, tr *transport) (*session, chan dto.RawProviderEvent, chan turndto.TurnCompleted) {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	rawEvents := make(chan dto.RawProviderEvent, 4)
	turnCompleted := make(chan turndto.TurnCompleted, 4)
	cancelRaw := event.Subscribe(bus, func(ev dto.RawProviderEvent) { rawEvents <- ev })
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
		silentTurnIDs:   map[string]struct{}{},
	}, rawEvents, turnCompleted
}

func currentActiveTurnID(s *session) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return currentTurnID(s.activeTurn)
}

func assertNoEvent[T any](t *testing.T, ch <-chan T) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event: %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
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
	if s.isSilentTurn(dto.RawProviderEvent{Data: map[string]any{"turn_id": turnID}}) {
		t.Fatal("silent turn marker was not cleared")
	}
	assertNoEvent(t, rawEvents)
	assertNoEvent(t, turnCompleted)
}

func TestSendKeepaliveTimeout(t *testing.T) {
	tr := newInterruptTestTransport(t, "while :; do sleep 1; done")
	s := &session{transport: tr, silentTurnIDs: map[string]struct{}{}}
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
	if s.isSilentTurn(dto.RawProviderEvent{Data: map[string]any{"turn_id": turnID}}) {
		t.Fatal("silent turn marker was not cleared after timeout")
	}
	if tr.readyForSend() {
		t.Fatal("transport remained ready after keepalive timeout kill")
	}
}

func TestIsSilentTurnFiltering(t *testing.T) {
	s := &session{silentTurnIDs: map[string]struct{}{"silent-1": {}}}
	if !s.isSilentTurn(dto.RawProviderEvent{Data: map[string]any{"turn_id": "silent-1"}}) {
		t.Fatal("isSilentTurn() = false, want true for silent turn")
	}
	if s.isSilentTurn(dto.RawProviderEvent{Data: map[string]any{"turn_id": "normal-1"}}) {
		t.Fatal("isSilentTurn() = true, want false for non-silent turn")
	}
	if s.isSilentTurn(dto.RawProviderEvent{Data: map[string]any{"turn_id": ""}}) {
		t.Fatal("isSilentTurn() = true, want false for empty turn_id")
	}
}

func TestHandleReceiveExitSilentTurn(t *testing.T) {
	tr := &transport{}
	s, rawEvents, turnCompleted := newSilentTurnTestSession(t, tr)
	handle := newTurnHandle("keepalive-1", "keepalive-1")
	s.activeTurn = handle
	s.silentTurnIDs[handle.LocalID()] = struct{}{}
	s.handleReceiveExit(tr, io.EOF)
	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handleReceiveExit() did not finish silent handle")
	}
	if !errors.Is(handle.Err(), io.EOF) {
		t.Fatalf("handle.Err() = %v, want EOF", handle.Err())
	}
	if got := currentActiveTurnID(s); got != "" {
		t.Fatalf("activeTurn = %q, want cleared", got)
	}
	if s.isSilentTurn(dto.RawProviderEvent{Data: map[string]any{"turn_id": handle.LocalID()}}) {
		t.Fatal("silent turn marker was not cleared after receive exit")
	}
	assertNoEvent(t, rawEvents)
	assertNoEvent(t, turnCompleted)
}

func TestSendKeepaliveLockModel(t *testing.T) {
	writer := &blockingWriteCloser{started: make(chan struct{}), release: make(chan struct{}), writes: make(chan string, 1)}
	tr := &transport{stdin: writer, stderr: newLimitedBuffer(stderrLimitBytes), done: make(chan struct{})}
	s := &session{transport: tr, silentTurnIDs: map[string]struct{}{}}
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
