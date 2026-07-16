package codexapp

import (
	"strings"
	"testing"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestCodexTerminalTruthRejectsUnknownAndMissingOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload map[string]any
	}{
		{name: "unknown status", payload: map[string]any{"success": true, "status": "mystery"}},
		{name: "missing success", payload: map[string]any{"status": "completed"}},
		{name: "missing status", payload: map[string]any{"success": true}},
		{name: "conflicting success", payload: map[string]any{"success": true, "status": "failed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.payload["threadId"] = "thread-1"
			tt.payload["agentId"] = "agent-1"
			tt.payload["turnId"] = "turn-1"
			ev, ok := translateTurnEvent("turn/completed", tt.payload)
			if !ok {
				t.Fatal("translateTurnEvent() ok = false, want visible contract-error terminal")
			}
			terminal, ok := ev.(turndto.TurnCompleted)
			if !ok {
				t.Fatalf("translated event type = %T, want TurnCompleted", ev)
			}
			if terminal.Success || !strings.Contains(terminal.Error, "terminal contract") {
				t.Fatalf("terminal = %#v, want explicit contract failure", terminal)
			}
		})
	}
}

func TestCodexProviderCancelCannotMasqueradeAsUserCancel(t *testing.T) {
	t.Parallel()

	ev, ok := translateTurnEvent("turn/completed", map[string]any{
		"threadId":  "thread-1",
		"agentId":   "agent-1",
		"turnId":    "turn-1",
		"success":   false,
		"status":    "cancelled",
		"requestId": "untrusted-provider-request",
	})
	if !ok {
		t.Fatal("translateTurnEvent() ok = false")
	}
	terminal := ev.(turndto.TurnCompleted)
	if terminal.Status != "cancelled" || terminal.Reason == "user_request" {
		t.Fatalf("terminal = %#v, want provider cancellation without user_request attribution", terminal)
	}
}

func TestCodexAbortedRawCauseCannotMasqueradeAsUserCancel(t *testing.T) {
	t.Parallel()

	ev, ok := translateTurnEvent("turn/aborted", map[string]any{
		"threadId":  "thread-1",
		"agentId":   "agent-1",
		"turnId":    "turn-1",
		"reason":    "user_request",
		"requestId": "untrusted-provider-request",
	})
	if !ok {
		t.Fatal("translateTurnEvent() ok = false")
	}
	terminal := ev.(turndto.TurnCompleted)
	if terminal.Status != "cancelled" || terminal.Reason != "provider" {
		t.Fatalf("terminal = %#v, want provider cancellation without user_request attribution", terminal)
	}
}
