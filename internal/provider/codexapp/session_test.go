package codexapp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
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
	return &session{
		agentID:    "agent-1",
		approvals:  approvals,
		ctx:        ctx,
		manager:    manager,
		suppressed: map[string]struct{}{},
		turns:      map[string]*turnHandle{},
	}
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

	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("onInboundMessage blocked on toolHandler")
	}
	select {
	case <-resp.ch:
		t.Fatal("RespondWithID returned before toolHandler completed")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("toolHandler did not start")
	}

	close(release)
	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("toolHandler did not finish")
	}

	call := waitResponseCall(t, resp.ch)
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
