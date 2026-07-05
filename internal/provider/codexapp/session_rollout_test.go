package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/kelindar/event"
)

func TestOnNotification_CodexRolloutAssistantMessageCompletesActiveTurn(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	completedCh := make(chan turndto.TurnCompleted, 1)
	cancelCompleted := event.Subscribe(bus, func(ev turndto.TurnCompleted) { completedCh <- ev })
	defer cancelCompleted()

	fixture := newCountingForceCompleteFixture(t)
	defer fixture.close()
	s := newForceCompleteTestSession(t, fixture.url())
	defer closeCodexTestSession(t, s)
	s.dispatcher = dispatcher
	active := configureSingleForceCompleteTurn(s, "turn-1")

	s.onNotification("response_item", json.RawMessage(`{
		"type":"message",
		"role":"assistant",
		"content":[{"type":"output_text","text":"done from resumed rollout"}]
	}`))

	completed := waitRolloutTurnCompleted(t, completedCh)
	if completed.TurnID != "turn-1" || !completed.Success || completed.Status != "completed" {
		t.Fatalf("TurnCompleted = %+v, want successful completed turn-1", completed)
	}
	if completed.Result != "done from resumed rollout" {
		t.Fatalf("TurnCompleted.Result = %q, want assistant message text", completed.Result)
	}
	assertTurnDone(t, active, "rollout assistant message did not complete active turn")
	s.mu.Lock()
	activeTurnID := s.activeTurnID
	_, stillTracked := s.turns["turn-1"]
	s.mu.Unlock()
	if activeTurnID != "" || stillTracked {
		t.Fatalf("active turn state = id:%q tracked:%v, want cleared", activeTurnID, stillTracked)
	}
	err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{ThreadID: "thread-1"})
	if !errors.Is(err, ErrForceCompleteTargetNotFound) {
		t.Fatalf("ForceComplete() after rollout completion error = %v, want ErrForceCompleteTargetNotFound", err)
	}
	fixture.assertNoForceComplete(t)
}

func TestOnNotification_CodexAssistantItemCompletedCompletesActiveTurnFromAccumulator(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	completedCh := make(chan turndto.TurnCompleted, 1)
	cancelCompleted := event.Subscribe(bus, func(ev turndto.TurnCompleted) { completedCh <- ev })
	defer cancelCompleted()
	toolEndCh := make(chan tooldto.ToolCallEnd, 1)
	cancelToolEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { toolEndCh <- ev })
	defer cancelToolEnd()
	defer func() { _ = bus.Close() }()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher
	active := configureSingleForceCompleteTurn(s, "turn-1")

	s.onNotification("item/agentMessage/delta", json.RawMessage(`{
		"agentId":"agent-1",
		"threadId":"provider-thread-1",
		"turnId":"turn-1",
		"stream":"message",
		"delta":"你好"
	}`))
	s.onNotification("item/completed", json.RawMessage(`{
		"agentId":"agent-1",
		"threadId":"provider-thread-1",
		"turnId":"turn-1",
		"item":{"type":"agent_message","role":"assistant","content":[]}
	}`))

	completed := waitRolloutTurnCompleted(t, completedCh)
	if completed.TurnID != "turn-1" || !completed.Success || completed.Status != "completed" {
		t.Fatalf("TurnCompleted = %+v, want successful completed turn-1", completed)
	}
	if completed.Result != "你好" {
		t.Fatalf("TurnCompleted.Result = %q, want accumulated assistant message", completed.Result)
	}
	assertTurnDone(t, active, "assistant item/completed did not complete active turn")
	assertNoToolCallEnd(t, toolEndCh)
}

func TestOnNotification_CodexAssistantItemCompletedDoesNotCompleteToolItem(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	completedCh := make(chan turndto.TurnCompleted, 1)
	cancelCompleted := event.Subscribe(bus, func(ev turndto.TurnCompleted) { completedCh <- ev })
	defer cancelCompleted()
	toolEndCh := make(chan tooldto.ToolCallEnd, 1)
	cancelToolEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { toolEndCh <- ev })
	defer cancelToolEnd()
	defer func() { _ = bus.Close() }()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher
	configureSingleForceCompleteTurn(s, "turn-1")

	s.onNotification("item/completed", json.RawMessage(`{
		"agentId":"agent-1",
		"threadId":"provider-thread-1",
		"turnId":"turn-1",
		"callId":"call-file",
		"name":"file",
		"success":true,
		"result":{"ok":true}
	}`))

	end := waitToolCallEnd(t, toolEndCh)
	if end.CallID != "call-file" || end.ToolName != "file" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful call-file/file", end)
	}
	assertNoRolloutTurnCompleted(t, completedCh)
}

