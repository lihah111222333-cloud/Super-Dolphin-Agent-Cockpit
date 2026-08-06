package claudecli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func newSilentTurnTestSession(t *testing.T, tr *transport) (*session, chan dto.BusRawProviderEvent, chan turndto.TurnCompleted) {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher, testRuntimeHooks(t))
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
	errCh := startKeepaliveForTest(t, s)

	expectKeepaliveWrite(t, stdin)
	turnID := requireActiveSilentTurn(t, s)
	completeSilentTurn(t, s, tr, turnID)
	awaitKeepaliveSuccess(t, errCh, "after turn:complete")
	assertNoActiveSilentTurn(t, s)
	assertNoKeepaliveDispatch(t, rawEvents, turnCompleted)
}

func startKeepaliveForTest(t *testing.T, s *session) chan error {
	t.Helper()
	errCh := make(chan error, 1)
	finished := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		defer close(finished)
		errCh <- s.SendKeepalive(context.Background())
	})
	t.Cleanup(func() {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("keepalive goroutine did not stop")
		}
	})
	return errCh
}

func expectKeepaliveWrite(t *testing.T, stdin *recordingWriteCloser) {
	t.Helper()
	select {
	case write := <-stdin.writes:
		if !strings.Contains(write, "CACHE-KEEPALIVE") {
			t.Fatalf("SendKeepalive() payload = %q, want keepalive prompt", write)
		}
	case <-time.After(time.Second):
		t.Fatal("SendKeepalive() did not write payload")
	}
}

func requireActiveSilentTurn(t *testing.T, s *session) string {
	t.Helper()
	turnID := currentActiveTurnID(s)
	if turnID == "" {
		t.Fatal("SendKeepalive() did not create active silent turn")
	}
	return turnID
}

func completeSilentTurn(t *testing.T, s *session, tr *transport, turnID string) {
	t.Helper()
	s.applyRaw(tr, dto.RawProviderEvent{EventType: "turn:complete", Data: map[string]any{"turn_id": turnID, "success": true, "status": "completed"}})
}

func awaitKeepaliveSuccess(t *testing.T, errCh <-chan error, stage string) {
	t.Helper()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("SendKeepalive() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("SendKeepalive() did not finish %s", stage)
	}
}

func assertNoActiveSilentTurn(t *testing.T, s *session) {
	t.Helper()
	if got := currentActiveTurnID(s); got != "" {
		t.Fatalf("activeTurn = %q, want cleared", got)
	}
}

// assertNoKeepaliveDispatch verifies a silent (keepalive) turn finishes its
// turn handle without dispatching any event to the UI stream — neither the
// raw event nor the translated turnCompleted.
func assertNoKeepaliveDispatch(t *testing.T, rawEvents <-chan dto.BusRawProviderEvent, turnCompleted <-chan turndto.TurnCompleted) {
	t.Helper()
	select {
	case raw := <-rawEvents:
		t.Fatalf("keepalive turn must not dispatch raw events, got %q", raw.Event.EventType)
	case completed := <-turnCompleted:
		t.Fatalf("keepalive turn must not emit turnCompleted, got turn %q", completed.TurnID)
	case <-time.After(200 * time.Millisecond):
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
	errCh := startKeepaliveForTest(t, s)

	waitForBlockingWriteStart(t, writer)
	lockAcquired := assertSessionMutexHeldDuringSend(t, s)
	releaseWriterAndWaitForMutex(t, writer, lockAcquired)
	turnID := requireActiveSilentTurn(t, s)
	completeSilentTurn(t, s, tr, turnID)
	awaitKeepaliveSuccess(t, errCh, "after write release")
}

func waitForBlockingWriteStart(t *testing.T, writer *blockingWriteCloser) {
	t.Helper()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("SendKeepalive() did not enter transport.Send")
	}
}

