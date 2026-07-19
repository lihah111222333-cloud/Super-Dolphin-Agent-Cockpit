package unified

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestEventDispatcherAttachesCanonicalTerminalBeforePublishing(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	seen := make(chan turndto.TurnCompleted, 1)
	cancel := event.Subscribe(bus, func(ev turndto.TurnCompleted) { seen <- ev })
	t.Cleanup(cancel)

	dispatcher := NewEventDispatcher(bus, nil)
	dispatcher.Publish(turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{EventHeader: shareddto.EventHeader{Timestamp: time.Now().UTC()}, ThreadID: "thread-1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: false,
		Status:  "failed",
		Error:   "Authorization: Bearer provider-secret",
	})

	select {
	case completed := <-seen:
		terminal, canonical, err := turndto.CanonicalTurnTerminal(completed)
		if err != nil || !canonical {
			t.Fatalf("CanonicalTurnTerminal() = (%#v, %v, %v), want attached canonical terminal", terminal, canonical, err)
		}
		if terminal.PublicError == nil || terminal.PublicError.Message != "The provider could not complete this turn." {
			t.Fatalf("terminal.PublicError = %#v, want canonical public provider failure", terminal.PublicError)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canonical TurnCompleted")
	}
}