func TestSyntheticAssistantCompletionPreservesToolFailure(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	completedCh := make(chan turndto.TurnCompleted, 1)
	cancelCompleted := event.Subscribe(bus, func(ev turndto.TurnCompleted) { completedCh <- ev })
	defer cancelCompleted()
	toolEndCh := make(chan tooldto.ToolCallEnd, 1)
	cancelToolEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { toolEndCh <- ev })
	defer cancelToolEnd()
	defer func() { _ = bus.Close() }()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher
	active := newTurnHandle("local-1", "turn-1")
	s.mu.Lock()
	s.turns["turn-1"] = active
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	s.dispatch(dto.RawProviderEvent{
		EventType: "tool.call.end",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "provider-thread-1",
			"turnId":   "turn-1",
			"callId":   "call-file",
			"name":     "file",
			"success":  false,
			"error":    "file read failed",
		},
	})
	toolEnd := waitToolCallEnd(t, toolEndCh)
	if toolEnd.Success || toolEnd.Error != "file read failed" {
		t.Fatalf("ToolCallEnd = %+v, want failed file read", toolEnd)
	}

	s.completeSyntheticTurn("turn-1", "rollout_assistant_message", "assistant text")

	completed := waitRolloutTurnCompleted(t, completedCh)
	if completed.Success || completed.Status != "completed_with_errors" {
		t.Fatalf("TurnCompleted = %+v, want completed_with_errors", completed)
	}
	for _, want := range []string{"call-file", "file", "file read failed"} {
		if !strings.Contains(completed.Error, want) {
			t.Fatalf("TurnCompleted.Error = %q, want %q correlation", completed.Error, want)
		}
	}
	if completed.Result != "assistant text" {
		t.Fatalf("TurnCompleted.Result = %q, want assistant text", completed.Result)
	}
	assertTurnDone(t, active, "synthetic completion with tool failure did not complete active turn")
	if active.Err() == nil || !strings.Contains(active.Err().Error(), "file read failed") {
		t.Fatalf("turn handle error = %v, want correlated tool failure", active.Err())
	}
}

func waitRolloutTurnCompleted(t *testing.T, ch <-chan turndto.TurnCompleted) turndto.TurnCompleted {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TurnCompleted")
		return turndto.TurnCompleted{}
	}
}

func assertNoRolloutTurnCompleted(t *testing.T, ch <-chan turndto.TurnCompleted) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected TurnCompleted = %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnNotification_CodexRolloutMCPToolEventsDispatchToolLifecycle(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher

	s.onNotification("response_item", json.RawMessage(`{
		"type":"function_call",
		"name":"file",
		"namespace":"mcp__lsp__",
		"arguments":"{\"action\":\"read_file\",\"file_path\":\"smoke.go\"}",
		"call_id":"call-file"
	}`))
	begin := waitToolCallBegin(t, beginCh)
	if begin.CallID != "call-file" || begin.ToolName != "mcp__lsp__file" {
		t.Fatalf("ToolCallBegin = %+v, want call-file/mcp__lsp__file", begin)
	}

	s.onNotification("event_msg", json.RawMessage(`{
		"type":"mcp_tool_call_end",
		"call_id":"call-file",
		"invocation":{"server":"lsp","tool":"file","arguments":{"action":"read_file","file_path":"smoke.go"}},
		"duration":{"secs":0,"nanos":2062541},
		"result":{"Ok":{"content":[{"type":"text","text":"\" 1: package main\\n\""}],"structuredContent":{"value":" 1: package main\n"},"isError":false}}
	}`))
	end := waitToolCallEnd(t, endCh)
	if end.CallID != "call-file" || end.ToolName != "mcp__lsp__file" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful call-file/mcp__lsp__file", end)
	}
	if !strings.Contains(end.Result, "package main") {
		t.Fatalf("Result = %q, want file preview", end.Result)
	}
}

func TestOnNotification_CodexRolloutToolResultReusesBeginToolName(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher

	s.onNotification("response_item", json.RawMessage(`{
		"item":{
			"type":"tool_call",
			"name":"file",
			"arguments":{"action":"read_file","file_path":"smoke.go"},
			"call_id":"call-file"
		}
	}`))
	begin := waitToolCallBegin(t, beginCh)
	if begin.CallID != "call-file" || begin.ToolName != "file" {
		t.Fatalf("ToolCallBegin = %+v, want call-file/file", begin)
	}

	s.onNotification("event_msg", json.RawMessage(`{
		"item":{
			"type":"tool_result",
			"call_id":"call-file",
			"invocation":{"server":"lsp","tool":"file","arguments":{"action":"read_file","file_path":"smoke.go"}},
			"duration":{"secs":0,"nanos":2062541},
			"result":{"Ok":{"content":[{"type":"text","text":"plain fallback"}],"structuredContent":{"value":" 1: package main\n"},"isError":false}}
		}
	}`))
	end := waitToolCallEnd(t, endCh)
	if end.CallID != begin.CallID || end.ToolName != begin.ToolName || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful %s/%s", end, begin.CallID, begin.ToolName)
	}
}

