package thread

import (
	"context"
	"sync"
	"testing"
	"time"

	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
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
	bindings := &eventBindingStore{binding: &BindingRecord{AgentID: "agent-1"}}
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

func registerThreadGoroutineCleanup(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s goroutines did not stop", label)
		}
	})
}

func TestThreadBusWorkersAsRunnerRunStopsWorkers(t *testing.T) {
	t.Parallel()

	bindings := &eventBindingStore{binding: &BindingRecord{AgentID: "agent-1"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	runner := threadBusWorkersAsRunner(svc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(finished)
		done <- runner.Run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
			wg.Wait()
		case <-time.After(time.Second):
			t.Fatal("thread bus runner goroutine did not stop")
		}
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancel")
	}

	assertClosed(t, svc.agentLaunchedWorker.stopCh, "agent launched stopCh")
	assertClosed(t, svc.sessionRecoveryWorker.stopCh, "session recovery stopCh")

	before := svc.agentLaunchedWorker.EnqueuedTotal()
	svc.onAgentLaunched(newAgentLaunchedEvent("agent-1", "thread-1", ""))
	if got := svc.agentLaunchedWorker.EnqueuedTotal(); got != before {
		t.Fatalf("EnqueuedTotal after Stop = %d, want %d", got, before)
	}
}

func assertClosed(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatalf("%s is not closed", name)
	}
}
