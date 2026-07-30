package claudecli

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kelindar/event"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestForceCompletePublishesInterruptedSystemTerminal(t *testing.T) {
	probe := newForceCompleteTerminalProbe(t)
	handle := newTurnHandle("local-1", "turn-1")
	s := &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		sessionID:       "thread-1",
		eventDispatcher: probe.dispatcher,
		activeTurn:      handle,
		suppressedTurns: map[string]struct{}{},
	}
	if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{}); err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}

	assertForceCompleteRawEvent(t, receiveForceCompleteEvent(t, probe.rawEvents, "RawProviderEvent").Event)
	assertForceCompleteTurnCompleted(t, receiveForceCompleteEvent(t, probe.completedEvents, "TurnCompleted"))
	assertForceCompleteTerminal(t, receiveForceCompleteEvent(t, probe.terminals, "TurnTerminalV2"))
}

type forceCompleteTerminalProbe struct {
	dispatcher      *unified.EventDispatcher
	rawEvents       <-chan dto.BusRawProviderEvent
	completedEvents <-chan turndto.TurnCompleted
	terminals       <-chan turndto.TurnTerminalV2
}

func newForceCompleteTerminalProbe(t *testing.T) forceCompleteTerminalProbe {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("close event dispatcher: %v", err)
		}
	})
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	rawEvents := make(chan dto.BusRawProviderEvent, 1)
	cancelRaw := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		rawEvents <- ev
	})
	t.Cleanup(cancelRaw)

	completedEvents := make(chan turndto.TurnCompleted, 1)
	cancelCompleted := event.Subscribe(bus, func(ev turndto.TurnCompleted) {
		completedEvents <- ev
	})
	t.Cleanup(cancelCompleted)

	terminals := make(chan turndto.TurnTerminalV2, 1)
	for _, cancel := range eventsurface.Bind(bus, nil, func(method string, payload any) {
		if method != eventsurface.MethodTurnTerminal {
			return
		}
		terminal, ok := payload.(turndto.TurnTerminalV2)
		if !ok {
			t.Errorf("turn terminal payload = %T, want turn.TurnTerminalV2", payload)
			return
		}
		terminals <- terminal
	}) {
		t.Cleanup(cancel)
	}
	return forceCompleteTerminalProbe{
		dispatcher: dispatcher, rawEvents: rawEvents, completedEvents: completedEvents, terminals: terminals,
	}
}

func assertForceCompleteRawEvent(t *testing.T, raw dto.RawProviderEvent) {
	t.Helper()
	if raw.EventType != "turn:interrupted" {
		t.Fatalf("raw ForceComplete event type = %q, want turn:interrupted", raw.EventType)
	}
}

func assertForceCompleteTurnCompleted(t *testing.T, completed turndto.TurnCompleted) {
	t.Helper()
	if completed.Success || completed.Status != "interrupted" || completed.Reason != "system" {
		t.Fatalf("TurnCompleted = %#v, want interrupted/non-success/system", completed)
	}
	if completed.TerminationRequestID != "" || completed.Result != "" || completed.Summary != "" || completed.Message != "" {
		t.Fatalf("TurnCompleted leaked fallback fields: %#v", completed)
	}
}

func assertForceCompleteTerminal(t *testing.T, terminal turndto.TurnTerminalV2) {
	t.Helper()
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		t.Fatalf("ValidateTurnTerminalV2() error = %v; payload=%#v", err, terminal)
	}
	if terminal.Outcome != "interrupted" || terminal.TerminationCause != "system" {
		t.Fatalf("terminal = %#v, want interrupted/system", terminal)
	}
	if terminal.TerminationRequestID != "" || terminal.PublicSummary != "" {
		t.Fatalf("terminal terminationRequestId/publicSummary = (%q, %q), want empty", terminal.TerminationRequestID, terminal.PublicSummary)
	}
	if terminal.PublicError == nil || terminal.PublicError.Code != "TURN_TERMINATED" {
		t.Fatalf("terminal publicError = %#v, want canonical TURN_TERMINATED", terminal.PublicError)
	}
}

func receiveForceCompleteEvent[T any](t *testing.T, events <-chan T, name string) T {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
		var zero T
		return zero
	}
}

func TestForceCompleteNilTransportCompletesActiveTurn(t *testing.T) {
	t.Parallel()

	handle := newTurnHandle("local-1", "turn-1")
	s := &session{
		threadID:   "thread-1",
		sessionID:  "thread-1",
		activeTurn: handle,
	}

	if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{}); err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}

	select {
	case <-handle.Done():
	default:
		t.Fatal("ForceComplete() did not finish active turn")
	}
	if err := handle.Err(); err != nil {
		t.Fatalf("handle.Err() = %v, want nil", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn != nil {
		t.Fatal("activeTurn was not cleared")
	}
	if _, ok := s.suppressedTurns["turn-1"]; !ok {
		t.Fatal("force-completed turn was not suppressed")
	}
}

func TestForceCompleteProviderIDMismatchDoesNotCompleteActiveTurn(t *testing.T) {
	t.Parallel()

	active := newTurnHandle("local-new", "turn-new")
	s := &session{
		threadID:        "thread-1",
		sessionID:       "thread-1",
		transport:       &transport{},
		activeTurn:      active,
		suppressedTurns: map[string]struct{}{},
	}

	if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ProviderID: "turn-old"}); err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}

	select {
	case <-active.Done():
		t.Fatal("ForceComplete() completed the current active turn for an old provider id")
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurn != active {
		t.Fatalf("activeTurn = %#v, want original active handle", s.activeTurn)
	}
	if _, ok := s.suppressedTurns["turn-old"]; ok {
		t.Fatal("mismatched provider id was suppressed")
	}
	if _, ok := s.suppressedTurns["turn-new"]; ok {
		t.Fatal("current active turn was suppressed")
	}
}

