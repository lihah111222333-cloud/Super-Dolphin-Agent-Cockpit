package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestTurnInterruptHandlerReturnsEnvelope(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-1", "provider-1")
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			time.AfterFunc(20*time.Millisecond, func() {
				handle.complete(errors.New("turn aborted"))
			})
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-1",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(handler.Map{"turn/interrupt": turnInterruptHandler(svc, rpcHelperResolver{session: session})})
	raw, err := server.Dispatch(context.Background(), "turn/interrupt", json.RawMessage(`{"threadId":"thread-1","source":"user"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	var result turnInterruptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assertInterruptEnvelope(t, result)
	if session.lastInterrupt.TurnID != "provider-1" {
		t.Fatalf("interrupt request turn id = %q, want provider-1", session.lastInterrupt.TurnID)
	}
}

func TestTurnInterruptHandlerAcceptsSnakeThreadID(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-snake", "provider-snake")
	session := &stubSession{
		threadID: "thread-snake",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			handle.complete(errors.New("turn aborted"))
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-snake",
		ThreadID: "thread-snake",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(handler.Map{"turn/interrupt": turnInterruptHandler(svc, rpcHelperResolver{session: session})})
	raw, err := server.Dispatch(context.Background(), "turn/interrupt", json.RawMessage(`{"thread_id":"thread-snake","source":"user"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	var result turnInterruptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !result.OK || result.TurnID != "local-snake" || session.lastInterrupt.TurnID != "provider-snake" {
		t.Fatalf("interrupt result = %#v, lastInterrupt=%#v", result, session.lastInterrupt)
	}
}

func TestTurnInterruptHandlerRequiresThreadID(t *testing.T) {
	t.Parallel()

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(handler.Map{"turn/interrupt": turnInterruptHandler(NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}), rpcHelperResolver{})})
	_, err := server.Dispatch(context.Background(), "turn/interrupt", json.RawMessage(`{"source":"user"}`))
	if err == nil || !strings.Contains(err.Error(), "threadId is required") {
		t.Fatalf("Dispatch() error = %v, want threadId required", err)
	}
}

func TestTurnInterruptHandlerRejectsUnknownField(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-unknown", "provider-unknown")
	session := &stubSession{
		threadID: "thread-unknown",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			handle.complete(errors.New("turn aborted"))
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-unknown",
		ThreadID: "thread-unknown",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(handler.Map{"turn/interrupt": turnInterruptHandler(svc, rpcHelperResolver{session: session})})
	_, err = server.Dispatch(context.Background(), "turn/interrupt", json.RawMessage(`{"thread_id":"thread-unknown","source":"user","unexpectedUiOnlyField":"leak"}`))
	if err == nil || !strings.Contains(err.Error(), "invalid parameters") {
		t.Fatalf("Dispatch() error = %v, want unknown field rejection", err)
	}
	if session.lastInterrupt.TurnID != "" {
		t.Fatalf("Interrupt() was called with %#v, want decode rejection before provider call", session.lastInterrupt)
	}
}

func TestTurnInterruptHandlerReturnsTimeoutEnvelope(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-timeout", "provider-timeout")
	session := &stubSession{
		threadID: "thread-timeout",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error { return nil },
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}).(*service)
	svc.interruptSettleTimeout = 25 * time.Millisecond
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-timeout",
		ThreadID: "thread-timeout",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(handler.Map{"turn/interrupt": turnInterruptHandler(svc, rpcHelperResolver{session: session})})
	raw, err := server.Dispatch(context.Background(), "turn/interrupt", json.RawMessage(`{"threadId":"thread-timeout","source":"user"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v, want timeout envelope result", err)
	}

	var result turnInterruptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if result.OK || result.Mode != "interrupt_timeout" || !result.InterruptSent || !result.Confirmed {
		t.Fatalf("interrupt result = %#v, want explicit timeout failure envelope", result)
	}
	if result.StateBefore != "running" || result.StateAfter != "running" {
		t.Fatalf("interrupt result = %#v, want running->running", result)
	}
}

// TestForceCompleteRPCDoesNotReportSuccessWhenProviderNoTarget verifies no-target errors use an explicit envelope.
func TestForceCompleteRPCDoesNotReportSuccessWhenProviderNoTarget(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-force", "provider-force")
	session := &stubSession{
		threadID: "thread-force",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		forceComplete: func(context.Context, dto.ForceCompleteRequest) error {
			return forceCompleteTargetNotFoundTestError{}
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{})
	if _, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-force",
		ThreadID: "thread-force",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(handler.Map{"turn/forceComplete": turnForceCompleteHandler(svc, rpcHelperResolver{session: session})})
	raw, err := server.Dispatch(context.Background(), "turn/forceComplete", json.RawMessage(`{"threadId":"thread-force"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v, want forceCompleted=false envelope", err)
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if result["ok"] != false || result["forceCompleted"] != false || result["errorCode"] != "force_complete_target_not_found" {
		t.Fatalf("forceComplete result = %#v, want no-target failure envelope", result)
	}
}

// forceCompleteTargetNotFoundTestError mimics provider no-target errors without importing a provider package.
type forceCompleteTargetNotFoundTestError struct{}

// Error returns the provider-facing no-target message.
func (forceCompleteTargetNotFoundTestError) Error() string {
	return "force complete target not found"
}

// ForceCompleteTargetNotFound marks the error for RPC envelope mapping.
func (forceCompleteTargetNotFoundTestError) ForceCompleteTargetNotFound() bool {
	return true
}

func assertInterruptEnvelope(t *testing.T, result turnInterruptResult) {
	t.Helper()
	if !result.OK {
		t.Fatalf("interrupt result = %#v, want OK", result)
	}
	if result.TurnID != "local-1" {
		t.Fatalf("interrupt result = %#v, want TurnID local-1", result)
	}
	if result.Status != "interrupted" {
		t.Fatalf("interrupt result = %#v, want interrupted status", result)
	}
	if !result.Confirmed {
		t.Fatalf("interrupt result = %#v, want confirmed", result)
	}
	if result.Mode != "interrupt_confirmed" {
		t.Fatalf("interrupt result = %#v, want interrupt_confirmed mode", result)
	}
	if !result.InterruptSent {
		t.Fatalf("interrupt result = %#v, want InterruptSent", result)
	}
	assertInterruptStateTransition(t, result)
	assertInterruptObservation(t, result)
}

func assertInterruptStateTransition(t *testing.T, result turnInterruptResult) {
	t.Helper()
	if result.StateBefore != "running" {
		t.Fatalf("interrupt result = %#v, want StateBefore running", result)
	}
	if result.StateAfter != "idle" {
		t.Fatalf("interrupt result = %#v, want StateAfter idle", result)
	}
}

func assertInterruptObservation(t *testing.T, result turnInterruptResult) {
	t.Helper()
	if result.WaitedMS == nil {
		t.Fatalf("interrupt result waitedMs = nil, want >0")
	}
	if *result.WaitedMS <= 0 {
		t.Fatalf("interrupt result waitedMs = %d, want >0", *result.WaitedMS)
	}
	if result.ActiveObserved == nil {
		t.Fatalf("interrupt result activeObserved = nil, want true")
	}
	if !*result.ActiveObserved {
		t.Fatalf("interrupt result activeObserved = false, want true")
	}
}
