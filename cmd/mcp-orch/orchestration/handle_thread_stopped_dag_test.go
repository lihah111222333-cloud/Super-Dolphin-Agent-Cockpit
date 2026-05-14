package orchestration

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/kelindar/event"
)

// fakeFallbackLookup 是 NodeSpawningThreadLookup 的测试桩。
type fakeFallbackLookup struct {
	nodes []taskdag.Node
	err   error
	calls atomic.Int64
}

func (f *fakeFallbackLookup) LookupNodesBySpawningThread(_ context.Context, _ string) ([]taskdag.Node, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.nodes, nil
}

// fakeFallbackFlow 是 NodeFlowStore 的测试桩，仅 FailNodeAndCancelDownstream 真实有效。
type fakeFallbackFlow struct {
	failErr   error
	failCalls atomic.Int64
	lastInput taskdag.FailNodeInput
}

func (f *fakeFallbackFlow) FailNodeAndCancelDownstream(_ context.Context, input taskdag.FailNodeInput) (*taskdag.FailNodeResult, error) {
	f.failCalls.Add(1)
	f.lastInput = input
	if f.failErr != nil {
		return nil, f.failErr
	}
	return &taskdag.FailNodeResult{}, nil
}

func (f *fakeFallbackFlow) CompleteNodeAndScheduleDownstream(_ context.Context, _ taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	return nil, errors.New("fakeFallbackFlow: CompleteNodeAndScheduleDownstream not used by thread.stopped fallback")
}

func (f *fakeFallbackFlow) UpdateNodeStatusFlexible(_ context.Context, _ taskdag.FlexibleNodeStatusUpdate) (*taskdag.Node, error) {
	return nil, errors.New("fakeFallbackFlow: UpdateNodeStatusFlexible not used by thread.stopped fallback")
}

// hookConsumerWithFallback wires hookConsumer + fake fallback ports. Tap nil.
// 复用 hook_consumer_notify_test.go 的 NewService 构造范式。
func hookConsumerWithFallback(t *testing.T, lookup taskdag.NodeSpawningThreadLookup, flow taskdag.NodeFlowStore) *hookConsumer {
	t.Helper()
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	return newHookConsumerInternal(svc, silentLogger(), nil, lookup, flow)
}

// fallbackMetricDelta snapshots dagFallbackMetrics, runs fn, returns post delta.
// 不用 metricDelta 名字避与 dag_turn_completed_subscriber_test.go 冲突。
func fallbackMetricDelta(t *testing.T, fn func()) DAGFallbackMetrics {
	t.Helper()
	before := DAGFallbackCounters()
	fn()
	after := DAGFallbackCounters()
	return DAGFallbackMetrics{
		LookupFailed:      after.LookupFailed - before.LookupFailed,
		NoNode:            after.NoNode - before.NoNode,
		IdempotentSkipped: after.IdempotentSkipped - before.IdempotentSkipped,
		Failed:            after.Failed - before.Failed,
		FailNodeErr:       after.FailNodeErr - before.FailNodeErr,
	}
}

// Case 1: fallback 触发 — 节点 ready → FailNode + metric Failed=1.
func TestThreadStoppedDAGFallback_FailsReadyNode(t *testing.T) {
	lookup := &fakeFallbackLookup{
		nodes: []taskdag.Node{{DagKey: "dag-a", NodeKey: "node-1", Status: "ready"}},
	}
	flow := &fakeFallbackFlow{}
	hc := hookConsumerWithFallback(t, lookup, flow)

	delta := fallbackMetricDelta(t, func() {
		hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")
	})

	if lookup.calls.Load() != 1 {
		t.Fatalf("expected 1 lookup call, got %d", lookup.calls.Load())
	}
	if flow.failCalls.Load() != 1 {
		t.Fatalf("expected 1 FailNode call, got %d", flow.failCalls.Load())
	}
	if flow.lastInput.DagKey != "dag-a" || flow.lastInput.NodeKey != "node-1" {
		t.Fatalf("FailNodeInput mismatch: %+v", flow.lastInput)
	}
	if flow.lastInput.Reason != "thread_stopped_fallback" {
		t.Fatalf("expected Reason=thread_stopped_fallback, got %q", flow.lastInput.Reason)
	}
	if delta.Failed != 1 {
		t.Fatalf("expected metric Failed=1, got %+v", delta)
	}
}

