package turn

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
	svc := NewService(silentLogger())
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-1",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(handler.Map{"turn/interrupt": turnInterruptHandler(svc, rpcHelperResolver{session: session})})
	raw, err := server.Dispatch(context.Background(), "turn/interrupt", json.RawMessage(`{"threadId":"thread-1","source":"user"}`))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	var result turnInterruptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !result.OK || result.TurnID != "local-1" || result.Status != "interrupted" {
		t.Fatalf("interrupt result = %#v, want ok/local-1/interrupted", result)
	}
	if !result.Confirmed || result.Mode != "interrupt_confirmed" || !result.InterruptSent {
		t.Fatalf("interrupt result = %#v, want confirmed envelope", result)
	}
	if result.StateBefore != "running" || result.StateAfter != "idle" {
		t.Fatalf("interrupt result = %#v, want running->idle envelope", result)
	}
	if result.WaitedMS == nil || *result.WaitedMS <= 0 {
		t.Fatalf("interrupt result waitedMs = %#v, want >0", result.WaitedMS)
	}
	if result.ActiveObserved == nil || !*result.ActiveObserved {
		t.Fatalf("interrupt result activeObserved = %#v, want true", result.ActiveObserved)
	}
}
