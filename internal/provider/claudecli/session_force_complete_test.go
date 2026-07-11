package claudecli

import (
	"context"
	"os"
	"os/exec"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

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
