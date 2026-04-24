package thread

import (
	"context"
	"testing"
	"time"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

func TestThreadBusWorkersAsRunnerRunStopsWorkers(t *testing.T) {
	t.Parallel()

	bindings := &eventBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	runner := threadBusWorkersAsRunner(svc)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancel")
	}

	assertClosed(t, svc.taskHandoffWorker.stopCh, "task handoff stopCh")
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
