package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// fakeFanoutNotifier records every NotifyConfigChanged call so tests can
// assert (a) the ctx handed to the notifier is NOT context.Background()
// (it comes from the worker-owned fanoutCtx) and (b) cancelling the
// worker unblocks an in-flight peer RPC.
type fakeFanoutNotifier struct {
	mu                    sync.Mutex
	received              []fakeFanoutCall
	releaseRequests       []dto.LSPReleaseScopeRequest
	entered               chan struct{}
	exited                chan error
	block                 chan struct{}
	releaseFailuresRemain atomic.Int64
}

type fakeFanoutCall struct {
	topic         string
	configVersion int64
	ctxErr        error
	payload       json.RawMessage
}

func (f *fakeFanoutNotifier) NotifyConfigChanged(ctx context.Context, topic string, _ *dto.SelectorScope, configVersion int64, payload json.RawMessage) error {
	// Signal "notify entered" before blocking so the test can synchronize.
	if f.entered != nil {
		select {
		case <-f.entered:
		default:
			close(f.entered)
		}
	}
	if f.block != nil {
		<-f.block
	}
	if f.exited != nil {
		select {
		case f.exited <- ctx.Err():
		default:
		}
	}
	f.mu.Lock()
	f.received = append(f.received, fakeFanoutCall{
		topic:         topic,
		configVersion: configVersion,
		ctxErr:        ctx.Err(),
		payload:       append(json.RawMessage(nil), payload...),
	})
	f.mu.Unlock()
	return ctx.Err()
}

func (f *fakeFanoutNotifier) calls() []fakeFanoutCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeFanoutCall, len(f.received))
	copy(out, f.received)
	return out
}

func (f *fakeFanoutNotifier) DispatchLSPReleaseScope(_ context.Context, req dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error) {
	f.mu.Lock()
	f.releaseRequests = append(f.releaseRequests, req)
	f.mu.Unlock()
	if f.releaseFailuresRemain.Add(-1) >= 0 {
		return dto.LSPReleaseScopeResult{}, errors.New("temporary release failure")
	}
	return dto.LSPReleaseScopeResult{
		MatchedManagers: 1,
		ClosedManagers:  1,
		Drained:         true,
	}, nil
}

func (f *fakeFanoutNotifier) releases() []dto.LSPReleaseScopeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dto.LSPReleaseScopeRequest(nil), f.releaseRequests...)
}

// stubVersionSource increments a monotonic counter for advanceConfigVersion
// so tests can confirm the worker serializes versioning.
type stubVersionSource struct{ v atomic.Int64 }

func (s *stubVersionSource) advanceConfigVersion() int64 { return s.v.Add(1) }

// TestConfigFanoutWorkerUsesCancelableContext is the P22 P2 §TDD test
// named in docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:415.
//
// Contract:
//  1. The ctx the worker passes to NotifyConfigChanged must be the
//     cancellable fanoutCtx, NOT context.Background() — otherwise peer
//     RPCs have no way to observe module-level Shutdown.
//  2. Stop(ctx) must unblock an in-flight NotifyConfigChanged by
//     cancelling fanoutCtx (so the peer sees ctx.Err() promptly) and
//     return bounded by ctx.
func TestConfigFanoutWorkerUsesCancelableContext(t *testing.T) {
	t.Parallel()

	notifier, worker := startedConfigFanoutWorker()

	// First assertion: the worker-exposed fanoutCtx is cancellable and
	// distinct from context.Background(). That's the "UsesCancelable
	// Context" part of the name — we verify it before the notifier even
	// fires, so the check holds even if Enqueue/processing has a bug.
	assertFanoutCtxCancelable(t, worker)

	enqueueAgentLaunchFanout(worker)
	waitForFanoutNotifier(t, notifier)
	stopFanoutWorker(t, worker, notifier)
	assertFanoutNotifierCanceled(t, notifier)
	assertAgentLaunchFanoutCall(t, notifier.calls())
}

