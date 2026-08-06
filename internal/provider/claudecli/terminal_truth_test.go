package claudecli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestClaudeInterruptedIsTerminalWithoutCompletedEvent(t *testing.T) {
	t.Parallel()

	ev, ok := translateCanonicalClaudeTerminal(t, dto.RawProviderEvent{EventType: "turn:interrupted", Data: map[string]any{
		"thread_id": "thread-1",
		"agent_id":  "agent-1",
		"turn_id":   "turn-1",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"reason":    "provider interrupted",
	}})
	if !ok {
		t.Fatal("translateTurnEvent() ok = false")
	}
	terminal, ok := ev.(turndto.TurnCompleted)
	if !ok {
		t.Fatalf("translated event type = %T, want canonical TurnCompleted terminal", ev)
	}
	if terminal.Success || terminal.Status != "interrupted" {
		t.Fatalf("terminal = %#v, want interrupted non-success", terminal)
	}
}

func TestClaudeCompletedRawCauseCannotMasqueradeAsUserCancel(t *testing.T) {
	t.Parallel()

	ev, ok := translateCanonicalClaudeTerminal(t, dto.RawProviderEvent{EventType: "turn:complete", Data: map[string]any{
		"thread_id":  "thread-1",
		"agent_id":   "agent-1",
		"turn_id":    "turn-1",
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"success":    false,
		"status":     "cancelled",
		"reason":     "user_request",
		"request_id": "untrusted-provider-request",
	}})
	if !ok {
		t.Fatal("translateTurnEvent() ok = false")
	}
	terminal := ev.(turndto.TurnCompleted)
	if terminal.Status != "cancelled" || terminal.Reason != "provider" {
		t.Fatalf("terminal = %#v, want provider cancellation without user_request attribution", terminal)
	}
}

func translateCanonicalClaudeTerminal(t *testing.T, raw dto.RawProviderEvent) (any, bool) {
	t.Helper()
	return translateTurnEvent(testRuntimeHooks(t), attachClaudeTerminalOutcome(raw))
}

func TestClaudeMalformedOutcomeUsesActiveIdentityAcrossEventSurface(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
	}{
		{name: "missing", data: map[string]any{"turn_id": "raw-turn", "agent_id": "raw-agent", "prompt": "raw-secret"}},
		{name: "unknown", data: map[string]any{"turn_id": "raw-turn", "agent_id": "raw-agent", "success": false, "status": "raw-secret"}},
		{name: "conflicting", data: map[string]any{"turn_id": "raw-turn", "agent_id": "raw-agent", "success": true, "status": "failed", "prompt": "raw-secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertClaudeMalformedOutcomeAcrossEventSurface(t, tt.data)
		})
	}
}

func assertClaudeMalformedOutcomeAcrossEventSurface(t *testing.T, data map[string]any) {
	t.Helper()
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher, testRuntimeHooks(t))
	surface := make(chan turndto.TurnTerminalV2, 1)
	for _, cancel := range eventsurface.Bind(bus, nil, func(method string, payload any) {
		if method == eventsurface.MethodTurnTerminal {
			surface <- payload.(turndto.TurnTerminalV2)
		}
	}) {
		defer cancel()
	}
	tr := &transport{}
	h := newTurnHandle("local-trusted", "T-trusted")
	s := &session{agentID: "trusted-agent", publicThreadID: "trusted-agent", sessionID: "trusted-session", transport: tr, eventDispatcher: dispatcher, activeTurn: h}
	s.applyRaw(tr, dto.RawProviderEvent{EventType: "turn:complete", Data: data})
	terminal := <-surface
	assertTrustedMalformedTerminal(t, terminal)
	encoded, err := json.Marshal(terminal)
	if err != nil {
		t.Fatalf("marshal surface terminal: %v", err)
	}
	assertMalformedTerminalHasNoRawLeak(t, encoded)
	assertMalformedHandleError(t, h.Err())
}

func assertTrustedMalformedTerminal(t *testing.T, terminal turndto.TurnTerminalV2) {
	t.Helper()
	if terminal.ThreadID != "trusted-agent" || terminal.TurnID != "T-trusted" || terminal.Outcome != "failed" {
		t.Fatalf("surface terminal = %#v, want trusted failed identity", terminal)
	}
}

func assertMalformedTerminalHasNoRawLeak(t *testing.T, encoded []byte) {
	t.Helper()
	if strings.Contains(string(encoded), "raw-secret") || strings.Contains(string(encoded), "raw-turn") || strings.Contains(string(encoded), "raw-agent") {
		t.Fatalf("surface terminal leaked raw payload: %s", encoded)
	}
}

func assertMalformedHandleError(t *testing.T, err error) {
	t.Helper()
	if err == nil || err.Error() != "terminal contract: malformed terminal payload" {
		t.Fatalf("handle error = %v, want canonical contract failure", err)
	}
}