func TestOnNotification_CodexRolloutMCPFileReadEmptySuccessResultFailsWithPathGuidance(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher

	s.onNotification("response_item", json.RawMessage(`{
		"item":{
			"type":"tool_call",
			"name":"file",
			"arguments":{"action":"read_file","file_path":"/home/user/Downloads/missing.md"},
			"call_id":"call-file"
		}
	}`))
	begin := waitToolCallBegin(t, beginCh)
	if begin.CallID != "call-file" || begin.ToolName != "file" {
		t.Fatalf("ToolCallBegin = %+v, want call-file/file", begin)
	}

	s.onNotification("event_msg", json.RawMessage(`{
		"item":{
			"type":"tool_result",
			"call_id":"call-file",
			"invocation":{"server":"lsp","tool":"file","arguments":{"action":"read_file","file_path":"/home/user/Downloads/missing.md"}},
			"result":{"Ok":{"content":[{"type":"text","text":""}],"structuredContent":{"value":""},"isError":false}}
		}
	}`))
	end := waitToolCallEnd(t, endCh)
	if end.Success {
		t.Fatalf("ToolCallEnd.Success = true, want false for empty file read result: %+v", end)
	}
	for _, want := range []string{"missing.md", "does not exist", "outside workspace"} {
		if !strings.Contains(end.Error, want) {
			t.Fatalf("ToolCallEnd.Error = %q, want %q", end.Error, want)
		}
	}
	if strings.Contains(end.Result, `"value":""`) {
		t.Fatalf("ToolCallEnd.Result = %q, want path guidance instead of empty structuredContent", end.Result)
	}
}

func TestOnNotification_CodexRolloutResponseItemToolResultDispatchesToolEnd(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher

	s.onNotification("response_item", json.RawMessage(`{
		"item":{
			"type":"tool_call",
			"name":"file",
			"arguments":{"action":"read_file","file_path":"smoke.go"},
			"call_id":"call-file"
		}
	}`))
	begin := waitToolCallBegin(t, beginCh)
	if begin.CallID != "call-file" || begin.ToolName != "file" {
		t.Fatalf("ToolCallBegin = %+v, want call-file/file", begin)
	}

	s.onNotification("response_item", json.RawMessage(`{
		"item":{
			"type":"tool_result",
			"call_id":"call-file",
			"invocation":{"server":"lsp","tool":"file"},
			"result":{
				"content":[{"type":"text","text":"plain fallback"}],
				"structuredContent":{"success":false,"error":"direct mcp failure","path":"smoke.go"},
				"isError":true
			}
		}
	}`))
	end := waitToolCallEnd(t, endCh)
	if end.CallID != begin.CallID || end.ToolName != begin.ToolName {
		t.Fatalf("ToolCallEnd = %+v, want begin key %s/%s", end, begin.CallID, begin.ToolName)
	}
	if end.Success || end.Error != "direct mcp failure" {
		t.Fatalf("ToolCallEnd = %+v, want direct MCP failure", end)
	}
	if !strings.Contains(end.Result, `"path":"smoke.go"`) || strings.Contains(end.Result, "plain fallback") {
		t.Fatalf("Result = %q, want structuredContent preview before content text", end.Result)
	}
}

func TestOnNotification_CodexRolloutResponseItemFunctionCallOutputDispatchesToolEnd(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher

	s.onNotification("response_item", json.RawMessage(`{
		"item":{"type":"tool_call","name":"file","arguments":{"action":"read_file"},"call_id":"call-file"}
	}`))
	begin := waitToolCallBegin(t, beginCh)
	if begin.ToolName != "file" {
		t.Fatalf("ToolCallBegin = %+v, want file", begin)
	}

	s.onNotification("response_item", json.RawMessage(`{
		"item":{"type":"function_call_output","call_id":"call-file","output":"{\"success\":true,\"path\":\"smoke.go\"}"}
	}`))
	end := waitToolCallEnd(t, endCh)
	if end.CallID != begin.CallID || end.ToolName != begin.ToolName || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful %s/%s", end, begin.CallID, begin.ToolName)
	}
	if !strings.Contains(end.Result, "smoke.go") {
		t.Fatalf("Result = %q, want function_call_output text", end.Result)
	}
}

