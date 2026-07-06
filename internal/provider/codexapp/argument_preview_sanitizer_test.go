package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/kelindar/event"
)

func TestTranslateToolCallBeginSanitizesArgumentsPreview(t *testing.T) {
	ev, ok := translateToolEvent("tool.call.begin", map[string]any{
		"threadId":  "thread-1",
		"agentId":   "agent-1",
		"turnId":    "turn-1",
		"callId":    "call-1",
		"toolName":  "shell",
		"arguments": sensitiveCodexArguments(),
	})
	if !ok {
		t.Fatal("translateToolEvent() ok=false, want ToolCallBegin")
	}
	begin, ok := ev.(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", ev)
	}
	assertCodexArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func TestPublishToolCallBeginSanitizesArgumentsPreview(t *testing.T) {
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher
	msg := rawParams(t, map[string]any{
		"name":      "shell",
		"arguments": sensitiveCodexArguments(),
	})
	s.publishToolCallBegin(preparedToolCall{
		header:  toolCallHeader("agent-1", "turn-1", "call-1", "shell", time.Now()),
		params:  msg,
		started: time.Now(),
	})

	begin := waitToolCallBegin(t, beginCh)
	assertCodexArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func TestTranslateCodexRolloutFunctionCallSanitizesArgumentsPreview(t *testing.T) {
	rawArguments, err := json.Marshal(sensitiveCodexArguments())
	if err != nil {
		t.Fatalf("marshal sensitive arguments: %v", err)
	}
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "response_item",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"type":      "function_call",
			"name":      "shell",
			"arguments": string(rawArguments),
			"call_id":   "call-1",
		},
	}, func(ev any) { got = append(got, ev) })
	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	begin, ok := got[0].(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", got[0])
	}
	assertCodexArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func sensitiveCodexArguments() map[string]any {
	return map[string]any{
		"command":   "curl --api-key sk-test https://example.test",
		"token":     "token=abc",
		"file_path": "/Users/mima0000/secret",
	}
}

func assertCodexArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"token=abc", "sk-test", "/Users/mima0000/secret", "--api-key"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("ArgumentsPreview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("ArgumentsPreview = %q, want redaction marker", preview)
	}
}
