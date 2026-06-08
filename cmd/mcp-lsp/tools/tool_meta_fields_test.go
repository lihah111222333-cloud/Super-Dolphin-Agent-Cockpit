package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustMarshalObject(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal object: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal object %s: %v", raw, err)
	}
	return payload
}

func requireObjectField(t *testing.T, payload map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := payload[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, payload[key])
	}
	return value
}

func requireNumberField(t *testing.T, payload map[string]any, key string, want int) {
	t.Helper()
	if payload[key] != float64(want) {
		t.Fatalf("%s = %#v, want %d", key, payload[key], want)
	}
}

func requireBoolField(t *testing.T, payload map[string]any, key string, want bool) {
	t.Helper()
	if payload[key] != want {
		t.Fatalf("%s = %#v, want %t", key, payload[key], want)
	}
}

func requireStringFieldContains(t *testing.T, payload map[string]any, key string, parts ...string) {
	t.Helper()
	value, _ := payload[key].(string)
	for _, part := range parts {
		if !strings.Contains(value, part) {
			t.Fatalf("%s = %q, want containing %q", key, value, part)
		}
	}
}

func requireAbsentField(t *testing.T, payload map[string]any, key string) {
	t.Helper()
	if _, ok := payload[key]; ok {
		t.Fatalf("payload contains legacy %s: %#v", key, payload)
	}
}

func requireLimitedMarkdownSupportMessage(t *testing.T, message string, capability string) {
	t.Helper()
	for _, part := range []string{capability, "markdown", "limited", "document_symbol", "text_search"} {
		if !strings.Contains(message, part) {
			t.Fatalf("message = %q, want containing %q", message, part)
		}
	}
	if strings.Contains(message, "language server") {
		t.Fatalf("message = %q, must not imply a broken language server", message)
	}
}
