package codexapp

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	provdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestDispatchPreservesCodexContextWindowTokens(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	rawEvents := make(chan provdto.BusRawProviderEvent, 1)
	cancelSub := event.Subscribe(bus, func(ev provdto.BusRawProviderEvent) {
		rawEvents <- ev
	})
	defer cancelSub()

	s := &session{
		agentID:    "agent-1",
		dispatcher: unified.NewEventDispatcher(bus, nil),
		runtimeConfig: map[string]any{
			"model": "gpt-5.5",
		},
	}
	s.dispatch(provdto.RawProviderEvent{
		EventType: "token_count_update",
		Data: map[string]any{
			"contextWindowTokens": 258400,
			"usage": map[string]any{
				"contextWindowTokens": 258400,
			},
		},
	})

	select {
	case ev := <-rawEvents:
		payload, ok := ev.Event.Data.(map[string]any)
		if !ok {
			t.Fatalf("Event.Data type = %T, want map[string]any", ev.Event.Data)
		}
		if got := payload["contextWindowTokens"]; got != 258400 {
			t.Fatalf("contextWindowTokens = %v, want 258400", got)
		}
		usage, ok := payload["usage"].(map[string]any)
		if !ok {
			t.Fatalf("usage type = %T, want map[string]any", payload["usage"])
		}
		if got := usage["contextWindowTokens"]; got != 258400 {
			t.Fatalf("usage.contextWindowTokens = %v, want 258400", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for raw provider event")
	}
}

// TestDispatchKeepsContextWindowForUnspecifiedModel covers the deleted
// empty-model override branch: a session launched without an explicit model
// previously had its window forced to 872000. The Codex CLI's reported value
// must now survive untouched.
func TestDispatchKeepsContextWindowForUnspecifiedModel(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	rawEvents := make(chan provdto.BusRawProviderEvent, 1)
	cancelSub := event.Subscribe(bus, func(ev provdto.BusRawProviderEvent) {
		rawEvents <- ev
	})
	defer cancelSub()

	// runtimeConfig carries no "model" key.
	s := &session{
		agentID:    "agent-1",
		dispatcher: unified.NewEventDispatcher(bus, nil),
	}
	s.dispatch(provdto.RawProviderEvent{
		EventType: "token_count_update",
		Data: map[string]any{
			"contextWindowTokens": 400000,
		},
	})

	select {
	case ev := <-rawEvents:
		payload, ok := ev.Event.Data.(map[string]any)
		if !ok {
			t.Fatalf("Event.Data type = %T, want map[string]any", ev.Event.Data)
		}
		if got := payload["contextWindowTokens"]; got != 400000 {
			t.Fatalf("contextWindowTokens = %v, want 400000 (must not be overridden to 872000)", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for raw provider event")
	}
}
