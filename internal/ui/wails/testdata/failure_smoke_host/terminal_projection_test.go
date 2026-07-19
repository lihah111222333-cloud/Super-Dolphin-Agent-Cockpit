package main

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
)

func TestPublishTerminalFailurePublishesCanonicalTerminal(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	emitted := make(chan turndto.TurnTerminalV2, 1)
	for _, cancel := range eventsurface.Bind(dispatcher, nil, func(method string, payload any) {
		if method == eventsurface.MethodTurnTerminal {
			emitted <- payload.(turndto.TurnTerminalV2)
		}
	}) {
		t.Cleanup(cancel)
	}

	if err := publishTerminalFailure(providerDispatchers(dispatcher)); err != nil {
		t.Fatalf("publishTerminalFailure() error = %v", err)
	}

	select {
	case terminal := <-emitted:
		if terminal.Outcome != "failed" || terminal.PublicError == nil {
			t.Fatalf("terminal = %#v, want canonical failed public terminal", terminal)
		}
		if terminal.PublicError.Message != "The provider could not complete this turn." {
			t.Fatalf("terminal.PublicError.Message = %q", terminal.PublicError.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canonical terminal event")
	}
}
