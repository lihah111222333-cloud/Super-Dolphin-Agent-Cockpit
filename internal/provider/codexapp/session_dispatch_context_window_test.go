package codexapp

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	provdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

// TestDispatchTurnIdentityFieldGuard 从公共 TurnIDHeader 的 JSON tag 动态取得公开字段，
// 锁定 provider turn ID 在 dispatch 边界必须映射回同一 handle 的 local TurnRef。
func TestDispatchTurnIdentityFieldGuard(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeFor[shareddto.TurnIDHeader]().FieldByName("TurnID")
	if !ok {
		t.Fatal("TurnIDHeader.TurnID field is missing")
	}
	wireName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if wireName == "" || wireName == "-" {
		t.Fatalf("TurnIDHeader.TurnID json tag = %q, want public wire field", field.Tag.Get("json"))
	}

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	rawEvents := make(chan provdto.BusRawProviderEvent, 1)
	cancelSub := event.Subscribe(bus, func(ev provdto.BusRawProviderEvent) { rawEvents <- ev })
	defer cancelSub()

	const providerTurnID = "provider-turn-1"
	const localTurnID = "local-turn-1"
	s := &session{
		agentID:    "agent-1",
		dispatcher: unified.NewEventDispatcher(bus, nil),
		turns: map[string]*turnHandle{
			providerTurnID: newTurnHandle(localTurnID, providerTurnID),
		},
	}
	input := map[string]any{wireName: providerTurnID}
	s.dispatch(provdto.RawProviderEvent{
		EventType: "turn/started",
		Data:      input,
	})
	if got := stringValue(input, wireName, "turnId"); got != providerTurnID {
		t.Fatalf("dispatch rewrote internal turn identity = %q, want provider ID %q", got, providerTurnID)
	}

	select {
	case ev := <-rawEvents:
		payload := ev.Event.Data.(map[string]any)
		if got := stringValue(payload, wireName, "turnId"); got != localTurnID {
			t.Fatalf("public turn identity = %q, want local TurnRef %q", got, localTurnID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for remapped turn event")
	}
}

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

// TestRemapEventIdentityPreservesLaunchProviderThread 验证启动事件保留 provider 原生 thread id，非启动事件仍拒绝透传 alien thread。
func TestRemapEventIdentityPreservesLaunchProviderThread(t *testing.T) {
	t.Parallel()
	const hostAgentID = "agent-host"
	const providerThreadID = "019fa888-06a9-78f2-9478-a96a57794439"
	s := &session{}
	launchPayload := map[string]any{"threadId": providerThreadID}
	s.remapEventIdentity("thread/started", launchPayload, hostAgentID)
	if got := stringValue(launchPayload, "threadId"); got != hostAgentID {
		t.Fatalf("threadId = %q, want host id %q", got, hostAgentID)
	}
	if got := stringValue(launchPayload, "providerThreadId"); got != providerThreadID {
		t.Fatalf("providerThreadId = %q, want %q", got, providerThreadID)
	}
	translated, ok := translateAgentEvent("thread/started", launchPayload)
	if !ok {
		t.Fatal("translateAgentEvent() ok = false, want true")
	}
	launched, ok := translated.(agentdto.AgentLaunched)
	if !ok {
		t.Fatalf("translated type = %T, want agentdto.AgentLaunched", translated)
	}
	if launched.AgentID != hostAgentID || launched.ProviderThreadID != providerThreadID {
		t.Fatalf("launched identity = (%q, %q), want (%q, %q)", launched.AgentID, launched.ProviderThreadID, hostAgentID, providerThreadID)
	}
	nonLaunchPayload := map[string]any{"threadId": providerThreadID}
	s.remapEventIdentity("turn/completed", nonLaunchPayload, hostAgentID)
	if got := stringValue(nonLaunchPayload, "providerThreadId"); got != "" {
		t.Fatalf("non-launch providerThreadId = %q, want empty", got)
	}
	if got := stringValue(nonLaunchPayload, "threadId"); got != hostAgentID {
		t.Fatalf("non-launch threadId = %q, want host id %q", got, hostAgentID)
	}
}