func assertSessionMutexHeldDuringSend(t *testing.T, s *session) chan struct{} {
	t.Helper()
	lockAcquired := make(chan struct{})
	finished := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		defer close(finished)
		s.mu.Lock()
		_ = s.activeTurn
		s.mu.Unlock()
		close(lockAcquired)
	})
	t.Cleanup(func() {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("session mutex probe goroutine did not stop")
		}
	})
	select {
	case <-lockAcquired:
		t.Fatal("session mutex unlocked before SendKeepalive transport.Send completed")
	case <-time.After(100 * time.Millisecond):
	}
	return lockAcquired
}

func releaseWriterAndWaitForMutex(t *testing.T, writer *blockingWriteCloser, lockAcquired <-chan struct{}) {
	t.Helper()
	close(writer.release)
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("session mutex remained locked after SendKeepalive transport.Send completed")
	}
}

func TestApplyRawDropsKeepaliveTurnEvents(t *testing.T) {
	tr := &transport{done: make(chan struct{})}
	s, rawEvents, _ := newSilentTurnTestSession(t, tr)

	// A decoded keepalive-turn event (turn_id carries the keepalive prefix) is
	// processed for turn bookkeeping but must never reach the UI event stream.
	s.applyRaw(tr, dto.RawProviderEvent{
		EventType: "assistant:message_delta",
		Data:      map[string]any{"turn_id": keepaliveTurnIDPrefix + "ping_x", "delta": "OK"},
	})
	select {
	case raw := <-rawEvents:
		t.Fatalf("keepalive-turn event must not be dispatched, got %q", raw.Event.EventType)
	case <-time.After(150 * time.Millisecond):
	}

	// A normal-turn event must still pass through untouched.
	s.applyRaw(tr, dto.RawProviderEvent{
		EventType: "assistant:message_delta",
		Data:      map[string]any{"turn_id": "turn_real", "delta": "hi"},
	})
	select {
	case raw := <-rawEvents:
		if raw.Event.EventType != "assistant:message_delta" {
			t.Fatalf("raw.EventType = %q, want assistant:message_delta", raw.Event.EventType)
		}
	case <-time.After(time.Second):
		t.Fatal("normal-turn event was not dispatched")
	}
}

func TestKeepaliveTurnEndToEndSilent(t *testing.T) {
	stdin := &recordingWriteCloser{writes: make(chan string, 1)}
	tr := &transport{stdin: stdin, stderr: newLimitedBuffer(stderrLimitBytes), done: make(chan struct{})}
	s, rawEvents, turnCompleted := newSilentTurnTestSession(t, tr)

	// Start a real keepalive turn; prepareSilentTurnLocked assigns the
	// keepalive turn id that the whole decode pipeline derives from.
	errCh := startKeepaliveForTest(t, s)
	expectKeepaliveWrite(t, stdin)
	requireActiveSilentTurn(t, s)

	base := s.rawBase()
	if !strings.HasPrefix(base.TurnID, keepaliveTurnIDPrefix) {
		t.Fatalf("rawBase().TurnID = %q, want keepalive-prefixed id", base.TurnID)
	}

	// Drive realistic claude-CLI stream-json lines through the real
	// decodeClaudeLine + applyRaw path — the production read-loop pipeline.
	// The assistant line carries the hallucinated-transcript shape from the
	// reported bug (turn #1083 in thread 3b7abdf5).
	lines := [][]byte{
		[]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"OK\n\nuser实施第一阶段\n\nuser<system-reminder>\nPlan mode is active.\n</parameter>"}]}}`),
		[]byte(`{"type":"result","subtype":"success","result":"OK"}`),
	}
	for _, line := range lines {
		events, err := decodeClaudeLine(line, base)
		if err != nil {
			t.Fatalf("decodeClaudeLine(%s) error = %v", line, err)
		}
		for _, ev := range events {
			s.applyRaw(tr, ev)
		}
	}

	awaitKeepaliveSuccess(t, errCh, "after decoded result")
	assertNoActiveSilentTurn(t, s)
	assertNoKeepaliveDispatch(t, rawEvents, turnCompleted)
}
