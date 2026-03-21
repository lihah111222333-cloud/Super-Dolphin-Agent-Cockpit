package codexapp

import (
	"context"
	"errors"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/creachadair/jrpc2"
	"github.com/kelindar/event"
)

func TestRequestApprovalDecisionUsesDefaultTimeout(t *testing.T) {
	previous := rpc.DefaultApprovalTimeout
	rpc.DefaultApprovalTimeout = 25 * time.Millisecond
	defer func() { rpc.DefaultApprovalTimeout = previous }()

	s := &session{
		approvals: rpc.NewApprovalManager(nil, nil),
		ctx:       context.Background(),
	}

	_, err := s.requestApprovalDecision(rpc.ApprovalRequest{CallID: "call-1"})
	if err == nil {
		t.Fatal("requestApprovalDecision() error = nil, want timeout")
	}
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(rpc.CodeApprovalTimeout) {
		t.Fatalf("requestApprovalDecision() error = %v, want approval timeout", err)
	}
}

func TestOnNotificationApprovalRequestPublishesRequestedOnce(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &session{
		agentID:    "agent-1",
		dispatcher: dispatcher,
		approvals:  rpc.NewApprovalManager(nil, bus),
		ctx:        ctx,
		suppressed: map[string]struct{}{},
		turns:      map[string]*turnHandle{},
	}

	requested := make(chan tooldto.ToolApprovalRequested, 4)
	cancelSub := event.Subscribe(bus, func(ev tooldto.ToolApprovalRequested) {
		requested <- ev
	})
	defer cancelSub()

	s.onNotification("item/commandExecution/requestApproval", []byte(`{"requestId":1,"command":"echo hi","toolName":"shell","turnId":"turn-1"}`))

	var first tooldto.ToolApprovalRequested
	select {
	case first = <-requested:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalRequested")
	}
	if first.RequestID != 1 {
		t.Fatalf("first requestID = %d, want 1", first.RequestID)
	}

	select {
	case extra := <-requested:
		t.Fatalf("received duplicate ToolApprovalRequested event: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
}
