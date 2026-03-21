package wails

import "testing"

func TestEventBridgePublishEmitsLegacyRefreshEvents(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	emitted := make([]map[string]any, 0, 3)
	lifecycle.SetEventEmitter(func(name string, payload any) {
		if name != bridgeEventName {
			t.Fatalf("event name = %q, want %q", name, bridgeEventName)
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

func assertBridgeType(t *testing.T, payload map[string]any, want string) {
	t.Helper()

	if payload["type"] != want {
		t.Fatalf("payload type = %#v, want %q; payload=%#v", payload["type"], want, payload)
	}
}
