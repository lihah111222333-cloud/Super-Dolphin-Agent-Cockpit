package codexapp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

type codexTimestampFailureCase struct {
	name         string
	raw          dto.RawProviderEvent
	wantCode     string
	wantTerminal bool
}

func TestCodexTimestampProviderErrorForMissingLifecycleAndInvalidTerminal(t *testing.T) {
	for _, tc := range []codexTimestampFailureCase{
		{
			name: "missing lifecycle timestamp",
			raw: dto.RawProviderEvent{EventType: "thread/status/changed", Data: map[string]any{
				"agentId": "agent-1", "threadId": "thread-1", "sessionId": "session-1", "newState": "idle",
			}},
			wantCode: codexMissingTimestampCode,
		},
		{
			name: "invalid terminal timestamp",
			raw: dto.RawProviderEvent{EventType: "turn/completed", Data: map[string]any{
				"agentId": "agent-1", "threadId": "thread-1", "sessionId": "session-1", "turnId": "turn-1", "timestamp": "not-a-time",
			}},
			wantCode:     codexInvalidTimestampCode,
			wantTerminal: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) { assertCodexTimestampFailure(t, tc) })
	}
}

func assertCodexTimestampFailure(t *testing.T, tc codexTimestampFailureCase) {
	t.Helper()
	published := collectCodexAdapterEvents(tc.raw)
	wantCount := 2
	if tc.wantTerminal {
		wantCount = 3
	}
	if len(published) != wantCount {
		t.Fatalf("published events = %#v, want %d timestamp failure events", published, wantCount)
	}
	assertCodexTimestampRawAndAgentError(t, published, tc.wantCode)
	if tc.wantTerminal {
		assertCodexSafeTimestampTurnCompleted(t, published[2])
	}
}

func collectCodexAdapterEvents(raw dto.RawProviderEvent) []any {
	var published []any
	translateCodexAdapterEvent(raw, func(ev any) { published = append(published, ev) })
	return published
}

func assertCodexTimestampRawAndAgentError(t *testing.T, published []any, wantCode string) {
	t.Helper()
	if _, ok := published[0].(dto.BusRawProviderEvent); !ok {
		t.Fatalf("first event type = %T, want BusRawProviderEvent", published[0])
	}
	agentErr, ok := published[1].(agentdto.AgentError)
	if !ok {
		t.Fatalf("second event type = %T, want AgentError", published[1])
	}
	if agentErr.Code != wantCode || !strings.HasPrefix(agentErr.Message, "Provider reported an error. Diagnostic ID:") {
		t.Fatalf("AgentError = %#v, want timestamp validation code %q", agentErr, wantCode)
	}
	if strings.Contains(agentErr.Message, "not-a-time") || strings.Contains(agentErr.Message, "missing timestamp") {
		t.Fatalf("AgentError leaked raw timestamp failure: %#v", agentErr)
	}
}

func assertCodexSafeTimestampTurnCompleted(t *testing.T, value any) {
	t.Helper()
	terminal, ok := value.(turndto.TurnCompleted)
	if !ok || terminal.Success || terminal.Status != "failed" || strings.Contains(terminal.Error, "not-a-time") {
		t.Fatalf("timestamp terminal = %#v, want safe failed TurnCompleted", value)
	}
}

func TestMalformedTerminalTimestampEmitsSafeCanonicalEventSurfaceTerminal(t *testing.T) {
	dispatcher, terminals := newTimestampTerminalEventSurface(t)
	s, handle := newTimestampTerminalSession(dispatcher, "turn-1")
	s.onNotification("turn/completed", malformedTimestampTerminal(t, "thread-local", "turn-1", true))
	assertSafeTimestampEventSurfaceTerminal(t, waitTimestampTerminal(t, terminals))
	assertTimestampHandleFailed(t, handle)

	s.onNotification("turn/completed", malformedTimestampTerminal(t, "thread-local", "turn-1", true))
	assertNoTimestampTerminal(t, terminals, "duplicate bad-timestamp terminal")

	alienSession, alien := newTimestampTerminalSession(dispatcher, "turn-alien")
	alienSession.onNotification("turn/completed", malformedTimestampTerminal(t, "thread-alien", "turn-alien", false))
	assertNoTimestampTerminal(t, terminals, "alien terminal")
	assertTimestampHandleActive(t, alien)
}

func newTimestampTerminalEventSurface(t *testing.T) (*unified.EventDispatcher, <-chan turndto.TurnTerminalV2) {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	terminals := make(chan turndto.TurnTerminalV2, 2)
	for _, cancel := range eventsurface.Bind(bus, nil, timestampTerminalSubscriber(terminals)) {
		t.Cleanup(cancel)
	}
	return dispatcher, terminals
}

func timestampTerminalSubscriber(terminals chan<- turndto.TurnTerminalV2) func(string, any) {
	return func(method string, payload any) {
		if method != eventsurface.MethodTurnTerminal {
			return
		}
		if terminal, ok := payload.(turndto.TurnTerminalV2); ok {
			terminals <- terminal
		}
	}
}

func newTimestampTerminalSession(dispatcher *unified.EventDispatcher, turnID string) (*session, *turnHandle) {
	handle := newTurnHandle("local-"+turnID, turnID)
	s := &session{
		agentID:      "agent-public",
		dispatcher:   dispatcher,
		turns:        map[string]*turnHandle{turnID: handle},
		activeTurnID: turnID,
	}
	s.setThreadID("thread-local")
	return s, handle
}

func malformedTimestampTerminal(t *testing.T, threadID, turnID string, includeError bool) json.RawMessage {
	t.Helper()
	payload := map[string]any{
		"agentId": "provider-agent", "threadId": threadID, "turnId": turnID,
		"timestamp": "not-a-time", "success": true, "status": "completed",
	}
	if includeError {
		payload["error"] = "raw provider secret"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal malformed terminal: %v", err)
	}
	return encoded
}

func waitTimestampTerminal(t *testing.T, terminals <-chan turndto.TurnTerminalV2) turndto.TurnTerminalV2 {
	t.Helper()
	select {
	case terminal := <-terminals:
		return terminal
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for safe canonical terminal")
		return turndto.TurnTerminalV2{}
	}
}

func assertSafeTimestampEventSurfaceTerminal(t *testing.T, terminal turndto.TurnTerminalV2) {
	t.Helper()
	if terminal.ThreadID != "agent-public" || terminal.TurnID != "turn-1" || terminal.Outcome != "failed" || terminal.PublicError == nil {
		t.Fatalf("terminal = %#v, want safe failed canonical terminal", terminal)
	}
	encoded, err := json.Marshal(terminal)
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	if strings.Contains(string(encoded), "not-a-time") || strings.Contains(string(encoded), "raw provider secret") {
		t.Fatalf("canonical terminal leaked provider detail: %s", encoded)
	}
}

func assertTimestampHandleFailed(t *testing.T, handle contract.TurnHandle) {
	t.Helper()
	select {
	case <-handle.Done():
		if err := handle.Err(); err == nil || strings.Contains(err.Error(), "raw provider secret") {
			t.Fatalf("handle error = %v, want safe protocol failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bad terminal timestamp left handle active")
	}
}

func assertNoTimestampTerminal(t *testing.T, terminals <-chan turndto.TurnTerminalV2, description string) {
	t.Helper()
	select {
	case terminal := <-terminals:
		t.Fatalf("%s reached event surface: %#v", description, terminal)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertTimestampHandleActive(t *testing.T, handle contract.TurnHandle) {
	t.Helper()
	select {
	case <-handle.Done():
		t.Fatal("alien terminal finished local handle")
	default:
	}
}