func startedConfigFanoutWorker() (*fakeFanoutNotifier, *configFanoutWorker) {
	notifier := &fakeFanoutNotifier{
		entered: make(chan struct{}),
		exited:  make(chan error, 1),
		block:   make(chan struct{}),
	}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()
	return notifier, worker
}

func assertFanoutCtxCancelable(t *testing.T, worker *configFanoutWorker) {
	t.Helper()
	if worker.FanoutCtx() == context.Background() {
		t.Fatal("worker.FanoutCtx() returned context.Background(); must be worker-owned cancellable ctx")
	}
}

func enqueueAgentLaunchFanout(worker *configFanoutWorker) {
	worker.Enqueue(configTopicAgent, map[string]any{
		"event":   "agent/launched",
		"agentId": "agent-1",
	})
}

func waitForFanoutNotifier(t *testing.T, notifier *fakeFanoutNotifier) {
	t.Helper()
	select {
	case <-notifier.entered:
	case <-time.After(time.Second):
		t.Fatal("NotifyConfigChanged did not enter within 1s; worker dispatch broken")
	}
}

func stopFanoutWorker(t *testing.T, worker *configFanoutWorker, notifier *fakeFanoutNotifier) {
	t.Helper()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	// Release the block in parallel with Stop; Stop must drive the ctx
	// cancellation that makes the notifier return.
	var wg sync.WaitGroup
	wg.Go(func() {
		// The fanoutCtx cancellation from Stop already unblocks the
		// notifier's ctx path; close block so the notifier actually
		// returns (the fake uses two stages: ctx.Err() + block release).
		time.Sleep(10 * time.Millisecond)
		close(notifier.block)
	})
	defer wg.Wait()
	if err := worker.Stop(shutdownCtx); err != nil {
		t.Fatalf("Stop() err = %v, want nil (drain must finish within ctx budget)", err)
	}
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Errorf("Stop took %s, want <250ms — fanoutCtx cancel should propagate to notifier immediately", elapsed)
	}
}

func assertFanoutNotifierCanceled(t *testing.T, notifier *fakeFanoutNotifier) {
	t.Helper()
	select {
	case ctxErr := <-notifier.exited:
		if !errors.Is(ctxErr, context.Canceled) {
			t.Errorf("notifier observed ctx.Err() = %v, want context.Canceled", ctxErr)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("notifier did not publish its ctx.Err() after Stop")
	}
}

func assertAgentLaunchFanoutCall(t *testing.T, calls []fakeFanoutCall) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("notifier received %d calls, want 1", len(calls))
	}
	if calls[0].topic != configTopicAgent {
		t.Errorf("call topic = %q, want %q", calls[0].topic, configTopicAgent)
	}
	if calls[0].configVersion != 1 {
		t.Errorf("configVersion = %d, want 1", calls[0].configVersion)
	}
}

// TestConfigFanoutWorkerEnqueueNonBlocking mirrors the other P2 workers:
// even if NotifyConfigChanged is stuck inside a slow peer, bus callback
// Enqueue must not block. That's the whole reason the fire-and-forget /
// `context.Background()` pattern on the callback path is forbidden.
func TestConfigFanoutWorkerEnqueueNonBlocking(t *testing.T) {
	t.Parallel()

	notifier := &fakeFanoutNotifier{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
	}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()

	enqueueDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 32 {
			worker.Enqueue(configTopicAgent, map[string]any{"event": "agent/launched"})
		}
		close(enqueueDone)
	})
	select {
	case <-enqueueDone:
		wg.Wait()
	case <-time.After(time.Second):
		t.Fatal("Enqueue blocked while notifier was stuck; callback path must stay non-blocking")
	}
	if got := worker.EnqueuedTotal(); got != 32 {
		t.Errorf("EnqueuedTotal = %d, want 32", got)
	}

	close(notifier.block)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = worker.Stop(ctx)
}

