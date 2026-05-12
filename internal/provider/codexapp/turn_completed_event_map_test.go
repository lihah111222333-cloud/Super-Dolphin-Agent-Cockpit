package codexapp

import (
	"encoding/json"
	"strings"
	"testing"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

// helper: decode the rewritten params produced by sniffTurnOutput, then
// translate them into the DTO.
func sniffAndTranslate(t *testing.T, s *session, method string, raw json.RawMessage) (turndto.TurnCompleted, bool) {
	t.Helper()
	merged := s.sniffTurnOutput(method, raw)
	payload := decodeEventPayload(merged)
	ev, ok := translateTurnEvent(method, payload)
	if !ok {
		return turndto.TurnCompleted{}, false
	}
	completed, ok := ev.(turndto.TurnCompleted)
	return completed, ok
}

// TestTurnCompleted_EndToEnd_AccumulatedResult drives the C1.1 + C1.2 +
// C1.3 chain: deltas arrive, TurnCompleted is translated, DTO carries the
// merged Result (ADR-015 v4.1 §2.1 acceptance criterion).
func TestTurnCompleted_EndToEnd_AccumulatedResult(t *testing.T) {
	s := newAccumulatorTestSession()

	// Stream three deltas under turn id "T-e2e".
	for _, chunk := range []string{"part-1", "part-2", "part-3"} {
		raw, err := json.Marshal(map[string]any{
			"turnId": "T-e2e",
			"stream": "message",
			"delta":  chunk,
		})
		if err != nil {
			t.Fatalf("marshal delta: %v", err)
		}
		_ = s.sniffTurnOutput("item/agentMessage/delta", raw)
	}

	terminal, err := json.Marshal(map[string]any{
		"turnId":  "T-e2e",
		"success": true,
		"status":  "completed",
	})
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected turn/completed to translate into TurnCompleted DTO")
	}
	if !completed.Success {
		t.Fatalf("expected Success=true")
	}
	if completed.Result != "part-1part-2part-3" {
		t.Fatalf("expected merged Result, got %q", completed.Result)
	}
	if completed.Status != "completed" {
		t.Fatalf("expected status preserved, got %q", completed.Status)
	}
}

// TestTurnCompleted_EndToEnd_TruncatedPropagatesButResultStillSet drives the
// 1MB cap: when truncated, DTO carries the under-cap content and the helper
// payload signals truncation through encodeEventPayload.
func TestTurnCompleted_EndToEnd_TruncatedPropagatesButResultStillSet(t *testing.T) {
	s := newAccumulatorTestSession()
	big := strings.Repeat("x", turnOutputAccumulatorMaxBytes+1)
	rawDelta, _ := json.Marshal(map[string]any{
		"turnId": "T-trunc",
		"stream": "message",
		"delta":  big,
	})
	_ = s.sniffTurnOutput("item/agentMessage/delta", rawDelta)

	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-trunc",
		"success": true,
	})
	merged := s.sniffTurnOutput("turn/completed", terminal)
	payload := decodeEventPayload(merged)
	if v, _ := payload["truncated"].(bool); !v {
		t.Fatalf("expected truncated=true in payload, got %v", payload["truncated"])
	}
	// DTO has no Truncated field today; verify Result is empty because the
	// over-cap delta was dropped before reaching the buffer (ADR §2.2 cap
	// semantics).
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected TurnCompleted DTO on second sniff")
	}
	if completed.Result != "" {
		t.Fatalf("expected empty Result (cap dropped first oversized delta), got len=%d", len(completed.Result))
	}
}

// TestTurnCompleted_EndToEnd_ProviderProvidedFieldsPreserved verifies the
// four new DTO fields fall through directly when the provider supplies them
// on the TurnCompleted payload (forward-compat path).
func TestTurnCompleted_EndToEnd_ProviderProvidedFieldsPreserved(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":      "T-direct",
		"success":     true,
		"result":      "from-provider",
		"summary":     "brief",
		"message":     "all good",
		"stop_reason": "end_turn",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected DTO translation")
	}
	if completed.Result != "from-provider" {
		t.Fatalf("Result mismatch: %q", completed.Result)
	}
	if completed.Summary != "brief" {
		t.Fatalf("Summary mismatch: %q", completed.Summary)
	}
	if completed.Message != "all good" {
		t.Fatalf("Message mismatch: %q", completed.Message)
	}
	if completed.StopReason != "end_turn" {
		t.Fatalf("StopReason mismatch: %q", completed.StopReason)
	}
}

// TestTurnCompleted_EndToEnd_NoDeltaNoResult verifies the no-buffer baseline:
// turn/completed without any preceding deltas produces DTO with empty Result,
// matching pre-ADR-015 behaviour (no regression for tool-only turns).
func TestTurnCompleted_EndToEnd_NoDeltaNoResult(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-quiet",
		"success": true,
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected DTO translation")
	}
	if completed.Result != "" {
		t.Fatalf("expected empty Result for tool-only turn, got %q", completed.Result)
	}
	if !completed.Success {
		t.Fatalf("expected Success=true")
	}
}
