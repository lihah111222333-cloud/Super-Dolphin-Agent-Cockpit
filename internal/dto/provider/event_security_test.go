package provider

import (
	"encoding/json"
	"errors"
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

func TestPublicMessageExcludesRawCausesAndPreservesDiagnosticID(t *testing.T) {
	raw := RawProviderEvent{Data: map[string]any{
		"token":   "sk-live-secret",
		"command": "rm -rf /Users/private/workspace",
		"stack":   "panic at private.go:42",
	}}
	for _, message := range []string{
		raw.PublicMessage("Provider reported an error."),
		PublicMessageForError("Agent process exited unexpectedly.", errors.New("token=sk-live-secret command=/Users/private/workspace stack=private.go:42")),
	} {
		for _, forbidden := range []string{"sk-live-secret", "rm -rf", "/Users/private", "private.go:42"} {
			if strings.Contains(message, forbidden) {
				t.Fatalf("public message leaked %q: %s", forbidden, message)
			}
		}
		if !strings.Contains(message, "Diagnostic ID: ") {
			t.Fatalf("public message is missing diagnostic correlation: %s", message)
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
