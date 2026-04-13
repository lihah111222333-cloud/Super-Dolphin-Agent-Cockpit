package claudecli

import (
	"encoding/json"
	"testing"
)

func TestParseLogLineUsageCountsCacheTokens(t *testing.T) {
	raw := mustMarshalWatcherLine(t, map[string]any{
		"type":      "assistant",
		"sessionId": "session-1",
		"timestamp": "2026-04-13T00:00:00Z",
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens":                3,
				"cache_creation_input_tokens": 1434,
				"cache_read_input_tokens":     8057,
				"output_tokens":               48,
			},
		},
	})
	got, ok := parseLogLineUsage(raw)
	if !ok {
		t.Fatal("parseLogLineUsage() ok = false, want true")
	}
	if got.SessionID != "session-1" || got.Timestamp != "2026-04-13T00:00:00Z" {
		t.Fatalf("parseLogLineUsage() identity = %#v", got)
	}
	if got.InputTokens != 9494 || got.OutputTokens != 48 || got.TotalTokens != 9542 {
		t.Fatalf("parseLogLineUsage() = %#v, want input=9494 output=48 total=9542", got)
	}
}

func TestParseLogLineUsageFallsBackToNestedCacheCreation(t *testing.T) {
	raw := mustMarshalWatcherLine(t, map[string]any{
		"type":      "assistant",
		"sessionId": "session-2",
		"timestamp": "2026-04-13T00:00:01Z",
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens":            10,
				"cache_read_input_tokens": 5,
				"output_tokens":           7,
				"cache_creation": map[string]any{
					"ephemeral_1h_input_tokens": 11,
					"ephemeral_5m_input_tokens": 13,
				},
			},
		},
	})
	got, ok := parseLogLineUsage(raw)
	if !ok {
		t.Fatal("parseLogLineUsage() ok = false, want true")
	}
	if got.InputTokens != 39 || got.OutputTokens != 7 || got.TotalTokens != 46 {
		t.Fatalf("parseLogLineUsage() = %#v, want input=39 output=7 total=46", got)
	}
}

func TestParseLogLineUsageIgnoresNonAssistant(t *testing.T) {
	raw := mustMarshalWatcherLine(t, map[string]any{
		"type":      "user",
		"sessionId": "session-3",
		"timestamp": "2026-04-13T00:00:02Z",
		"message": map[string]any{
			"usage": map[string]any{
				"input_tokens":  1,
				"output_tokens": 2,
			},
		},
	})
	if _, ok := parseLogLineUsage(raw); ok {
		t.Fatal("parseLogLineUsage() ok = true, want false")
	}
}

func mustMarshalWatcherLine(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}
