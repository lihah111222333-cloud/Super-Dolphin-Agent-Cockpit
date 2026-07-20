package claudecli

import (
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestEventTimeMissingTimestampIsZero(t *testing.T) {
	t.Parallel()

	if got := eventTime(map[string]any{"thread_id": "thread-1"}); !got.IsZero() {
		t.Fatalf("eventTime() = %v, want zero time for missing timestamp", got)
	}
}

func TestEventTimeInvalidTimestampIsZero(t *testing.T) {
	t.Parallel()

	if got := eventTime(map[string]any{"timestamp": "not-a-time"}); !got.IsZero() {
		t.Fatalf("eventTime() = %v, want zero time for invalid timestamp", got)
	}
}

type claudeTerminalFixture struct {
	name, eventType, outcome, cause, requestID string
	publicError                                bool
	data                                       map[string]any
}

type claudeTerminalPublication struct {
	method  string
	payload turndto.TurnTerminalV2
}

func TestClaudeTerminalFixturesReachCanonicalEventSurface(t *testing.T) {
	t.Parallel()

	fixtures := []claudeTerminalFixture{
		{name: "success", eventType: "turn:complete", outcome: "success", data: map[string]any{"success": true, "status": "completed"}},
		{name: "failed", eventType: "turn:complete", outcome: "failed", publicError: true, data: map[string]any{"success": false, "status": "failed", "error": "raw provider failure"}},
		{name: "user interrupted", eventType: "turn:interrupted", outcome: "interrupted", cause: "user_request", requestID: "stop-1", data: map[string]any{"termination_cause": "user_request", "termination_request_id": "stop-1", "reason": "ui stop"}},
		{name: "provider cancelled", eventType: "turn:complete", outcome: "cancelled", cause: "provider", publicError: true, data: map[string]any{"success": false, "status": "cancelled", "termination_cause": "provider", "error": "provider cancelled"}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			assertClaudeTerminalFixture(t, fixture, publishClaudeTerminalFixture(t, fixture))
		})
	}
}

func publishClaudeTerminalFixture(t *testing.T, fixture claudeTerminalFixture) claudeTerminalPublication {
	t.Helper()
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	published := make(chan claudeTerminalPublication, 1)
	cancels := eventsurface.Bind(bus, nil, func(method string, payload any) {
		terminal, ok := payload.(turndto.TurnTerminalV2)
		if ok {
			published <- claudeTerminalPublication{method: method, payload: terminal}
		}
	})
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	rawData := map[string]any{
		"agent_id": "agent-1", "thread_id": "thread-1", "turn_id": "turn-1",
		"timestamp": "2026-07-16T10:11:12.123Z",
	}
	maps.Copy(rawData, fixture.data)
	dispatcher.Dispatch(dto.RawProviderEvent{EventType: fixture.eventType, Data: rawData})

	select {
	case got := <-published:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canonical turn terminal")
		return claudeTerminalPublication{}
	}
}

func assertClaudeTerminalFixture(t *testing.T, fixture claudeTerminalFixture, got claudeTerminalPublication) {
	t.Helper()
	if got.method != eventsurface.MethodTurnTerminal {
		t.Fatalf("method = %q, want %q", got.method, eventsurface.MethodTurnTerminal)
	}
	terminal := got.payload
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		t.Fatalf("ValidateTurnTerminalV2() error = %v; payload=%#v", err, terminal)
	}
	identity := struct {
		schemaVersion             int
		eventID, threadID, turnID string
		outcome, occurredAt       string
	}{terminal.SchemaVersion, terminal.EventID, terminal.ThreadID, terminal.TurnID, terminal.Outcome, terminal.OccurredAt}
	wantIdentity := struct {
		schemaVersion             int
		eventID, threadID, turnID string
		outcome, occurredAt       string
	}{2, terminal.EventID, "thread-1", "turn-1", fixture.outcome, "2026-07-16T10:11:12.123Z"}
	if terminal.EventID == "" || identity != wantIdentity {
		t.Fatalf("terminal identity/outcome = %#v", terminal)
	}
	if terminal.TerminationCause != fixture.cause || terminal.TerminationRequestID != fixture.requestID {
		t.Fatalf("terminal termination = (%q, %q), want (%q, %q)", terminal.TerminationCause, terminal.TerminationRequestID, fixture.cause, fixture.requestID)
	}
	if (terminal.PublicError != nil) != fixture.publicError {
		t.Fatalf("terminal PublicError = %#v, want present=%v", terminal.PublicError, fixture.publicError)
	}
}

