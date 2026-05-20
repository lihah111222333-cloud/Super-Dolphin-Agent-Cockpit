package codexapp

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/kelindar/event"
)

type recordedResponse struct {
	id     json.RawMessage
	result any
	err    error
}

type recordingResponder struct {
	mu    sync.Mutex
	calls []recordedResponse
	ch    chan recordedResponse
}

func newRecordingResponder() *recordingResponder {
	return &recordingResponder{ch: make(chan recordedResponse, 8)}
}

func (r *recordingResponder) RespondWithID(id json.RawMessage, result any, callErr error) error {
	call := recordedResponse{id: append(json.RawMessage(nil), id...), result: result, err: callErr}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	select {
	case r.ch <- call:
	default:
	}
	return nil
}

func (r *recordingResponder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func newInboundTestSession(ctx context.Context, approvals *rpc.ApprovalManager, manager *ServerManager) *session {
	s := &session{
		agentID:    "agent-1",
		approvals:  approvals,
		ctx:        ctx,
		manager:    manager,
		suppressed: map[string]struct{}{},
		turns:      map[string]*turnHandle{},
	}
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{"cwd": "/trusted/root"})
	return s
}

func waitApprovalRequested(t *testing.T, ch <-chan tooldto.ToolApprovalRequested) tooldto.ToolApprovalRequested {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
		return tooldto.ToolApprovalRequested{}
	}
}

func waitResponseCall(t *testing.T, ch <-chan recordedResponse) recordedResponse {
	t.Helper()
	select {
	case call := <-ch:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RespondWithID")
		return recordedResponse{}
	}
}