func TestForceCompleteProcessGoneCompletesActiveTurn(t *testing.T) {
	handle := newTurnHandle("local-1", "turn-1")
	s := &session{
		threadID:   "thread-1",
		sessionID:  "thread-1",
		transport:  &transport{cmd: &exec.Cmd{Process: &os.Process{Pid: 99999999}}, done: make(chan struct{})},
		activeTurn: handle,
	}

	if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{}); err != nil {
		t.Fatalf("ForceComplete() error = %v, want process-gone idempotent success", err)
	}

	select {
	case <-handle.Done():
	default:
		t.Fatal("ForceComplete() did not finish active turn after process-gone signal")
	}
	if err := handle.Err(); err != nil {
		t.Fatalf("handle.Err() = %v, want nil", err)
	}
}

func TestForceCompleteLateResultCannotFinishReplacementTurn(t *testing.T) {
	s, old, next, launched := newForceCompleteReplacementRaceSession(t)
	defer old.finish()
	defer next.finish()

	forceCompleteOldTurn(t, s)
	newHandle := startReplacementTurn(t, s, launched)
	emitLateOldResult(t, old)
	assertTurnStaysOpen(t, newHandle)
}

func TestTerminalResultLateDuplicateCannotFinishReplacementTurn(t *testing.T) {
	s, old, next, launched := newForceCompleteReplacementRaceSession(t)
	defer old.finish()
	defer next.finish()

	oldHandle, err := s.StartTurn(context.Background(), turnRequest("claude-old"))
	if err != nil {
		t.Fatalf("first StartTurn() error = %v", err)
	}
	emitLateOldResult(t, old)
	select {
	case <-oldHandle.Done():
	case <-time.After(time.Second):
		t.Fatal("terminal result did not finish the old turn")
	}
	s.mu.Lock()
	fenced := s.fencedTransport == old.tr
	s.mu.Unlock()
	if !fenced {
		t.Fatal("normal terminal did not fence its transport")
	}

	newHandle := startReplacementTurn(t, s, launched)
	emitLateOldResult(t, old)
	emitLateOldResult(t, old)
	assertTurnStaysOpen(t, newHandle)
}

func newForceCompleteReplacementRaceSession(t *testing.T) (*session, *scriptedTransport, *scriptedTransport, <-chan struct{}) {
	t.Helper()
	old := newScriptedTransport()
	next := newScriptedTransport()
	launched := make(chan struct{}, 1)
	initLine := []byte(marshalSystemInit(t, "11111111-2222-3333-4444-555555555555") + "\n")
	launchFn := overrideLaunchCLI(t, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		launched <- struct{}{}
		newTestGoroutineGroup(t).Go(func() {
			_, _ = next.stdout.Write(initLine)
		})
		return next.tr, nil, nil
	})
	ready := make(chan struct{})
	close(ready)
	s := &session{
		threadID:        "thread-1",
		sessionID:       "thread-1",
		threadReady:     ready,
		transport:       old.tr,
		model:           "claude-old",
		transportModel:  "claude-old",
		config:          cliLaunchConfig{PromptSnapshot: validResumePromptSnapshotForTest()},
		launchCLI:       launchFn,
		suppressedTurns: map[string]struct{}{},
	}
	old.tr.signal = func(processSig) error { return nil }
	s.startReadLoop(old.tr)
	return s, old, next, launched
}

func forceCompleteOldTurn(t *testing.T, s *session) {
	t.Helper()
	oldHandle, err := s.StartTurn(context.Background(), turnRequest("claude-old"))
	if err != nil {
		t.Fatalf("first StartTurn() error = %v", err)
	}
	if err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{}); err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}
	select {
	case <-oldHandle.Done():
	case <-time.After(time.Second):
		t.Fatal("ForceComplete() did not finish the old turn")
	}
}

func startReplacementTurn(t *testing.T, s *session, launched <-chan struct{}) interface {
	Done() <-chan struct{}
	Err() error
} {
	t.Helper()
	handle, err := s.StartTurn(context.Background(), turnRequest("claude-old"))
	if err != nil {
		t.Fatalf("replacement StartTurn() error = %v", err)
	}
	select {
	case <-launched:
	case <-time.After(time.Second):
		t.Fatal("replacement turn reused the fenced transport")
	}
	return handle
}

func emitLateOldResult(t *testing.T, old *scriptedTransport) {
	t.Helper()
	if _, err := old.stdout.Write([]byte(`{"type":"result","subtype":"success","result":"late old result"}` + "\n")); err != nil {
		t.Fatalf("write late old result: %v", err)
	}
}

func assertTurnStaysOpen(t *testing.T, handle interface {
	Done() <-chan struct{}
	Err() error
}) {
	t.Helper()
	select {
	case <-handle.Done():
		t.Fatalf("late old result finished replacement turn: %v", handle.Err())
	case <-time.After(150 * time.Millisecond):
	}
}