func TestOnNotification_CodexRolloutFunctionCallOutputAfterResultDoesNotPublishNamelessEnd(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 2)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	s := newInboundTestSession(context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher

	s.onNotification("response_item", json.RawMessage(`{
		"item":{"type":"tool_call","name":"file","arguments":{"action":"read_file"},"call_id":"call-file"}
	}`))
	begin := waitToolCallBegin(t, beginCh)
	if begin.ToolName != "file" {
		t.Fatalf("ToolCallBegin = %+v, want file", begin)
	}

	s.onNotification("event_msg", json.RawMessage(`{
		"item":{"type":"tool_result","call_id":"call-file","invocation":{"server":"lsp","tool":"file"},"result":{"Ok":{"structuredContent":{"success":true},"isError":false}}}
	}`))
	end := waitToolCallEnd(t, endCh)
	if end.ToolName != begin.ToolName {
		t.Fatalf("ToolCallEnd = %+v, want begin tool name %q", end, begin.ToolName)
	}

	s.onNotification("event_msg", json.RawMessage(`{
		"item":{"type":"function_call_output","call_id":"call-file","output":"{\"success\":true}"}
	}`))
	assertNoToolCallEnd(t, endCh)
}

func TestOnInboundMessage_ToolsCallSuppressesDuplicateRolloutToolResult(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	ctx := context.Background()
	endCh := make(chan tooldto.ToolCallEnd, 2)
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(ctx, nil, manager)
	s.dispatcher = dispatcher
	s.mu.Lock()
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`"req-1"`),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"file","arguments":{"action":"read_file","file_path":"smoke.go"}}`),
	})
	_ = waitResponseCall(t, resp.ch)
	first := waitToolCallEnd(t, endCh)
	if first.CallID != "req-1" || first.ToolName != "file" {
		t.Fatalf("first ToolCallEnd = %+v, want req-1/file", first)
	}

	s.onNotification("event_msg", json.RawMessage(`{
		"item":{
			"type":"tool_result",
			"call_id":"req-1",
			"invocation":{"server":"lsp","tool":"file"},
			"result":{"Ok":{"structuredContent":{"success":true},"isError":false}}
		}
	}`))
	assertNoToolCallEnd(t, endCh)
}

func TestOnInboundMessage_ToolsCallSuppressesDuplicateResponseItemToolResult(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	ctx := context.Background()
	endCh := make(chan tooldto.ToolCallEnd, 2)
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(ctx, nil, manager)
	s.dispatcher = dispatcher
	s.mu.Lock()
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`"req-1"`),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"file","arguments":{"action":"read_file","file_path":"smoke.go"}}`),
	})
	_ = waitResponseCall(t, resp.ch)
	first := waitToolCallEnd(t, endCh)
	if first.CallID != "req-1" || first.ToolName != "file" {
		t.Fatalf("first ToolCallEnd = %+v, want req-1/file", first)
	}

	s.onNotification("response_item", json.RawMessage(`{
		"item":{
			"type":"tool_result",
			"call_id":"req-1",
			"invocation":{"server":"lsp","tool":"file"},
			"result":{"structuredContent":{"success":true},"isError":false}
		}
	}`))
	assertNoToolCallEnd(t, endCh)
}

func TestOnInboundMessage_ToolsCallSuppressesDuplicateRolloutToolResultAliases(t *testing.T) {
	for _, method := range []string{"item_completed", "agent/event/item_completed", "rawResponseItem/completed"} {
		t.Run(method, func(t *testing.T) {
			bus := event.NewDispatcher()
			dispatcher := unified.NewEventDispatcher(bus, nil)
			RegisterTranslators(dispatcher)
			ctx := context.Background()
			endCh := make(chan tooldto.ToolCallEnd, 2)
			cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
			defer cancelEnd()

			manager := &ServerManager{}
			manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
				return map[string]any{"ok": true}, nil
			})
			resp := newRecordingResponder()
			s := newInboundTestSession(ctx, nil, manager)
			s.dispatcher = dispatcher
			s.mu.Lock()
			s.activeTurnID = "turn-1"
			s.mu.Unlock()

			s.onInboundMessage(ctx, resp, RawMessage{
				ID:     json.RawMessage(`"req-1"`),
				Method: "tools/call",
				Params: json.RawMessage(`{"name":"file","arguments":{"action":"read_file","file_path":"smoke.go"}}`),
			})
			_ = waitResponseCall(t, resp.ch)
			first := waitToolCallEnd(t, endCh)
			if first.CallID != "req-1" || first.ToolName != "file" {
				t.Fatalf("first ToolCallEnd = %+v, want req-1/file", first)
			}

			s.onNotification(method, json.RawMessage(`{
				"item":{
					"type":"tool_result",
					"call_id":"req-1",
					"invocation":{"server":"lsp","tool":"file"},
					"result":{"Ok":{"structuredContent":{"success":true},"isError":false}}
				}
			}`))
			assertNoToolCallEnd(t, endCh)
		})
	}
}