func TestOnInboundMessage_Approval_ViaApprovalBridge(t *testing.T) {
	bus := event.NewDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	requested := make(chan tooldto.ToolApprovalRequested, 1)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) { requested <- ev })
	defer cancelSub()

	var handlerCalls atomic.Int32
	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		handlerCalls.Add(1)
		return nil, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(ctx, rpc.NewApprovalManager(nil, bus), manager)

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`1`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"requestId":1,"command":"echo hi","toolName":"shell","turnId":"turn-1"}`),
	})

	ev := waitApprovalRequested(t, requested)
	if ev.RequestID != 1 {
		t.Fatalf("RequestID = %d, want 1", ev.RequestID)
	}
	if handlerCalls.Load() != 0 {
		t.Fatalf("toolHandler calls = %d, want 0", handlerCalls.Load())
	}
	if resp.callCount() != 0 {
		t.Fatalf("RespondWithID calls = %d, want 0", resp.callCount())
	}
}

func TestOnInboundMessage_RequestUserInput_ViaApprovalBridge(t *testing.T) {
	bus := event.NewDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	requested := make(chan tooldto.ToolApprovalRequested, 1)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) { requested <- ev })
	defer cancelSub()

	var handlerCalls atomic.Int32
	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		handlerCalls.Add(1)
		return nil, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(ctx, rpc.NewApprovalManager(nil, bus), manager)

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`2`),
		Method: "request_user_input",
		Params: json.RawMessage(`{"requestId":2,"message":"continue","turnId":"turn-1"}`),
	})

	ev := waitApprovalRequested(t, requested)
	if ev.RequestID != 2 {
		t.Fatalf("RequestID = %d, want 2", ev.RequestID)
	}
	if ev.Kind != "request_user_input" {
		t.Fatalf("Kind = %q, want request_user_input", ev.Kind)
	}
	if handlerCalls.Load() != 0 {
		t.Fatalf("toolHandler calls = %d, want 0", handlerCalls.Load())
	}
	if resp.callCount() != 0 {
		t.Fatalf("RespondWithID calls = %d, want 0", resp.callCount())
	}
}

func TestOnInboundMessage_ToolCall_AsyncNoBlockReadLoop(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	release := make(chan struct{})
	handlerDone := make(chan struct{})
	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		close(started)
		<-release
		close(handlerDone)
		return "tool-result", nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(ctx, nil, manager)

	returned := make(chan struct{})
	go func() {
		s.onInboundMessage(ctx, resp, RawMessage{
			ID:     json.RawMessage(`9`),
			Method: "item/tool/call",
			Params: json.RawMessage(`{"tool":"demo"}`),
		})
		close(returned)
	}()

	waitSignal(t, returned, 100*time.Millisecond, "onInboundMessage blocked on toolHandler")
	assertNoEarlyToolResponse(t, resp.ch)
	waitSignal(t, started, time.Second, "toolHandler did not start")

	close(release)
	waitSignal(t, handlerDone, time.Second, "toolHandler did not finish")

	call := waitResponseCall(t, resp.ch)
	assertAsyncToolResponse(t, call)
}

func TestOnInboundMessage_ToolCall_DispatchesLifecycleAndPrivateMetadata(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	ctx := context.Background()
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	enrichedParams := make(chan map[string]any, 1)
	manager := &ServerManager{}
	manager.SetToolHandler(func(_ context.Context, msg RawMessage) (any, error) {
		var payload map[string]any
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			t.Fatalf("json.Unmarshal(enriched params) error = %v", err)
		}
		enrichedParams <- payload
		return map[string]any{"ok": true}, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(ctx, nil, manager)
	s.dispatcher = dispatcher
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{"cwd": "/trusted/root"})
	s.mu.Lock()
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`9`),
		Method: "item/tool/call",
		Params: json.RawMessage(`{"name":"grep","arguments":{"cwd":"/evil"},"agentId":"evil-agent","threadId":"evil-thread","callId":"evil-call","cwd":"/evil"}`),
	})

	begin := waitToolCallBegin(t, beginCh)
	assertSyntheticToolBeginHeader(t, begin)
	params := waitEnrichedToolParams(t, enrichedParams)
	assertStrictPrivateMetadata(t, params)
	call := waitResponseCall(t, resp.ch)
	assertAsyncToolResponseResult(t, call, map[string]any{"ok": true})
	end := waitToolCallEnd(t, endCh)
	assertSyntheticToolEndHeader(t, end, begin)
}

func TestOnInboundMessage_ToolCall_ResultSuccessFalseDispatchesFailedLifecycle(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	ctx := context.Background()
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		return map[string]any{
			"success": false,
			"error":   "lsp peer unavailable",
			"contentItems": []map[string]any{{
				"type": "inputText",
				"text": `{"success":false,"error":"lsp peer unavailable"}`,
			}},
		}, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(ctx, nil, manager)
	s.dispatcher = dispatcher

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`10`),
		Method: "item/tool/call",
		Params: json.RawMessage(`{"name":"grep","arguments":{}}`),
	})

	call := waitResponseCall(t, resp.ch)
	if call.err != nil {
		t.Fatalf("response err = %v, want nil tool response transport error", call.err)
	}
	end := waitToolCallEnd(t, endCh)
	if end.Success {
		t.Fatalf("ToolCallEnd success = true, result = %s", end.Result)
	}
	if end.Error != "lsp peer unavailable" {
		t.Fatalf("ToolCallEnd error = %q, want lsp peer unavailable", end.Error)
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal(msg)
	}
}

func assertNoEarlyToolResponse(t *testing.T, ch <-chan recordedResponse) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("RespondWithID returned before toolHandler completed")
	case <-time.After(50 * time.Millisecond):
	}
}

func assertAsyncToolResponse(t *testing.T, call recordedResponse) {
	t.Helper()
	if string(call.id) != "9" {
		t.Fatalf("response id = %s, want 9", string(call.id))
	}
	if call.result != "tool-result" {
		t.Fatalf("response result = %#v, want \"tool-result\"", call.result)
	}
	if call.err != nil {
		t.Fatalf("response err = %v, want nil", call.err)
	}
}

func assertAsyncToolResponseResult(t *testing.T, call recordedResponse, want any) {
	t.Helper()
	if string(call.id) != "9" {
		t.Fatalf("response id = %s, want 9", string(call.id))
	}
	if !reflect.DeepEqual(call.result, want) {
		t.Fatalf("response result = %#v, want %#v", call.result, want)
	}
	if call.err != nil {
		t.Fatalf("response err = %v, want nil", call.err)
	}
}

func assertSyntheticToolBeginHeader(t *testing.T, begin tooldto.ToolCallBegin) {
	t.Helper()
	if begin.ThreadID != "agent-1" {
		t.Fatalf("begin ThreadID = %q, want agent-1", begin.ThreadID)
	}
	if begin.AgentID != "agent-1" {
		t.Fatalf("begin AgentID = %q, want agent-1", begin.AgentID)
	}
	if begin.TurnID != "turn-1" {
		t.Fatalf("begin TurnID = %q, want turn-1", begin.TurnID)
	}
	if begin.CallID != "9" || begin.ToolName != "grep" {
		t.Fatalf("begin call key = %q/%q, want 9/grep", begin.CallID, begin.ToolName)
	}
}

func assertStrictPrivateMetadata(t *testing.T, params map[string]any) {
	t.Helper()
	for _, key := range []string{"agentId", "agent_id", "threadId", "thread_id", "callId", "call_id", "cwd"} {
		if _, ok := params[key]; ok {
			t.Fatalf("enriched params kept untrusted public key %q: %#v", key, params)
		}
	}
	want := map[string]any{
		"_agentId":  "agent-1",
		"_threadId": "provider-thread-1",
		"_callId":   "9",
		"_cwd":      "/trusted/root",
	}
	for key, value := range want {
		if params[key] != value {
			t.Fatalf("private metadata %s = %#v, want %#v; params=%#v", key, params[key], value, params)
		}
	}
}

func assertSyntheticToolEndHeader(t *testing.T, end tooldto.ToolCallEnd, begin tooldto.ToolCallBegin) {
	t.Helper()
	if end.ThreadID != begin.ThreadID || end.AgentID != begin.AgentID || end.TurnID != begin.TurnID {
		t.Fatalf("end scope = %#v, want begin scope %#v", end.ToolCallHeader, begin.ToolCallHeader)
	}
	if end.CallID != begin.CallID || end.ToolName != begin.ToolName {
		t.Fatalf("end call key = %q/%q, want %q/%q", end.CallID, end.ToolName, begin.CallID, begin.ToolName)
	}
	if !end.Success {
		t.Fatalf("ToolCallEnd success = false, error = %q", end.Error)
	}
}

func waitToolCallBegin(t *testing.T, ch <-chan tooldto.ToolCallBegin) tooldto.ToolCallBegin {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolCallBegin")
		return tooldto.ToolCallBegin{}
	}
}

func waitToolCallEnd(t *testing.T, ch <-chan tooldto.ToolCallEnd) tooldto.ToolCallEnd {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolCallEnd")
		return tooldto.ToolCallEnd{}
	}
}

func waitEnrichedToolParams(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for enriched tool params")
		return nil
	}
}
