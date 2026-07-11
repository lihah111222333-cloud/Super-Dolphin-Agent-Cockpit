package hooks

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewHooksRelaySubscribersSpec(t *testing.T) {
	t.Parallel()

	worker := newHookDispatchWorker(&fakeHookFanout{}, nil)
	spec := NewHooksRelaySubscribers(worker, nil).Spec

	if spec.EventType != "hooks.event.relay" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "hooks.startEventRelay" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "hooks" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "hooks-relay-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestHooksRelaySubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	worker := newHookDispatchWorker(&fakeHookFanout{}, nil)
	spec := NewHooksRelaySubscribers(worker, nil).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, threaddto.Started{EventHeader: shareddto.EventHeader{Timestamp: time.Now()}, ThreadID: "thread-1", AgentID: "agent-1"})
	waitForHookEnqueued(t, worker, 1)

	cancel()
	cancel()

	event.Publish(dispatcher, threaddto.Started{EventHeader: shareddto.EventHeader{Timestamp: time.Now()}, ThreadID: "thread-after-cancel", AgentID: "agent-1"})
	time.Sleep(50 * time.Millisecond)
	if got := worker.EnqueuedTotal(); got != 1 {
		t.Fatalf("EnqueuedTotal after cancel = %d, want 1", got)
	}
}

func waitForHookEnqueued(t *testing.T, worker *hookDispatchWorker, want int64) {
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
