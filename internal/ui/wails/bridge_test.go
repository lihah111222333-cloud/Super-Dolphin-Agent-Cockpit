package wails

import "testing"

func TestEventBridgePublishEmitsLegacyRefreshEvents(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	emitted := make([]map[string]any, 0, 3)
	lifecycle.SetEventEmitter(func(name string, payload any) {
		if name != bridgeEventName {
			return
		}
		value, _ := payload.(map[string]any)
		emitted = append(emitted, value)
	})

	bridge := NewEventBridge(nil, lifecycle, nil)
	bridge.publish("thread/started", map[string]any{
		"threadId": "thread-1",
		"agentId":  "agent-1",
	})

	if len(emitted) != 3 {
		t.Fatalf("len(emitted) = %d, want 3", len(emitted))
	}
	assertBridgeType(t, emitted[0], "thread/started")
	assertBridgeType(t, emitted[1], "ui/thread/changed")
	assertBridgeType(t, emitted[2], "ui/sidebar/changed")
}

func TestEventBridgePublishEmitsAgentEventCompatibilityChannel(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	emitted := make([]map[string]any, 0, 3)
	lifecycle.SetEventEmitter(func(name string, payload any) {
		if name != agentEventName {
			return
		}
		value, _ := payload.(map[string]any)
		emitted = append(emitted, value)
	})

	bridge := NewEventBridge(nil, lifecycle, nil)
	bridge.publish("thread/started", map[string]any{
		"threadId": "thread-1",
		"agentId":  "agent-1",
	})

	if len(emitted) != 3 {
		t.Fatalf("len(emitted) = %d, want 3", len(emitted))
	}
	assertAgentEvent(t, emitted[0], "thread-1", "thread/started")
	assertAgentEvent(t, emitted[1], "thread-1", "ui/thread/changed")
	assertAgentEvent(t, emitted[2], "thread-1", "ui/sidebar/changed")
}

func TestEventBridgePublishSkipsAgentEventWithoutIdentity(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	agentEvents := 0
	lifecycle.SetEventEmitter(func(name string, payload any) {
		if name == agentEventName {
			agentEvents++
		}
	})

	bridge := NewEventBridge(nil, lifecycle, nil)
	bridge.publish("skills/changed", map[string]any{"name": "demo"})

	if agentEvents != 0 {
		t.Fatalf("agent event count = %d, want 0", agentEvents)
	}
}

func assertBridgeType(t *testing.T, payload map[string]any, want string) {
	t.Helper()

	if payload["type"] != want {
		t.Fatalf("payload type = %#v, want %q; payload=%#v", payload["type"], want, payload)
	}
}

func assertAgentEvent(t *testing.T, payload map[string]any, wantAgentID string, wantType string) {
	t.Helper()

	if payload["agent_id"] != wantAgentID {
		t.Fatalf("payload agent_id = %#v, want %q; payload=%#v", payload["agent_id"], wantAgentID, payload)
	}
	if payload["type"] != wantType {
		t.Fatalf("payload type = %#v, want %q; payload=%#v", payload["type"], wantType, payload)
	}
}
