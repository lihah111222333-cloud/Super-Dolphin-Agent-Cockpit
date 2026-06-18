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

func TestTranslateTurnEvent_MessageAliasPayloadReasoningStream(t *testing.T) {
	payload := map[string]any{
		"threadId": "thread-1",
		"turnId":   "T-reasoning",
		"stream":   "reasoning",
		"delta":    "thinking text",
	}

	ev, ok := translateTurnEvent("message.delta", payload)
	if !ok {
		t.Fatal("expected message.delta to translate into TurnOutputDelta")
	}
	delta, ok := ev.(turndto.TurnOutputDelta)
	if !ok {
		t.Fatalf("event type = %T, want TurnOutputDelta", ev)
	}
	if delta.Stream != "reasoning" {
		t.Fatalf("TurnOutputDelta.Stream = %q, want reasoning", delta.Stream)
	}
	if delta.Delta != "thinking text" {
		t.Fatalf("TurnOutputDelta.Delta = %q, want reasoning text", delta.Delta)
	}
}

func TestTurnCompleted_EndToEnd_ReasoningDeltaNotAccumulatedAsResult(t *testing.T) {
	s := newAccumulatorTestSession()
	rawDelta, err := json.Marshal(map[string]any{
		"turnId": "T-reasoning",
		"stream": "reasoning",
		"delta":  "thinking text",
	})
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	_ = s.sniffTurnOutput("message.delta", rawDelta)

	terminal, err := json.Marshal(map[string]any{
		"turnId":  "T-reasoning",
		"success": true,
	})
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected turn/completed to translate into TurnCompleted DTO")
	}
	if completed.Result != "" {
		t.Fatalf("TurnCompleted.Result = %q, want no reasoning text in final result", completed.Result)
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

// TestTurnCompleted_EndToEnd_FailedTurnCarriesError covers the failed-turn
// path the success-only cases above miss: a codex turn that ends
// unsuccessfully must translate into TurnCompleted{Success:false} with the
// failure detail in Error. turnCompletedReportText (the orchestration
// report fallback) relies on this so a failed child agent's
// get_agent_report carries the error instead of an empty report.
func TestTurnCompleted_EndToEnd_FailedTurnCarriesError(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-fail",
		"success": false,
		"status":  "failed",
		"error":   "codex tool call denied",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected turn/completed to translate into TurnCompleted DTO")
	}
	if completed.Success {
		t.Fatalf("expected Success=false for a failed turn")
	}
	if completed.Error != "codex tool call denied" {
		t.Fatalf("TurnCompleted.Error = %q, want the failure detail", completed.Error)
	}
}

func TestTurnCompleted_EndToEnd_TurnFailedEventCarriesError(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId": "T-failed-event",
		"status": "failed",
		"error":  "The 'gpt-5' model is not supported when using Codex with a ChatGPT account.",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/failed", terminal)
	if !ok {
		t.Fatalf("expected turn/failed to translate into TurnCompleted DTO")
	}
	if completed.Success {
		t.Fatalf("expected Success=false for a turn/failed event")
	}
	if !strings.Contains(completed.Error, "gpt-5") {
		t.Fatalf("TurnCompleted.Error = %q, want the provider failure detail", completed.Error)
	}
}

func TestFinishTurn_ModelUnsupportedErrorCompletesHandleWithNotice(t *testing.T) {
	h := newTurnHandle("local-1", "T-model-error")
	s := &session{
		turns: map[string]*turnHandle{
			"T-model-error": h,
		},
		activeTurnID: "T-model-error",
		runtimeConfig: map[string]any{
			"model": "gpt-5",
		},
	}
	terminal, _ := json.Marshal(map[string]any{
		"turnId": "T-model-error",
		"error":  "The 'gpt-5' model is not supported when using Codex with a ChatGPT account.",
	})

	s.finishTurn(terminal, false)

	select {
	case <-h.Done():
	default:
		t.Fatal("expected failed turn to complete the handle")
	}
	if h.Err() == nil {
		t.Fatal("expected failed turn to complete with an error")
	}
	if !strings.Contains(h.Err().Error(), `Codex model "gpt-5" is not supported`) {
		t.Fatalf("turn handle error = %q, want actionable model notice", h.Err().Error())
	}
	if s.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want cleared", s.activeTurnID)
	}
	if _, ok := s.turns["T-model-error"]; ok {
		t.Fatal("expected completed turn to be removed")
	}
}

// TestTurnCompleted_EndToEnd_AbortedTurnIsUnsuccessful covers the second
// failure shape: an aborted codex turn must translate into Success=false
// (turnTerminalSuccess short-circuits on "aborted" regardless of payload).
func TestTurnCompleted_EndToEnd_AbortedTurnIsUnsuccessful(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId": "T-abort",
		"reason": "interrupted by user",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/aborted", terminal)
	if !ok {
		t.Fatalf("expected turn/aborted to translate into TurnCompleted DTO")
	}
	if completed.Success {
		t.Fatalf("expected Success=false for an aborted turn")
	}
	if completed.Reason != "interrupted by user" {
		t.Fatalf("TurnCompleted.Reason = %q, want the abort reason", completed.Reason)
	}
}
