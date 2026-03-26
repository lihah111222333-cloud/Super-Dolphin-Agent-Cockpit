package claudecli

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/kelindar/event"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func TestBaseDataUsesPublicThreadIDAndSeparateSessionID(t *testing.T) {
	t.Parallel()

	got := baseData(rawBase{
		AgentID:  "agent-1",
		ThreadID: "thread-public",
		TurnID:   "turn-1",
	}, "session-123", "2026-03-26T00:00:00Z")

	if got["thread_id"] != "thread-public" {
		t.Fatalf("thread_id = %v, want thread-public", got["thread_id"])
	}
	if got["session_id"] != "session-123" {
		t.Fatalf("session_id = %v, want session-123", got["session_id"])
	}
}

func TestHandleReceiveExitEOFCompletesActiveTurn(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	got := make(chan turndto.TurnCompleted, 1)
	cancel := event.Subscribe(bus, func(ev turndto.TurnCompleted) {
		got <- ev
	})
	defer cancel()

	tr := &transport{}
	handle := newTurnHandle("local-1", "turn-1")
	s := &session{
		agentID:         "agent-1",
		threadID:        "thread-1",
		sessionID:       "thread-1",
		transport:       tr,
		activeTurn:      handle,
		eventDispatcher: dispatcher,
	}

	s.handleReceiveExit(tr, io.EOF)

	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handle was not completed on EOF")
	}
	if !errors.Is(handle.Err(), io.EOF) {
		t.Fatalf("handle.Err() = %v, want EOF", handle.Err())
	}
	if s.activeTurn != nil {
		t.Fatal("activeTurn was not cleared")
	}

	select {
	case ev := <-got:
		if ev.Success {
			t.Fatal("TurnCompleted.Success = true, want false")
		}
		if ev.Error != io.EOF.Error() {
			t.Fatalf("TurnCompleted.Error = %q, want %q", ev.Error, io.EOF.Error())
		}
		if ev.TurnID != "turn-1" {
			t.Fatalf("TurnCompleted.TurnID = %q, want turn-1", ev.TurnID)
		}
	case <-time.After(time.Second):
		t.Fatal("TurnCompleted event was not published")
	}
}
