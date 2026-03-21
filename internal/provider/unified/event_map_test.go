package unified

import (
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

func TestEventDispatcherDispatchesCommonPlanAndItemEvents(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()

	dispatcher := NewEventDispatcher(bus, nil)
	planCh := make(chan turndto.PlanDelta, 1)
	itemCh := make(chan turndto.ItemCompleted, 1)
	cancelPlan := event.Subscribe(bus, func(ev turndto.PlanDelta) { planCh <- ev })
	cancelItem := event.Subscribe(bus, func(ev turndto.ItemCompleted) { itemCh <- ev })
	defer cancelPlan()
	defer cancelItem()

	dispatcher.Dispatch(dto.RawProviderEvent{
		Type: "item/plan/delta",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"delta":     "step 1",
			"timestamp": time.Now().Format(time.RFC3339Nano),
		},
	})
	dispatcher.Dispatch(dto.RawProviderEvent{
		Type: "item/completed",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"type":      "command_execution",
			"command":   "ls",
			"exit_code": 0,
			"timestamp": time.Now().Format(time.RFC3339Nano),
		},
	})

	select {
	case got := <-planCh:
		if got.ThreadID != "thread-1" || got.TurnID != "turn-1" || got.Delta != "step 1" {
			t.Fatalf("plan delta = %#v", got)
		}
	default:
		t.Fatal("expected plan delta event")
	}

	select {
	case got := <-itemCh:
		if got.ThreadID != "thread-1" || got.Command != "ls" || got.ExitCode != 0 {
			t.Fatalf("item completed = %#v", got)
		}
	default:
		t.Fatal("expected item completed event")
	}
}

func TestEventDispatcherDispatchesCommonErrorEvents(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()

	dispatcher := NewEventDispatcher(bus, nil)
	errCh := make(chan agentdto.AgentError, 1)
	warnCh := make(chan agentdto.AgentWarning, 1)
	cancelErr := event.Subscribe(bus, func(ev agentdto.AgentError) { errCh <- ev })
	cancelWarn := event.Subscribe(bus, func(ev agentdto.AgentWarning) { warnCh <- ev })
	defer cancelErr()
	defer cancelWarn()

	dispatcher.Dispatch(dto.RawProviderEvent{
		Type: "error",
		Data: map[string]any{
			"agentId":     "agent-1",
			"threadId":    "thread-1",
			"message":     "boom",
			"recoverable": true,
		},
	})
	dispatcher.Dispatch(dto.RawProviderEvent{
		Type: "configWarning",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "thread-1",
			"message":  "heads up",
		},
	})

	select {
	case got := <-errCh:
		if got.AgentID != "agent-1" || got.Message != "boom" || !got.Recoverable {
			t.Fatalf("agent error = %#v", got)
		}
	default:
		t.Fatal("expected agent error event")
	}

	select {
	case got := <-warnCh:
		if got.AgentID != "agent-1" || got.Message != "heads up" {
			t.Fatalf("agent warning = %#v", got)
		}
	default:
		t.Fatal("expected agent warning event")
	}
}