// TestConfigFanoutWorkerOverflowKeepsLatestAndEmitsDegradedEvent 锁定 config fanout 满载语义。
// 版本推进不能在无界 slice 中堆积；满载时先通知 degraded，再只发送最后一条配置变更。
func TestConfigFanoutWorkerOverflowKeepsLatestAndEmitsDegradedEvent(t *testing.T) {
	t.Parallel()

	notifier := &fakeFanoutNotifier{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
	}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	worker.Enqueue(configTopicAgent, map[string]any{"event": "agent/state/changed", "sequence": 0})
	waitForFanoutNotifier(t, notifier)
	for i := 1; i <= 6; i++ {
		worker.Enqueue(configTopicAgent, map[string]any{"event": "agent/state/changed", "sequence": i})
	}
	close(notifier.block)

	waitForConfigFanoutProcessed(t, worker, 3)
	calls := notifier.calls()
	if len(calls) != 3 {
		t.Fatalf("NotifyConfigChanged count = %d, want initial + degraded + latest; calls=%#v", len(calls), calls)
	}
	assertConfigPayloadField(t, calls[1].payload, "event", "platform/queue/degraded")
	assertConfigPayloadField(t, calls[1].payload, "queue", "config_fanout")
	assertConfigPayloadField(t, calls[2].payload, "sequence", float64(6))
	if calls[2].configVersion != 3 {
		t.Fatalf("latest configVersion = %d, want 3 after degraded signal", calls[2].configVersion)
	}
}

func TestConfigFanoutWorkerOverflowPreservesEveryLSPRelease(t *testing.T) {
	notifier := &fakeFanoutNotifier{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
	}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	})

	worker.Enqueue(configTopicAgent, map[string]any{"event": "agent/state/changed", "sequence": 0})
	waitForFanoutNotifier(t, notifier)
	for index := 1; index <= 6; index++ {
		worker.Enqueue(configTopicAgent, map[string]any{
			"event":   "agent/stopped",
			"agentId": "agent-" + string(rune('0'+index)),
			"reason":  "overflow_release",
		})
	}
	close(notifier.block)

	waitForLSPReleaseCalls(t, notifier, 6)
	releases := notifier.releases()
	for index, req := range releases {
		wantAgentID := "agent-" + string(rune('1'+index))
		if req.AgentID != wantAgentID {
			t.Fatalf("release[%d].AgentID = %q, want %q; releases=%#v", index, req.AgentID, wantAgentID, releases)
		}
	}
}

func TestConfigFanoutWorkerRetriesTransientLSPReleaseFailure(t *testing.T) {
	notifier := &fakeFanoutNotifier{}
	notifier.releaseFailuresRemain.Store(1)
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	})

	worker.Enqueue(configTopicAgent, map[string]any{
		"event":   "agent/stopped",
		"agentId": "agent-retry",
		"reason":  "retry_release",
	})

	waitForLSPReleaseCalls(t, notifier, 2)
}

func waitForLSPReleaseCalls(t *testing.T, notifier *fakeFanoutNotifier, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(notifier.releases()) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("release calls = %d, want at least %d", len(notifier.releases()), want)
}

func waitForConfigFanoutProcessed(t *testing.T, worker *configFanoutWorker, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if worker.ProcessedTotal() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ProcessedTotal = %d, want at least %d", worker.ProcessedTotal(), want)
}

func assertConfigPayloadField(t *testing.T, raw json.RawMessage, key string, want any) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v; raw=%s", err, raw)
	}
	if payload[key] != want {
		t.Fatalf("payload[%q] = %#v, want %#v; payload=%#v", key, payload[key], want, payload)
	}
}

// TestConfigFanoutWorkerEmptyTopicDropped covers the blank-topic short
// circuit so invalid events never pay a queue slot.
func TestConfigFanoutWorkerEmptyTopicDropped(t *testing.T) {
	t.Parallel()

	notifier := &fakeFanoutNotifier{}
	worker := newConfigFanoutWorker(notifier, &stubVersionSource{}, nil)
	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	worker.Enqueue("", map[string]any{"event": "agent/launched"})
	worker.Enqueue("   ", map[string]any{"event": "agent/launched"})

	time.Sleep(20 * time.Millisecond)
	if got := worker.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after blank topic enqueues = %d, want 0", got)
	}
	if got := len(notifier.calls()); got != 0 {
		t.Errorf("notifier calls = %d, want 0 for blank topics", got)
	}
}
