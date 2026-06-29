package unified

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

func TestEventDispatcherDispatchesCommonPlanAndItemEvents(t *testing.T) {
	var published []any
	translateCommonRawEvent(dto.RawProviderEvent{
		EventType: "item/plan/delta",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "thread-1",
			"turnId":   "turn-1",
			"delta":    "step 1",
		},
	}, func(ev any) {
		published = append(published, ev)
	})
	translateCommonRawEvent(dto.RawProviderEvent{
		EventType: "item/completed",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"type":      "command_execution",
			"command":   "ls",
			"exit_code": 0,
		},
	}, func(ev any) {
		published = append(published, ev)
	})

	if len(published) != 2 {
		t.Fatalf("published events = %#v", published)
	}
	plan, ok := published[0].(turndto.PlanDelta)
	if !ok || plan.ThreadID != "thread-1" || plan.TurnID != "turn-1" || plan.Delta != "step 1" {
		t.Fatalf("plan delta = %#v", published[0])
	}
	item, ok := published[1].(turndto.ItemCompleted)
	if !ok || item.ThreadID != "thread-1" || item.Command != "ls" || item.ExitCode != 0 {
		t.Fatalf("item completed = %#v", published[1])
	}
}

func TestEventDispatcherDispatchesCommonErrorEvents(t *testing.T) {
	var (
		gotErr  agentdto.AgentError
		gotWarn agentdto.AgentWarning
	)
	translateCommonRawEvent(dto.RawProviderEvent{
		EventType: "error",
		Data: map[string]any{
			"agentId":     "agent-1",
			"threadId":    "thread-1",
			"message":     "boom",
			"recoverable": true,
		},
	}, func(ev any) {
		gotErr = ev.(agentdto.AgentError)
	})
	translateCommonRawEvent(dto.RawProviderEvent{
		EventType: "configWarning",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "thread-1",
			"message":  "heads up",
		},
	}, func(ev any) {
		gotWarn = ev.(agentdto.AgentWarning)
	})

	if gotErr.AgentID != "agent-1" || gotErr.Message != "boom" || !gotErr.Recoverable {
		raw, _ := json.Marshal(gotErr)
		t.Fatalf("agent error = %s", raw)
	}
	if gotWarn.AgentID != "agent-1" || gotWarn.Message != "heads up" {
		raw, _ := json.Marshal(gotWarn)
		t.Fatalf("agent warning = %s", raw)
	}
}

func TestEventDispatcherPublishesSanitizedRawProviderEvent(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := NewEventDispatcher(bus, nil)
	got := make(chan dto.BusRawProviderEvent, 1)
	cancel := event.Subscribe(bus, func(ev dto.BusRawProviderEvent) {
		got <- ev
	})
	defer cancel()

	dispatcher.Dispatch(dto.RawProviderEvent{
		EventType: "error",
		Data: map[string]any{
			"session_id": "session-1",
			"message":    "boom",
			"token":      "sk-live-secret",
		},
	})

	select {
	case ev := <-got:
		encoded, _ := json.Marshal(ev.Event.Data)
		text := string(encoded)
		if strings.Contains(text, "sk-live-secret") || strings.Contains(text, "token") {
			t.Fatalf("raw bus event leaked provider payload: %s", text)
		}
		if !strings.Contains(text, "payload_sha256") || !strings.Contains(text, "payload_size_bytes") {
			t.Fatalf("raw bus event missing safe metadata: %s", text)
		}
	case <-time.After(time.Second):
		t.Fatal("raw provider event was not published")
	}
}

func TestCommonErrorPayloadUsesSafeProviderMetadata(t *testing.T) {
	var gotErr agentdto.AgentError
	translateCommonRawEvent(dto.RawProviderEvent{
		EventType: "error",
		Data: map[string]any{
			"agentId":  "agent-1",
			"message":  "boom",
			"password": "hunter2",
		},
	}, func(ev any) {
		gotErr = ev.(agentdto.AgentError)
	})
	text := string(gotErr.Payload)
	for _, forbidden := range []string{"hunter2", "password"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("AgentError payload leaked %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{"payload_sha256", "payload_size_bytes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("AgentError payload missing %q: %s", want, text)
		}
	}
}

func TestEventDispatcherSuppressesRetryProgressErrorEvents(t *testing.T) {
	var published []any
	translateCommonRawEvent(dto.RawProviderEvent{
		EventType: "error",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"willRetry": true,
			"error": map[string]any{
				"message":           "Reconnecting... 2/5",
				"additionalDetails": "request timed out",
			},
		},
	}, func(ev any) {
		published = append(published, ev)
	})
	if len(published) != 0 {
		t.Fatalf("published events = %#v, want retry progress suppressed", published)
	}

	translateCommonRawEvent(dto.RawProviderEvent{
		EventType: "error",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"willRetry": true,
			"error": map[string]any{
				"message": "permission denied",
			},
		},
	}, func(ev any) {
		published = append(published, ev)
	})
	if len(published) != 1 {
		t.Fatalf("published events = %#v, want non-progress retry error still visible", published)
	}
}
