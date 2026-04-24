package thread

import (
	"testing"
	"time"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/kelindar/event"
)

func TestNewThreadSubscribersSpec(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil, nil, nil).(*service)
	spec := NewThreadSubscribers(svc).Spec

	if spec.EventType != "thread.core" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "thread.registerThreadSubscriptions" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "thread" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "thread-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestThreadSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	bindings := &eventBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	spec := NewThreadSubscribers(svc).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, newAgentLaunchedEvent("agent-1", "thread-1", ""))
	waitForThreadSubscriberEnqueued(t, svc, 1)

	cancel()
	cancel()

	event.Publish(dispatcher, newAgentLaunchedEvent("agent-1", "thread-after-cancel", ""))
	time.Sleep(50 * time.Millisecond)
	if got := svc.agentLaunchedWorker.EnqueuedTotal(); got != 1 {
		t.Fatalf("EnqueuedTotal after cancel = %d, want 1", got)
	}
}

func waitForThreadSubscriberEnqueued(t *testing.T, svc *service, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if svc.agentLaunchedWorker.EnqueuedTotal() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("EnqueuedTotal = %d, want %d", svc.agentLaunchedWorker.EnqueuedTotal(), want)
}
