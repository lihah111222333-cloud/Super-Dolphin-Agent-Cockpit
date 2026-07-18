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
	t.Run("FM-20", testUnmatchedProviderUserCancellation)
	t.Run("stale-claim-cannot-own-cancellation", testStaleClaimCannotOwnCancellation)
	t.Run("system-cancellation-without-accepted-stop", func(t *testing.T) {
		assertCodexFailureMatrixTerminal(t, map[string]any{
			"threadId": "thread-1", "agentId": "agent-1", "turnId": "turn-1",
			"success": false, "status": "cancelled", "terminationCause": "system",
		}, "cancelled", false, "system", "")
	})
	t.Run("system-cancellation-wins-accepted-stop", testSystemCancellationWinsAcceptedStop)
}

func testAcceptedStopFollowedByFailure(t *testing.T) {
	t.Helper()
	s := failureMatrixSessionWithClaim("turn-1", "stop-1", 1)
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
	s := failureMatrixSessionWithClaim("turn-1", "stop-user-1", 1)
	payload := codexFailureMatrixPayload(false, "cancelled")
	payload["terminationCause"] = "user_request"
	payload["terminationRequestId"] = "untrusted-provider-request"
	if !s.applyAcceptedInterruptRequest("turn/completed", payload) {
		t.Fatalf("cancelled terminal did not consume accepted user stop claim: %#v", payload)
	}
	terminal := requireFailureMatrixTerminal(t, payload)
	if terminal.Success || terminal.Status != "cancelled" || terminal.Reason != "user_request" || terminal.TerminationRequestID != "stop-user-1" {
		t.Fatalf("terminal = %#v, want request-attributed user cancellation", terminal)
	}
}

func testUnmatchedProviderUserCancellation(t *testing.T) {
	t.Helper()
	payload := map[string]any{
		"threadId": "thread-1", "agentId": "agent-1", "turnId": "turn-1",
		"success": false, "status": "cancelled",
		"terminationCause": "user_request", "terminationRequestId": "untrusted-provider-request",
	}
	s := &session{activeTurnID: "turn-1", activeTurnGeneration: 1}
	if s.applyAcceptedInterruptRequest("turn/completed", payload) {
		t.Fatalf("unmatched provider attribution changed payload: %#v", payload)
	}
	assertCodexFailureMatrixTerminal(t, payload, "cancelled", false, "provider", "")
}

func testStaleClaimCannotOwnCancellation(t *testing.T) {
	t.Helper()
	s := failureMatrixSessionWithClaim("turn-1", "stale-stop", 1)
	s.activeTurnGeneration = 2
	payload := codexFailureMatrixPayload(false, "cancelled")
	if s.applyAcceptedInterruptRequest("turn/completed", payload) {
		t.Fatalf("stale TurnRef claim owned cancellation: %#v", payload)
	}
	assertCodexFailureMatrixTerminal(t, payload, "cancelled", false, "provider", "")
}

func testSystemCancellationWinsAcceptedStop(t *testing.T) {
	t.Helper()
	s := failureMatrixSessionWithClaim("turn-1", "stop-user-1", 1)
	payload := codexFailureMatrixPayload(false, "cancelled")
	payload["terminationCause"] = "system"
	if s.applyAcceptedInterruptRequest("turn/completed", payload) {
		t.Fatalf("accepted Stop overrode explicit system cancellation: %#v", payload)
	}
	assertCodexFailureMatrixTerminal(t, payload, "cancelled", false, "system", "")
}

func failureMatrixSessionWithClaim(turnID, requestID string, generation uint64) *session {
	return &session{
		activeTurnID:         turnID,
		activeTurnGeneration: generation,
		interruptRequests: map[string]*interruptRequestClaim{turnID: {
			turnID: turnID, requestID: requestID, generation: generation, state: interruptRequestAccepted,
		}},
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
