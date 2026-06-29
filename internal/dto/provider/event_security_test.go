package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRawProviderEventSanitizedCopyRemovesSecretPayload(t *testing.T) {
	raw := RawProviderEvent{
		EventType: "error",
		Data: map[string]any{
			"session_id": "session-1",
			"thread_id":  "thread-1",
			"message":    "provider failed",
			"token":      "sk-live-secret",
			"nested": map[string]any{
				"password": "hunter2",
			},
		},
	}

	safe := raw.SanitizedCopy()
	encoded, err := json.Marshal(safe.Data)
	if err != nil {
		t.Fatalf("Marshal(safe.Data) error = %v", err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"sk-live-secret", "hunter2", "token", "password", "nested"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sanitized raw event leaked %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{"session_id", "session-1", "thread_id", "thread-1", "payload_size_bytes", "payload_sha256", "payload_field_names"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sanitized raw event missing %q: %s", want, text)
		}
	}
}

func TestRawProviderEventSafePayloadOnlyContainsMetadata(t *testing.T) {
	raw := RawProviderEvent{
		EventType: "configWarning",
		Data: map[string]any{
			"agentId": "agent-1",
			"message": "safe visible warning",
			"api_key": "super-secret",
		},
	}

	payload := raw.SafePayload()
	text := string(payload)
	for _, forbidden := range []string{"super-secret", "api_key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("safe payload leaked %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{"agent_id", "agent-1", "payload_size_bytes", "payload_sha256"} {
		if !strings.Contains(text, want) {
			t.Fatalf("safe payload missing %q: %s", want, text)
		}
	}
}
