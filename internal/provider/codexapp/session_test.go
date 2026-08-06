package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type recordedResponse struct {
	id     json.RawMessage
	result any
	err    error
}

func TestNewSessionWithOptionsRejectsNilApprovalManagerAndReleasesPoolSlot(t *testing.T) {
	releases := 0

	s, err := newSessionWithOptions(
		context.Background(),
		slog.Default(),
		"ws://127.0.0.1:1",
		"agent-1",
		nil,
		nil,
		nil,
		withPoolServer("ws://127.0.0.1:1", func() { releases++ }),
	)
	if s != nil {
		t.Fatalf("newSessionWithOptions() session = %#v, want nil", s)
	}
	if err != errApprovalManagerRequired {
		t.Fatalf("newSessionWithOptions() error = %v, want %v", err, errApprovalManagerRequired)
	}
	if releases != 1 {
		t.Fatalf("pool releases = %d, want 1", releases)
	}
}

func TestNewSessionWithOptionsPropagatesAgentLogFailureAndReleasesPoolSlot(t *testing.T) {
	logDir := t.TempDir()
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{Mode: pkglogger.Production, Level: slog.LevelInfo})
	if err := runtime.InitWithFile(logDir); err != nil {
		t.Fatalf("init file logger: %v", err)
	}
	previous := pkglogger.InstallRuntime(runtime)
	t.Cleanup(func() {
		pkglogger.InstallRuntime(previous)
		runtime.ShutdownFileHandler()
	})
	if err := os.Mkdir(filepath.Join(logDir, "agent-blocked.log"), 0o700); err != nil {
		t.Fatalf("create blocking agent log directory: %v", err)
	}
	serverURL := startCodexRPCServer(t, func(string) json.RawMessage {
		return mustJSON(map[string]any{"ok": true})
	})
	releases := 0
	s, err := newSessionWithOptions(
		context.Background(),
		slog.Default(),
		serverURL,
		"blocked",
		nil,
		testApprovalManager(),
		nil,
		withPoolServer(serverURL, func() { releases++ }),
	)
	if err == nil || !strings.Contains(err.Error(), "create agent logger") {
		t.Fatalf("newSessionWithOptions() error = %v, want agent logger failure", err)
	}
	if s != nil {
		t.Fatalf("newSessionWithOptions() session = %#v, want nil", s)
	}
	if releases != 1 {
		t.Fatalf("pool releases = %d, want 1", releases)
	}
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

