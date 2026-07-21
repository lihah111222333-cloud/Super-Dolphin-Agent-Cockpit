package rpc

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewRPCPushSubscribersSpec(t *testing.T) {
	t.Parallel()

	worker := newPushNotificationWorker(&fakePushBroadcaster{}, &PushBridge{}, nil)
	spec := NewRPCPushSubscribers(worker, nil, nil).Spec

	if spec.EventType != "rpc.push.core" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "rpc.subscribeCoreEventPushes" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "rpc" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "rpc-push-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestRPCPushSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	worker := newPushNotificationWorker(&fakePushBroadcaster{}, &PushBridge{}, nil)
	spec := NewRPCPushSubscribers(worker, nil, nil).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, rpcPushSubscriberTurnCompleted(t, "thread-1", "turn-1", "agent-1"))
	waitForRPCPushEnqueued(t, worker, 1)

	cancel()
	cancel()

	event.Publish(dispatcher, rpcPushSubscriberTurnCompleted(t, "thread-1", "turn-after-cancel", "agent-1"))
	time.Sleep(50 * time.Millisecond)
	if got := worker.EnqueuedTotal(); got != 1 {
		t.Fatalf("EnqueuedTotal after cancel = %d, want 1", got)
	}
}

func rpcPushSubscriberTurnHeader(threadID, turnID, agentID string) shareddto.TurnHeader {
	return shareddto.TurnHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{
				EventHeader: shareddto.EventHeader{Timestamp: time.Date(2026, time.July, 19, 0, 0, 0, 0, time.UTC)},
				ThreadID:    threadID,
			},
			AgentID: agentID,
		},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
	}
}

func rpcPushSubscriberTurnCompleted(t *testing.T, threadID, turnID, agentID string) turndto.TurnCompleted {
	t.Helper()
	completed := turndto.TurnCompleted{
		TurnHeader: rpcPushSubscriberTurnHeader(threadID, turnID, agentID),
		Success:    true,
		Status:     "completed",
	}
	terminal, err := turndto.NewTurnTerminalV2(completed, "rpc-push-subscriber-"+turnID)
	if err != nil {
		t.Fatalf("NewTurnTerminalV2() error = %v", err)
	}
	completed, err = turndto.AttachCanonicalTurnTerminal(completed, terminal)
	if err != nil {
		t.Fatalf("AttachCanonicalTurnTerminal() error = %v", err)
	}
	return completed
}

func waitForRPCPushEnqueued(t *testing.T, worker *pushNotificationWorker, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if worker.EnqueuedTotal() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("EnqueuedTotal = %d, want %d", worker.EnqueuedTotal(), want)
}
