package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// fakeHookFanout 记录 DispatchAfter 调用。
// 测试借它确认真正触达 Manager 的是 worker，而不是 callback 里临时启动的 goroutine。
type fakeHookFanout struct {
	mu      sync.Mutex
	calls   []hookDispatchRequest
	entered chan struct{}
	block   chan struct{} // if non-nil, DispatchAfter blocks on receive
}

func (f *fakeHookFanout) DispatchAfter(_ context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error) {
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, hookDispatchRequest{topic: topic, payload: payload})
	return mcp.AfterDecision{}, nil
}

func (f *fakeHookFanout) Calls() []hookDispatchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]hookDispatchRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestHookRelayDrainAfterShutdown 固定 hook relay 的停机排空边界。
// Stop 触发前已经入队的请求必须在 Stop 返回前进入 DispatchAfter；否则 fire-and-forget 会让回调越过关闭点。
func TestHookRelayDrainAfterShutdown(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	for i := 0; i < 3; i++ {
		w.Enqueue("session.start", time.Now(), payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	if got := w.ProcessedTotal(); got != 3 {
		t.Fatalf("ProcessedTotal after drain = %d, want 3 (all below-limit enqueues must reach DispatchAfter)", got)
	}
	if got := len(fanout.Calls()); got != 3 {
		t.Fatalf("DispatchAfter call count after drain = %d, want 3", got)
	}
}

// TestHookDispatchWorkerEnqueueNonBlockingUnderSlowFanout 确认慢 fanout 不阻塞 Enqueue。
// DispatchAfter 被卡住时，bus callback 仍只能入队，不能同步执行下游派发。
func TestHookDispatchWorkerEnqueueNonBlockingUnderSlowFanout(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	fanout := &fakeHookFanout{block: block}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	enqueueDone := make(chan struct{})
	go func() {
		for i := 0; i < 32; i++ {
			w.Enqueue("session.start", time.Now(), payload)
		}
		close(enqueueDone)
	}()
	select {
	case <-enqueueDone:
	case <-time.After(time.Second):
		t.Fatalf("Enqueue blocked while DispatchAfter was stuck; pre-P2 fire-and-forget replacement must stay non-blocking")
	}
	if got := len(fanout.Calls()); got != 0 {
		t.Fatalf("DispatchAfter invoked %d times while fanout blocked; callback must never drive it", got)
	}
	if got := w.EnqueuedTotal(); got != 32 {
		t.Fatalf("EnqueuedTotal = %d, want 32 attempts", got)
	}

	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.ProcessedTotal() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := w.ProcessedTotal(); got == 0 || got >= 32 {
		t.Errorf("ProcessedTotal after unblock = %d, want bounded compressed processing", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = w.Stop(ctx)
}

// TestHookDispatchWorkerOverflowKeepsLatestAndEmitsDegradedEvent 锁定 hook 队列满载策略。
// 慢 peer 卡住 dispatch 时，worker 只能保留最新 hook，并用同一 topic 发出 degraded 上下文。
func TestHookDispatchWorkerOverflowKeepsLatestAndEmitsDegradedEvent(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{
		entered: make(chan struct{}),
		block:   make(chan struct{}),
	}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = w.Stop(ctx)
	}()

	initialPayload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-0", Context: json.RawMessage(`{"sequence":0}`)}
	w.Enqueue("session.start", time.Now(), initialPayload)
	select {
	case <-fanout.entered:
	case <-time.After(time.Second):
		t.Fatal("DispatchAfter did not enter within 1s")
	}

	for i := 1; i <= 6; i++ {
		w.Enqueue("session.start", time.Now(), mcp.HookPayload{
			AgentID:  "agent-A",
			ThreadID: "thread-overflow",
			Context:  json.RawMessage(`{"sequence":1}`),
		})
	}
	close(fanout.block)

	waitForHookProcessed(t, w, 3)
	assertHookOverflowCalls(t, fanout.Calls())
}

func waitForHookProcessed(t *testing.T, w *hookDispatchWorker, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if w.ProcessedTotal() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ProcessedTotal = %d, want at least %d", w.ProcessedTotal(), want)
}

func assertHookOverflowCalls(t *testing.T, calls []hookDispatchRequest) {
	t.Helper()
	if len(calls) != 3 {
		t.Fatalf("DispatchAfter count = %d, want initial + degraded + latest; calls=%#v", len(calls), calls)
	}
	if calls[1].topic != "session.start" {
		t.Fatalf("degraded topic = %q, want session.start", calls[1].topic)
	}
	if calls[1].payload.AgentID != "platform" {
		t.Fatalf("degraded AgentID = %q, want platform", calls[1].payload.AgentID)
	}
	if !json.Valid(calls[1].payload.Context) || !strings.Contains(string(calls[1].payload.Context), `"queue":"hooks_dispatch"`) {
		t.Fatalf("degraded context = %s, want queue marker", calls[1].payload.Context)
	}
	if calls[2].payload.ThreadID != "thread-overflow" {
		t.Fatalf("latest thread = %q, want thread-overflow", calls[2].payload.ThreadID)
	}
}

// TestHookDispatchWorkerEnqueueAfterStopDrops 固定 Stop 后的唯一丢弃路径。
// Stop 之后订阅已停止，新的 Enqueue 必须不进入队列，避免关闭后的事件重新激活 worker。
func TestHookDispatchWorkerEnqueueAfterStopDrops(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	beforeEnq := w.EnqueuedTotal()
	w.Enqueue("session.start", time.Now(), payload)
	if got := w.EnqueuedTotal(); got != beforeEnq {
		t.Errorf("EnqueuedTotal after post-Stop enqueue = %d, want %d", got, beforeEnq)
	}
	if got := len(fanout.Calls()); got != 0 {
		t.Errorf("DispatchAfter invoked after Stop: %d, want 0", got)
	}
}

// TestHookDispatchWorkerPreservesFIFOOrder 确认派发请求按入队顺序到达 fanout。
// hook observer 可能把 topic 当因果事件流消费，因此 worker 不能重排。
func TestHookDispatchWorkerPreservesFIFOOrder(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = w.Stop(ctx)
	}()

	payload := mcp.HookPayload{AgentID: "agent-A", ThreadID: "thread-A", Context: json.RawMessage(`{}`)}
	topics := []string{"session.start", "turn.after", "turn.failed", "process.exit"}
	for _, topic := range topics {
		w.Enqueue(topic, time.Now(), payload)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if w.ProcessedTotal() >= int64(len(topics)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := fanout.Calls()
	if len(calls) != len(topics) {
		t.Fatalf("DispatchAfter count = %d, want %d", len(calls), len(topics))
	}
	for i, call := range calls {
		if call.topic != topics[i] {
			t.Fatalf("calls[%d].topic = %q, want %q", i, call.topic, topics[i])
		}
	}
}

// TestEnqueueHookDispatchFiltersInvalidPayloads 固定 callback 的快速输入过滤。
// 空 topic、空 agentID 或空 context 不能进入 worker 队列，避免占用有限队列容量。
func TestEnqueueHookDispatchFiltersInvalidPayloads(t *testing.T) {
	t.Parallel()

	fanout := &fakeHookFanout{}
	w := newHookDispatchWorker(fanout, pkglogger.Get())
	w.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = w.Stop(ctx)
	}()

	validCtx := json.RawMessage(`{}`)
	cases := []struct {
		name    string
		topic   string
		payload mcp.HookPayload
	}{
		{"nil worker is a no-op", "session.start", mcp.HookPayload{AgentID: "a", Context: validCtx}},
		{"empty topic", "", mcp.HookPayload{AgentID: "a", Context: validCtx}},
		{"blank topic", "   ", mcp.HookPayload{AgentID: "a", Context: validCtx}},
		{"empty agent", "session.start", mcp.HookPayload{AgentID: "", Context: validCtx}},
		{"empty context", "session.start", mcp.HookPayload{AgentID: "a", Context: nil}},
	}

	// nil worker 是专门的 no-op 分支，必须显式传 nil 覆盖。
	enqueueHookDispatch(nil, cases[0].topic, time.Now(), cases[0].payload)
	for _, tc := range cases[1:] {
		enqueueHookDispatch(w, tc.topic, time.Now(), tc.payload)
	}

	time.Sleep(20 * time.Millisecond)
	if got := w.EnqueuedTotal(); got != 0 {
		t.Errorf("EnqueuedTotal after invalid enqueues = %d, want 0", got)
	}
	if got := len(fanout.Calls()); got != 0 {
		t.Errorf("DispatchAfter invoked for invalid payloads: %d, want 0", got)
	}
}