func newInboundTestSession(t *testing.T, ctx context.Context, approvals *rpc.ApprovalManager, manager *ServerManager) *session {
	t.Helper()
	s := &session{
		agentID:              "agent-1",
		approvalSessionScope: "test-session-scope",
		approvals:            approvals,
		ctx:                  ctx,
		logger:               slog.Default(),
		manager:              manager,
		suppressed:           map[string]struct{}{},
		suppressedToolEnds:   map[string]struct{}{},
		turns:                map[string]*turnHandle{},
		runtimeHooks:         testRuntimeHooks(t),
	}
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{"cwd": "/trusted/root"})
	s.setApprovalPolicy("on-request")
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
	ctx := t.Context()

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
	s := newInboundTestSession(t, ctx, rpc.NewApprovalManager(nil, bus), manager)

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`1`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"requestId":1,"callId":"call-1","command":"echo hi","toolName":"shell","turnId":"turn-1"}`),
	})

	ev := waitApprovalRequested(t, requested)
	if ev.RequestID != 1 {
		t.Fatalf("RequestID = %d, want 1", ev.RequestID)
	}
	if ev.SessionScope != "test-session-scope" || ev.CallID != "call-1" {
		t.Fatalf("identity = (%q, %q, %d), want (%q, %q, %d)", ev.SessionScope, ev.CallID, ev.RequestID, "test-session-scope", "call-1", 1)
	}
	if handlerCalls.Load() != 0 {
		t.Fatalf("toolHandler calls = %d, want 0", handlerCalls.Load())
	}
	if resp.callCount() != 0 {
		t.Fatalf("RespondWithID calls = %d, want 0", resp.callCount())
	}
}

func TestOnInboundMessage_ApprovalRequest_RespondsWithJSONRPCID(t *testing.T) {
	bus := event.NewDispatcher()
	ctx := t.Context()

	requested := make(chan tooldto.ToolApprovalRequested, 1)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) { requested <- ev })
	defer cancelSub()

	approvals := rpc.NewApprovalManager(nil, bus)
	resp := newRecordingResponder()
	s := newInboundTestSession(t, ctx, approvals, &ServerManager{})

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`17`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"itemId":"command-1","command":"echo hi","turnId":"turn-1","threadId":"provider-thread-1","startedAtMs":1}`),
	})

	ev := waitApprovalRequested(t, requested)
	if ev.RequestID != 17 || ev.CallID != "command-1" {
		t.Fatalf("approval identity = (%d, %q), want (17, command-1)", ev.RequestID, ev.CallID)
	}
	approved := true
	if err := approvals.Respond(contract.ApprovalIdentity{
		SessionScope: ev.SessionScope,
		CallID:       ev.CallID,
		RequestID:    ev.RequestID,
	}, contract.ApprovalDecision{Approved: &approved}); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	call := waitResponseCall(t, resp.ch)
	if string(call.id) != "17" || call.err != nil {
		t.Fatalf("response = (id=%s, err=%v), want (17, nil)", call.id, call.err)
	}
	result, ok := call.result.(map[string]any)
	if !ok || result["decision"] != "accept" {
		t.Fatalf("response result = %#v, want decision=accept", call.result)
	}
}

func TestApprovalRequestIDFromJSONRPCSupportsOpaqueString(t *testing.T) {
	first, err := approvalRequestIDFromJSONRPC(json.RawMessage(`"approval-request-alpha"`))
	if err != nil {
		t.Fatalf("approvalRequestIDFromJSONRPC() error = %v", err)
	}
	second, err := approvalRequestIDFromJSONRPC(json.RawMessage(`"approval-request-alpha"`))
	if err != nil {
		t.Fatalf("approvalRequestIDFromJSONRPC() second error = %v", err)
	}
	if first <= 0 || first != second {
		t.Fatalf("opaque request surrogate = (%d, %d), want matching positive IDs", first, second)
	}
}

func TestApprovalRequestIDFromJSONRPCMapsZeroToStablePositiveIdentity(t *testing.T) {
	first, err := approvalRequestIDFromJSONRPC(json.RawMessage(`0`))
	if err != nil {
		t.Fatalf("approvalRequestIDFromJSONRPC(0) error = %v", err)
	}
	second, err := approvalRequestIDFromJSONRPC(json.RawMessage(`0`))
	if err != nil {
		t.Fatalf("approvalRequestIDFromJSONRPC(0) second error = %v", err)
	}
	if first <= 0 || first != second {
		t.Fatalf("zero request surrogate = (%d, %d), want matching positive IDs", first, second)
	}
}

func TestOnInboundMessage_ApprovalRequestWithZeroID_RespondsWithOriginalJSONRPCID(t *testing.T) {
	bus := event.NewDispatcher()
	ctx := t.Context()

	requested := make(chan tooldto.ToolApprovalRequested, 1)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) { requested <- ev })
	defer cancelSub()

	approvals := rpc.NewApprovalManager(nil, bus)
	resp := newRecordingResponder()
	s := newInboundTestSession(t, ctx, approvals, &ServerManager{})

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`0`),
		Method: "item/commandExecution/requestApproval",
		Params: json.RawMessage(`{"itemId":"command-zero","command":"echo hi","turnId":"turn-zero","threadId":"provider-thread-zero","startedAtMs":1}`),
	})

	ev := waitApprovalRequested(t, requested)
	if ev.RequestID <= 0 || ev.CallID != "command-zero" {
		t.Fatalf("approval identity = (%d, %q), want positive request ID and command-zero", ev.RequestID, ev.CallID)
	}
	approved := true
	if err := approvals.Respond(contract.ApprovalIdentity{
		SessionScope: ev.SessionScope,
		CallID:       ev.CallID,
		RequestID:    ev.RequestID,
	}, contract.ApprovalDecision{Approved: &approved}); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}

	call := waitResponseCall(t, resp.ch)
	if string(call.id) != "0" || call.err != nil {
		t.Fatalf("response = (id=%s, err=%v), want (0, nil)", call.id, call.err)
	}
	result, ok := call.result.(map[string]any)
	if !ok || result["decision"] != "accept" {
		t.Fatalf("response result = %#v, want decision=accept", call.result)
	}
}