func TestThreadStoppedDAGFallback_InvokesLifecycleHooks(t *testing.T) {
	events := []string{}
	lookup := &fakeFallbackLookup{
		nodes: []taskdag.Node{{
			DagKey:   "dag-a",
			NodeKey:  "node-1",
			NodeType: "agent",
			Status:   "running",
			Config:   []byte(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
		}},
	}
	flow := &fakeFallbackFlow{}
	agentExec := nodeexec.NewAgentExecutor(&stubAgentLauncher{}, nodeexec.WithHooks(recordingLifecycleHooks(&events)))
	router := NewNodeExecutorRouter(&stubRouterStore{}, agentExec, nil, nil, nil, nil)
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	hc := newHookConsumerInternal(
		svc,
		silentLogger(),
		nil,
		lookup,
		flow,
		withHookTurnCompletedDAGDeps(DAGSubscriberDeps{NodeRouter: router}),
	)

	hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")

	want := []string{
		"on_state_change:node-1:failed",
		"on_failure:node-1:failed",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// Case 2: 节点已 done — fallback 跳过 + metric IdempotentSkipped=1.
func TestThreadStoppedDAGFallback_SkipsTerminalNode(t *testing.T) {
	lookup := &fakeFallbackLookup{
		nodes: []taskdag.Node{{DagKey: "dag-a", NodeKey: "node-1", Status: "done"}},
	}
	flow := &fakeFallbackFlow{}
	hc := hookConsumerWithFallback(t, lookup, flow)

	delta := fallbackMetricDelta(t, func() {
		hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")
	})

	if flow.failCalls.Load() != 0 {
		t.Fatalf("expected 0 FailNode calls (terminal node skip), got %d", flow.failCalls.Load())
	}
	if delta.IdempotentSkipped != 1 {
		t.Fatalf("expected metric IdempotentSkipped=1, got %+v", delta)
	}
}

func TestThreadStoppedDAGFallback_SkipsAwaitingVerifyNode(t *testing.T) {
	lookup := &fakeFallbackLookup{
		nodes: []taskdag.Node{{DagKey: "dag-a", NodeKey: "node-1", Status: "awaiting_verify"}},
	}
	flow := &fakeFallbackFlow{}
	hc := hookConsumerWithFallback(t, lookup, flow)

	delta := fallbackMetricDelta(t, func() {
		hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")
	})

	if flow.failCalls.Load() != 0 {
		t.Fatalf("expected 0 FailNode calls while output materialization may complete, got %d", flow.failCalls.Load())
	}
	if delta.IdempotentSkipped != 1 {
		t.Fatalf("expected metric IdempotentSkipped=1, got %+v", delta)
	}
}

// Case 3: 反查失败 — DB 错 → log warn + metric LookupFailed=1，不抛 error。
func TestThreadStoppedDAGFallback_LookupFailedLogsAndContinues(t *testing.T) {
	lookup := &fakeFallbackLookup{err: errors.New("db down")}
	flow := &fakeFallbackFlow{}
	hc := hookConsumerWithFallback(t, lookup, flow)

	delta := fallbackMetricDelta(t, func() {
		// 不应 panic 不应 抛 error（runThreadStoppedDAGFallback 不返 error）
		hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")
	})

	if flow.failCalls.Load() != 0 {
		t.Fatalf("expected 0 FailNode calls (lookup failed), got %d", flow.failCalls.Load())
	}
	if delta.LookupFailed != 1 {
		t.Fatalf("expected metric LookupFailed=1, got %+v", delta)
	}
}

// Case 4: DAG FailNode 失败 — log warn + metric FailNodeErr=1，不抛 error。
func TestThreadStoppedDAGFallback_FailNodeErrLogsAndContinues(t *testing.T) {
	lookup := &fakeFallbackLookup{
		nodes: []taskdag.Node{
			{DagKey: "dag-a", NodeKey: "node-1", Status: "ready"},
			{DagKey: "dag-b", NodeKey: "node-2", Status: "running"},
		},
	}
	flow := &fakeFallbackFlow{failErr: errors.New("pg constraint conflict")}
	hc := hookConsumerWithFallback(t, lookup, flow)

	delta := fallbackMetricDelta(t, func() {
		hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")
	})

	if flow.failCalls.Load() != 2 {
		t.Fatalf("expected 2 FailNode calls (both nodes attempted), got %d", flow.failCalls.Load())
	}
	if delta.FailNodeErr != 2 {
		t.Fatalf("expected metric FailNodeErr=2, got %+v", delta)
	}
	if delta.Failed != 0 {
		t.Fatalf("expected metric Failed=0 (all FailNode errored), got %+v", delta)
	}
}

// Case 5: 双路径互不影响 — fallback ports nil 时（runtime-only / 测试），
// handleThreadStopped 现有 agent runtime 推进逻辑仍正常工作；fallback 函数
// 短路 return 不触发任何 metric。
func TestThreadStoppedDAGFallback_NilPortsShortCircuit(t *testing.T) {
	hc := hookConsumerWithFallback(t, nil, nil) // 两个 ports 都 nil

	delta := fallbackMetricDelta(t, func() {
		hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")
	})

	if delta != (DAGFallbackMetrics{}) {
		t.Fatalf("expected zero metric delta on nil ports, got %+v", delta)
	}
}

// Case 5b: threadID 空 — 也短路 return，不触发 lookup。
func TestThreadStoppedDAGFallback_EmptyThreadIDShortCircuit(t *testing.T) {
	lookup := &fakeFallbackLookup{}
	flow := &fakeFallbackFlow{}
	hc := hookConsumerWithFallback(t, lookup, flow)

	delta := fallbackMetricDelta(t, func() {
		hc.runThreadStoppedDAGFallback(context.Background(), "   ")
	})

	if lookup.calls.Load() != 0 {
		t.Fatalf("expected 0 lookup calls on empty threadID, got %d", lookup.calls.Load())
	}
	if delta != (DAGFallbackMetrics{}) {
		t.Fatalf("expected zero metric delta, got %+v", delta)
	}
}

// Case 6 (extra): 反查空 — 0 nodes → metric NoNode=1.
func TestThreadStoppedDAGFallback_NoNodeMetric(t *testing.T) {
	lookup := &fakeFallbackLookup{nodes: nil}
	flow := &fakeFallbackFlow{}
	hc := hookConsumerWithFallback(t, lookup, flow)

	delta := fallbackMetricDelta(t, func() {
		hc.runThreadStoppedDAGFallback(context.Background(), "thread-1")
	})

	if delta.NoNode != 1 {
		t.Fatalf("expected metric NoNode=1, got %+v", delta)
	}
	if flow.failCalls.Load() != 0 {
		t.Fatalf("expected 0 FailNode calls, got %d", flow.failCalls.Load())
	}
}
