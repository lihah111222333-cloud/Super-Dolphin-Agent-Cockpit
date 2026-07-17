package codexapp

import (
	"strings"
	"testing"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestFailureMatrixCodexTerminalCases(t *testing.T) {
	t.Parallel()

	t.Run("FM-07", testAcceptedStopFollowedByFailure)
	t.Run("FM-09", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, codexFailureMatrixPayload(false, "cancelled"), "cancelled", false, "provider", "")
	})
	t.Run("FM-10", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, codexFailureMatrixPayload(false, "interrupted"), "interrupted", false, "provider", "")
	})
	t.Run("FM-11", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, codexFailureMatrixPayload(false, "failed"), "failed", false, "", "")
	})
	t.Run("FM-12", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, codexFailureMatrixPayload(false, "mystery"), "failed", true, "", "")
	})
	t.Run("FM-13", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, map[string]any{
			"threadId": "thread-1", "agentId": "agent-1", "turnId": "turn-1", "status": "failed",
		}, "failed", true, "", "")
	})
	t.Run("FM-14", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, codexFailureMatrixPayload(false, "completed"), "failed", true, "", "")
	})
	t.Run("FM-19", testMatchedUserCancellation)
	t.Run("FM-20", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, map[string]any{
			"threadId": "thread-1", "agentId": "agent-1", "turnId": "turn-1",
			"success": false, "status": "cancelled", "requestId": "untrusted-provider-request",
		}, "cancelled", false, "provider", "")
	})
}

func testAcceptedStopFollowedByFailure(t *testing.T) {
	t.Helper()
	s := &session{interruptRequests: map[string]*interruptRequestClaim{"turn-1": {
		requestID: "stop-1",
		state:     interruptRequestAccepted,
	}}}
	payload := codexFailureMatrixPayload(false, "failed")
	if s.applyAcceptedInterruptRequest("turn/completed", payload) {
		t.Fatalf("failed terminal consumed accepted stop claim: %#v", payload)
	}
	terminal := requireFailureMatrixTerminal(t, payload)
	if terminal.Success || terminal.Status != "failed" || terminal.Reason == "user_request" || terminal.TerminationRequestID != "" {
		t.Fatalf("terminal = %#v, want provider failure without user-request attribution", terminal)
	}
}

func testMatchedUserCancellation(t *testing.T) {
	t.Helper()
	s := &session{interruptRequests: map[string]*interruptRequestClaim{"turn-1": {
		requestID: "stop-user-1",
		state:     interruptRequestAccepted,
	}}}
	payload := codexFailureMatrixPayload(false, "cancelled")
	if !s.applyAcceptedInterruptRequest("turn/completed", payload) {
		t.Fatalf("cancelled terminal did not consume accepted user stop claim: %#v", payload)
	}
	terminal := requireFailureMatrixTerminal(t, payload)
	if terminal.Success || terminal.Status != "cancelled" || terminal.Reason != "user_request" || terminal.TerminationRequestID != "stop-user-1" {
		t.Fatalf("terminal = %#v, want request-attributed user cancellation", terminal)
	}
}

func assertCodexFailureMatrixTerminal(
	t *testing.T,
	payload map[string]any,
	wantStatus string,
	wantContract bool,
	wantCause string,
	wantRequestID string,
) {
	t.Helper()
	terminal := requireFailureMatrixTerminal(t, payload)
	if terminal.Success || terminal.Status != wantStatus {
		t.Fatalf("terminal = %#v, want non-success status %q", terminal, wantStatus)
	}
	if strings.Contains(terminal.Error, "terminal contract") != wantContract {
		t.Fatalf("terminal error = %q, wantContract=%v", terminal.Error, wantContract)
	}
	if terminal.Reason != wantCause || terminal.TerminationRequestID != wantRequestID {
		t.Fatalf("terminal attribution = (%q, %q), want (%q, %q)", terminal.Reason, terminal.TerminationRequestID, wantCause, wantRequestID)
	}
}

func codexFailureMatrixPayload(success bool, status string) map[string]any {
	return map[string]any{
		"threadId": "thread-1",
		"agentId":  "agent-1",
		"turnId":   "turn-1",
		"success":  success,
		"status":   status,
	}
}

func requireFailureMatrixTerminal(t *testing.T, payload map[string]any) turndto.TurnCompleted {
	t.Helper()
	ev, ok := translateTurnEvent("turn/completed", payload)
	if !ok {
		t.Fatal("translateTurnEvent() ok = false, want visible terminal")
	}
	terminal, ok := ev.(turndto.TurnCompleted)
	if !ok {
		t.Fatalf("translated event type = %T, want TurnCompleted", ev)
	}
	return terminal
}
