package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestConfigFanoutWorkerStopDrainsReleaseWithLiveContext(t *testing.T) {
	notifier := &shutdownDrainReleaseNotifier{
		notifyEntered: make(chan struct{}),
		releaseCtx:    make(chan error, 1),
	}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()

	worker.Enqueue(configTopicAgent, map[string]any{
		"event":   "agent/launched",
		"agentId": "agent-shutdown-drain",
	})
	select {
	case <-notifier.notifyEntered:
	case <-time.After(time.Second):
		t.Fatal("initial config notification did not enter")
	}
	worker.Enqueue(configTopicAgent, map[string]any{
		"event":   "agent/stopped",
		"agentId": "agent-shutdown-drain",
		"reason":  "shutdown_drain",
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := worker.Stop(ctx); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	select {
	case ctxErr := <-notifier.releaseCtx:
		if ctxErr != nil {
			t.Fatalf("release callback context error = %v, want live shutdown-drain context", ctxErr)
		}
	case <-time.After(time.Second):
		t.Fatal("release callback was not dispatched during Stop drain")
	}
}

func TestConfigFanoutWorkerRetainsFailedReleaseUntilSuccess(t *testing.T) {
	notifier := &retryingReleaseNotifier{
		allowSuccess: make(chan struct{}),
		failures:     3,
	}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	})

	worker.Enqueue(configTopicAgent, map[string]any{
		"event":   "agent/stopped",
		"agentId": "agent-durable-retry",
		"reason":  "durable_retry",
	})
	waitForAtomicCount(t, &notifier.releaseCalls, 3)
	if got := worker.ProcessedTotal(); got != 0 {
		t.Fatalf("ProcessedTotal after failed release = %d, want 0", got)
	}

	close(notifier.allowSuccess)
	waitForAtomicCount(t, &notifier.releaseCalls, 4)
	waitForConfigFanoutProcessed(t, worker, 1)
}

func TestConfigFanoutWorkerReleaseQueueAppliesBoundedBackpressure(t *testing.T) {
	notifier := &blockingReleaseNotifier{
		entered: make(chan struct{}),
		unblock: make(chan struct{}),
	}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()

	worker.Enqueue(configTopicAgent, stoppedAgentPayloadForQueueTest(0))
	select {
	case <-notifier.entered:
	case <-time.After(time.Second):
		t.Fatal("first release callback did not enter")
	}

	enqueueDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(enqueueDone)
		for index := 1; index <= 64; index++ {
			worker.Enqueue(configTopicAgent, stoppedAgentPayloadForQueueTest(index))
		}
	})
	select {
	case <-enqueueDone:
		t.Fatal("unique release queue accepted more than its bounded capacity without backpressure")
	case <-time.After(50 * time.Millisecond):
	}

	close(notifier.unblock)
	select {
	case <-enqueueDone:
		wg.Wait()
	case <-time.After(time.Second):
		t.Fatal("release enqueue remained blocked after queue capacity became available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := worker.Stop(ctx); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
}

type shutdownDrainReleaseNotifier struct {
	notifyOnce    sync.Once
	notifyEntered chan struct{}
	releaseCtx    chan error
}

func (n *shutdownDrainReleaseNotifier) NotifyConfigChanged(ctx context.Context, _ string, _ *dto.SelectorScope, _ int64, _ json.RawMessage) error {
	blocked := false
	n.notifyOnce.Do(func() {
		blocked = true
		close(n.notifyEntered)
	})
	if blocked {
		<-ctx.Done()
	}
	return ctx.Err()
}

func (n *shutdownDrainReleaseNotifier) DispatchLSPReleaseScope(ctx context.Context, _ dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error) {
	n.releaseCtx <- ctx.Err()
	if err := ctx.Err(); err != nil {
		return dto.LSPReleaseScopeResult{}, err
	}
	return dto.LSPReleaseScopeResult{MatchedManagers: 1, ClosedManagers: 1, Drained: true}, nil
}

type retryingReleaseNotifier struct {
	releaseCalls atomic.Int64
	allowSuccess chan struct{}
	failures     int64
}

func (n *retryingReleaseNotifier) NotifyConfigChanged(context.Context, string, *dto.SelectorScope, int64, json.RawMessage) error {
	return nil
}

func (n *retryingReleaseNotifier) DispatchLSPReleaseScope(ctx context.Context, _ dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error) {
	call := n.releaseCalls.Add(1)
	if call <= n.failures {
		return dto.LSPReleaseScopeResult{}, errors.New("release target unavailable")
	}
	select {
	case <-n.allowSuccess:
		return dto.LSPReleaseScopeResult{MatchedManagers: 1, ClosedManagers: 1, Drained: true}, nil
	case <-ctx.Done():
		return dto.LSPReleaseScopeResult{}, ctx.Err()
	}
}

type blockingReleaseNotifier struct {
	entered chan struct{}
	unblock chan struct{}
	signal  sync.Once
}

func (n *blockingReleaseNotifier) NotifyConfigChanged(context.Context, string, *dto.SelectorScope, int64, json.RawMessage) error {
	return nil
}

func (n *blockingReleaseNotifier) DispatchLSPReleaseScope(ctx context.Context, _ dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error) {
	n.signal.Do(func() {
		close(n.entered)
	})
	select {
	case <-n.unblock:
		return dto.LSPReleaseScopeResult{MatchedManagers: 1, ClosedManagers: 1, Drained: true}, nil
	case <-ctx.Done():
		return dto.LSPReleaseScopeResult{}, ctx.Err()
	}
}

func stoppedAgentPayloadForQueueTest(index int) map[string]any {
	return map[string]any{
		"event":   "agent/stopped",
		"agentId": fmt.Sprintf("agent-queue-%03d", index),
		"reason":  "bounded_queue",
	}
}

func waitForAtomicCount(t *testing.T, counter *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if counter.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("counter = %d, want at least %d", counter.Load(), want)
}