func TestRegisterTranslatorsPublishesProviderErrorForMissingEventTimestamp(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	agentErrors := make(chan agentdto.AgentError, 1)
	runtimes := make(chan agentdto.AgentRuntimeReported, 1)
	cancelErr := event.Subscribe(bus, func(ev agentdto.AgentError) { agentErrors <- ev })
	defer cancelErr()
	cancelRuntime := event.Subscribe(bus, func(ev agentdto.AgentRuntimeReported) { runtimes <- ev })
	defer cancelRuntime()

	dispatcher.Dispatch(dto.RawProviderEvent{
		EventType: "system:init",
		Data: map[string]any{
			"agent_id":   "agent-1",
			"thread_id":  "thread-1",
			"session_id": "session-1",
		},
	})

	got := requireAgentError(t, agentErrors)
	if got.AgentID != "agent-1" || got.ThreadID != "thread-1" || got.Code != "claude_missing_timestamp" {
		t.Fatalf("AgentError = %#v, want agent/thread and missing timestamp code", got)
	}
	if !strings.HasPrefix(got.Message, "Provider reported an error. Diagnostic ID: ") || strings.Contains(got.Message, "missing timestamp") {
		t.Fatalf("AgentError.Message = %q, want public diagnostic", got.Message)
	}
	select {
	case ev := <-runtimes:
		t.Fatalf("unexpected AgentRuntimeReported for bad timestamp = %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRegisterTranslatorsPublishesProviderErrorForInvalidToolTimestamp(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	agentErrors := make(chan agentdto.AgentError, 1)
	toolEnds := make(chan tooldto.ToolCallEnd, 1)
	cancelErr := event.Subscribe(bus, func(ev agentdto.AgentError) { agentErrors <- ev })
	defer cancelErr()
	cancelTool := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { toolEnds <- ev })
	defer cancelTool()

	dispatcher.Dispatch(dto.RawProviderEvent{
		EventType: "tool:use_end",
		Data: map[string]any{
			"agent_id":   "agent-1",
			"thread_id":  "thread-1",
			"session_id": "session-1",
			"turn_id":    "turn-1",
			"call_id":    "call-1",
			"tool_name":  "Read",
			"timestamp":  "not-a-time",
		},
	})

	got := requireAgentError(t, agentErrors)
	if got.Code != "claude_invalid_timestamp" {
		t.Fatalf("AgentError.Code = %q, want claude_invalid_timestamp", got.Code)
	}
	if !strings.HasPrefix(got.Message, "Provider reported an error. Diagnostic ID: ") || strings.Contains(got.Message, "invalid timestamp") {
		t.Fatalf("AgentError.Message = %q, want public diagnostic", got.Message)
	}
	select {
	case ev := <-toolEnds:
		t.Fatalf("unexpected ToolCallEnd for bad timestamp = %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestToolCallEndReportsPersistFailure 确认 Claude 工具结束事件透传结果持久化失败诊断。
func TestToolCallEndReportsPersistFailure(t *testing.T) {
	configureCaptureRuntimeHookForTest(t, func(providershared.ToolResultMeta, string) (providershared.ToolResultRecord, error) {
		return providershared.ToolResultRecord{Preview: "captured", PersistFailed: true, PersistError: "disk full"}, nil
	})
	ev, ok := translateToolEvent(dto.RawProviderEvent{EventType: "tool:use_end", Data: map[string]any{"thread_id": "thread-1", "turn_id": "turn-1", "call_id": "call-1", "tool_name": "Read", "success": true, "result": "raw"}})
	if !ok {
		t.Fatal("translateToolEvent() ok=false, want ToolCallEnd")
	}
	end, ok := ev.(tooldto.ToolCallEnd)
	if !ok || end.Result != "captured" || !end.PersistFailed || !strings.HasPrefix(end.PersistError, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.PersistError, "disk full") {
		t.Fatalf("ToolCallEnd = %+v, want persist failure fields", ev)
	}
}

// TestToolCallEndFailsWhenRuntimeCaptureFails 验证捕获依赖错误不会退化成成功事件。
func TestToolCallEndFailsWhenRuntimeCaptureFails(t *testing.T) {
	configureCaptureRuntimeHookForTest(t, func(providershared.ToolResultMeta, string) (providershared.ToolResultRecord, error) {
		return providershared.ToolResultRecord{}, errors.New("capture unavailable")
	})

	ev, ok := translateToolEvent(dto.RawProviderEvent{EventType: "tool:use_end", Data: map[string]any{
		"thread_id": "thread-1",
		"turn_id":   "turn-1",
		"call_id":   "call-1",
		"tool_name": "Read",
		"success":   true,
		"result":    "raw",
	}})
	if !ok {
		t.Fatal("translateToolEvent() ok = false, want ToolCallEnd")
	}
	end, ok := ev.(tooldto.ToolCallEnd)
	if !ok || end.Success || !strings.HasPrefix(end.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.Error, "capture unavailable") {
		t.Fatalf("ToolCallEnd = %+v, want public capture failure", ev)
	}
}

func requireAgentError(t *testing.T, ch <-chan agentdto.AgentError) agentdto.AgentError {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("AgentError was not published")
		return agentdto.AgentError{}
	}
}
