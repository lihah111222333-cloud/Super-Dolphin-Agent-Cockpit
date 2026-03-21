package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/kelindar/event"
)

func TestRegisterPendingAssignsUniqueRequestIDForDuplicateCallID(t *testing.T) {
	manager := NewApprovalManager(nil, nil)

	first, firstOwner := manager.registerPending(ApprovalRequest{CallID: "call-1"}, nil)
	second, secondOwner := manager.registerPending(ApprovalRequest{CallID: "call-1"}, nil)

	if !firstOwner || !secondOwner {
		t.Fatalf("registerPending owner flags = %v, %v; want true, true", firstOwner, secondOwner)
	}
	if first == second {
		t.Fatal("registerPending returned the same pending for duplicate callID without requestID")
	}
	if first.requestID == nil || second.requestID == nil {
		t.Fatal("registerPending did not assign request IDs")
	}
	if *first.requestID == *second.requestID {
		t.Fatalf("request IDs collapsed: %d", *first.requestID)
	}
	if first.key == second.key {
		t.Fatalf("pending keys collapsed: %q", first.key)
	}
}

func TestRegisterPendingStoresDispatcherBeforePublish(t *testing.T) {
	manager := NewApprovalManager(nil, nil)
	dispatcher := &event.Dispatcher{}

	pending, owner := manager.registerPending(ApprovalRequest{CallID: "call-1"}, dispatcher)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	if pending.dispatcher != dispatcher {
		t.Fatal("registerPending did not retain dispatcher")
	}
}

func TestRegisterPendingFallsBackToManagerDispatcher(t *testing.T) {
	dispatcher := &event.Dispatcher{}
	manager := NewApprovalManager(nil, dispatcher)

	pending, owner := manager.registerPending(ApprovalRequest{CallID: "call-1"}, nil)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	if pending.dispatcher != dispatcher {
		t.Fatal("registerPending did not fall back to manager dispatcher")
	}
}

func TestCleanupPublishesResolvedTimeoutEvent(t *testing.T) {
	dispatcher := event.NewDispatcher()
	manager := NewApprovalManager(nil, dispatcher)
	resolved := make(chan tooldto.ToolApprovalResolved, 1)
	cancel := event.Subscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
		resolved <- ev
	})
	defer cancel()

	pending, owner := manager.registerPending(ApprovalRequest{
		CallID:  "call-1",
		AgentID: "agent-1",
		TurnID:  "turn-1",
		Kind:    "request_user_input",
	}, nil)
	if !owner {
		t.Fatal("registerPending owner = false, want true")
	}
	pending.createdAt = time.Now().Add(-time.Minute)

	manager.Cleanup(time.Second)

	event := awaitResolvedEvent(t, resolved)
	if event.CallID != "call-1" {
		t.Fatalf("resolved callID = %q, want %q", event.CallID, "call-1")
	}
	if event.Decision != ErrApprovalTimeout("approval timed out").Error() {
		t.Fatalf("resolved decision = %q, want %q", event.Decision, ErrApprovalTimeout("approval timed out").Error())
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("Cleanup left pending approvals behind")
	}
}

func TestRequestApprovalCanceledContextPublishesResolvedEvent(t *testing.T) {
	dispatcher := event.NewDispatcher()
	manager := NewApprovalManager(nil, dispatcher)
	resolved := make(chan tooldto.ToolApprovalResolved, 1)
	cancelSub := event.Subscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
		resolved <- ev
	})
	defer cancelSub()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := manager.RequestApproval(ctx, nil, nil, ApprovalRequest{
		CallID:  "call-1",
		AgentID: "agent-1",
		TurnID:  "turn-1",
		Kind:    "request_user_input",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RequestApproval() error = %v, want %v", err, context.Canceled)
	}

	event := awaitResolvedEvent(t, resolved)
	if event.CallID != "call-1" {
		t.Fatalf("resolved callID = %q, want %q", event.CallID, "call-1")
	}
	if event.Decision != context.Canceled.Error() {
		t.Fatalf("resolved decision = %q, want %q", event.Decision, context.Canceled.Error())
	}
	if len(manager.PendingSnapshot()) != 0 {
		t.Fatal("RequestApproval left pending approvals behind")
	}
}

func awaitResolvedEvent(t *testing.T, resolved <-chan tooldto.ToolApprovalResolved) tooldto.ToolApprovalResolved {
	t.Helper()

	select {
	case event := <-resolved:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ToolApprovalResolved event")
		return tooldto.ToolApprovalResolved{}
	}
}