func TestOnInboundMessage_RequestUserInput_ViaApprovalBridge(t *testing.T) {
	bus := event.NewDispatcher()
	ctx := t.Context()

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
	s := newInboundTestSession(t, ctx, rpc.NewApprovalManager(nil, bus), manager)

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`2`),
		Method: "request_user_input",
		Params: json.RawMessage(`{"requestId":2,"callId":"call-2","message":"continue","turnId":"turn-1"}`),
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
	s := newInboundTestSession(t, ctx, nil, manager)

	returned := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		s.onInboundMessage(ctx, resp, RawMessage{
			ID:     json.RawMessage(`9`),
			Method: "item/tool/call",
			Params: json.RawMessage(`{"tool":"demo"}`),
		})
		close(returned)
	})

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
	s := newInboundTestSession(t, ctx, nil, manager)
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

func TestOnInboundMessage_ToolsCall_DispatchesLifecycle(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	ctx := context.Background()
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	manager := &ServerManager{}
	sawLifecycleContext := false
	var sawTrace observability.TraceContext
	manager.SetToolHandler(func(ctx context.Context, _ RawMessage) (any, error) {
		sawLifecycleContext = contract.ToolLifecycleAlreadyPublished(ctx)
		sawTrace, _ = observability.TraceFromContext(ctx)
		return map[string]any{"ok": true, "content": []any{map[string]any{"type": "text", "text": "package main"}}}, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(t, ctx, nil, manager)
	s.dispatcher = dispatcher
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{"cwd": "/trusted/root"})
	s.mu.Lock()
	s.activeTurnID = "turn-1"
	s.turns["turn-1"] = &turnHandle{trace: observability.TraceContext{TraceID: "trace-1", SpanID: "provider-span", ParentSpanID: "turn-span"}}
	s.mu.Unlock()

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`"req-1"`),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"file","arguments":{"action":"read_file","file_path":"smoke.go"}}`),
	})

	begin := waitToolCallBegin(t, beginCh)
	if begin.CallID != "req-1" || begin.ToolName != "file" {
		t.Fatalf("begin call key = %q/%q, want req-1/file", begin.CallID, begin.ToolName)
	}
	call := waitResponseCall(t, resp.ch)
	if string(call.id) != `"req-1"` {
		t.Fatalf("response id = %s, want req-1", string(call.id))
	}
	wantResult := map[string]any{"ok": true, "content": []any{map[string]any{"type": "text", "text": "package main"}}}
	if !reflect.DeepEqual(call.result, wantResult) {
		t.Fatalf("response result = %#v, want file content result", call.result)
	}
	assertToolHandlerTraceContext(t, sawLifecycleContext, sawTrace)
	end := waitToolCallEnd(t, endCh)
	if end.CallID != begin.CallID || end.ToolName != begin.ToolName || !end.Success {
		t.Fatalf("end = %+v, want successful req-1/file end", end)
	}
}

func assertToolHandlerTraceContext(t *testing.T, sawLifecycleContext bool, sawTrace observability.TraceContext) {
	t.Helper()
	if !sawLifecycleContext {
		t.Fatal("tool handler context missing lifecycle already-published marker")
	}
	if sawTrace.TraceID != "trace-1" || sawTrace.SpanID != "provider-span" || sawTrace.ParentSpanID != "turn-span" {
		t.Fatalf("tool handler trace context = %#v, want active turn trace", sawTrace)
	}
}

func TestOnInboundMessage_ToolsCallSuppressesDuplicateRawEnd(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher, testRuntimeHooks(t))
	ctx := context.Background()
	endCh := make(chan tooldto.ToolCallEnd, 2)
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(t, ctx, nil, manager)
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

	s.onNotification("item/completed", json.RawMessage(`{"agentId":"agent-1","threadId":"provider-thread-1","turnId":"turn-1","callId":"req-1","name":"file","success":true,"result":{"ok":true}}`))
	assertNoToolCallEnd(t, endCh)
}

func TestSuppressedToolEndBoundedAndTurnScoped(t *testing.T) {
	s := newInboundTestSession(t, context.Background(), nil, &ServerManager{})
	s.suppressToolEnd("turn-1", "call-1", "file")
	if s.consumeSuppressedToolEnd("turn-2", "call-1", "file") {
		t.Fatal("consumeSuppressedToolEnd consumed a different turn")
	}
	if !s.consumeSuppressedToolEnd("turn-1", "call-1", "file") {
		t.Fatal("consumeSuppressedToolEnd did not consume matching turn/call/tool")
	}
	for i := range maxSuppressedToolEnds + 5 {
		s.suppressToolEnd("turn-bounded", fmt.Sprintf("call-%d", i), "file")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := len(s.suppressedToolEnds); got > maxSuppressedToolEnds {
		t.Fatalf("suppressedToolEnds size = %d, want <= %d", got, maxSuppressedToolEnds)
	}
	if got := len(s.suppressedToolOrder); got > maxSuppressedToolEnds {
		t.Fatalf("suppressedToolOrder size = %d, want <= %d", got, maxSuppressedToolEnds)
	}
}

func TestOnInboundMessage_ToolCall_DispatchesStructuredFailureEnd(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	ctx := context.Background()
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	manager := &ServerManager{}
	manager.SetToolHandler(func(context.Context, RawMessage) (any, error) {
		return &toolbridge.ToolCallResult{
			Success: false,
			ContentItems: []toolbridge.ToolCallContentItem{{
				Type: "inputText",
				Text: `{"error":"tool failed"}`,
			}},
		}, nil
	})
	resp := newRecordingResponder()
	s := newInboundTestSession(t, ctx, nil, manager)
	s.dispatcher = dispatcher

	s.onInboundMessage(ctx, resp, RawMessage{
		ID:     json.RawMessage(`9`),
		Method: "item/tool/call",
		Params: json.RawMessage(`{"name":"grep","arguments":{}}`),
	})

	call := waitResponseCall(t, resp.ch)
	if call.err != nil {
		t.Fatalf("response err = %v, want nil structured failure", call.err)
	}
	end := waitToolCallEnd(t, endCh)
	if end.Success {
		t.Fatalf("ToolCallEnd success = true, want false for structured tool failure")
	}
	if !strings.HasPrefix(end.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.Error, "tool failed") {
		t.Fatalf("ToolCallEnd error = %q, want public structured failure diagnostic", end.Error)
	}
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
	s := newInboundTestSession(t, ctx, nil, manager)
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
	if !strings.HasPrefix(end.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.Error, "lsp peer unavailable") {
		t.Fatalf("ToolCallEnd error = %q, want public tool failure diagnostic", end.Error)
	}
}

func TestToolCallEndOutcomeFailsOnMalformedEnvelope(t *testing.T) {
	t.Parallel()

	success, errText := toolCallEndOutcome(make(chan int), nil)
	if success {
		t.Fatal("toolCallEndOutcome() success = true, want false for uninspectable result")
	}
	if !strings.Contains(errText, "decode tool result envelope") {
		t.Fatalf("toolCallEndOutcome() error = %q, want decode error", errText)
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

func assertNoToolCallEnd(t *testing.T, ch <-chan tooldto.ToolCallEnd) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected ToolCallEnd = %+v", ev)
	case <-time.After(50 * time.Millisecond):
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
