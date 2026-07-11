package claudecli

import (
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
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
	if !strings.Contains(got.Message, "missing timestamp") {
		t.Fatalf("AgentError.Message = %q, want missing timestamp", got.Message)
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
	if !strings.Contains(got.Message, "invalid timestamp") {
		t.Fatalf("AgentError.Message = %q, want invalid timestamp", got.Message)
	}
	select {
	case ev := <-toolEnds:
		t.Fatalf("unexpected ToolCallEnd for bad timestamp = %#v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestToolCallEndReportsPersistFailure 确认 Claude 工具结束事件透传结果持久化失败诊断。
func TestToolCallEndReportsPersistFailure(t *testing.T) {
	providershared.SetCaptureToolResultHook(func(providershared.ToolResultMeta, string) providershared.ToolResultRecord {
		return providershared.ToolResultRecord{Preview: "captured", PersistFailed: true, PersistError: "disk full"}
	})
	t.Cleanup(func() { providershared.SetCaptureToolResultHook(nil) })
	ev, ok := translateToolEvent(dto.RawProviderEvent{EventType: "tool:use_end", Data: map[string]any{"thread_id": "thread-1", "turn_id": "turn-1", "call_id": "call-1", "tool_name": "Read", "success": true, "result": "raw"}})
	if !ok {
		t.Fatal("translateToolEvent() ok=false, want ToolCallEnd")
	}
	end, ok := ev.(tooldto.ToolCallEnd)
	if !ok || end.Result != "captured" || !end.PersistFailed || end.PersistError != "disk full" {
		t.Fatalf("ToolCallEnd = %+v, want persist failure fields", ev)
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
