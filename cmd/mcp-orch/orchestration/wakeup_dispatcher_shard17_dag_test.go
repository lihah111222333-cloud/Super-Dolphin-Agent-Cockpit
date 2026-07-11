package orchestration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	orchmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/metrics"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	taskdag "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
)

func TestDispatcherF151FiveNodeDAGMetricsEndpointAndAlert(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 5, 13, 14, 30, 0, 0, time.UTC)
	nodes := make([]taskdag.Node, 0, 5)
	for i := 1; i <= 5; i++ {
		runID := int64(9001)
		nodes = append(nodes, taskdag.Node{
			DagKey:   "dag-five",
			NodeKey:  "node-" + strconv.Itoa(i),
			RunID:    &runID,
			NodeType: "agent",
			Config:   testRawConfig(t, `{"exec":{"agent_key":"metrics","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
			Status:   string(nodeexec.NodeStatusReady),
		})
	}
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(205, "dag-five", "node-3", "agent-hot", 3, now)},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-five",
			Metadata: dagDefaultRetryMetadata(t, 5, false),
		},
		nodesReply: nodes,
	}
	launcher := &dispatcherStubLauncher{
		errs: []error{errors.New("connection refused")},
	}
	agentLauncher := &stubAgentLauncher{
		threadID: "thread-five",
		errs:     []error{errors.New("connection refused")},
	}
	sink := &recordingDispatchRetryAlertSink{}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	d.WithNodeRouter(NewNodeExecutorRouter(store, newTestAgentExecutor(agentLauncher, nil), nil, nil, nil, nil))
	d.WithDispatchRetryAlertSink(sink)
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	sink.waitForCalls(t, 1)
	store.claimReply = []taskdag.Wakeup{
		makeDAGWakeup(211, "dag-five", "node-1", "agent-1", 1, now),
		makeDAGWakeup(212, "dag-five", "node-2", "agent-2", 1, now),
		makeDAGWakeup(213, "dag-five", "node-3", "agent-hot", 4, now),
		makeDAGWakeup(214, "dag-five", "node-4", "agent-4", 1, now),
		makeDAGWakeup(215, "dag-five", "node-5", "agent-5", 1, now),
	}
	if processed, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch second err = %v", err)
	} else if processed != 5 {
		t.Fatalf("second batch processed = %d, want 5", processed)
	}
	if len(store.markSentCalls) != 5 {
		t.Fatalf("markSentCalls = %d, want 5-node DAG run-through", len(store.markSentCalls))
	}
	store.claimReply = []taskdag.Wakeup{makeClaimedWakeup(216, "agent-fail", 1, now)}
	launcher.errs = []error{errors.New("HTTP 401 unauthorized")}
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch failure err = %v", err)
	}
	rec := httptest.NewRecorder()
	platformmetrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, platformmetrics.PrometheusMetricsPath, nil))
	if body := rec.Body.String(); !strings.Contains(body, "dispatch_failed_total 1") ||
		!strings.Contains(body, `retry_count_per_node{dag_key="dag-five",node_key="node-3"} 3`) {
		t.Fatalf("metrics endpoint missing F15.1 counters:\n%s", body)
	}
}

// TestDispatcherDAGRetryFailsAtMaxAttemptsWithFailFastCascade 验证 fail_fast DAG 达到最大重试时的级联失败。
// default_retry=0 表示首次失败即达上限，dispatcher 应写 permanent fail 并取消下游，不再写 RetryWakeup。
func TestDispatcherDAGRetryFailsAtMaxAttemptsWithFailFastCascade(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(21, "dag-y", "node-B", "agent-B", 1, now)},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-y",
			Metadata: dagDefaultRetryMetadata(t, 0, true),
		},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("network unreachable")}}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 (skipped at max)", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1 (permanent fail)", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("failNodeCalls = %d, want 1 (cascade)", len(store.failNodeCalls))
	}
	if !store.failNodeCalls[0].FailFast {
		t.Fatalf("FailNodeInput.FailFast = false, want true")
	}
	if store.failNodeCalls[0].DagKey != "dag-y" || store.failNodeCalls[0].NodeKey != "node-B" {
		t.Fatalf("FailNodeInput key wrong: %+v", store.failNodeCalls[0])
	}
}

func TestDispatcherPermanentFailurePublishesFailedEventWithOldStatus(t *testing.T) {
	dispatcher := event.NewDispatcher()
	events := make(chan taskdto.TaskNodeStatusChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev taskdto.TaskNodeStatusChanged) { events <- ev })
	defer cancel()

	now := time.Date(2026, 5, 14, 16, 0, 0, 0, time.UTC)
	runID := int64(9001)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(31, "dag-y", "node-B", "agent-B", 1, now)},
		dagReply:   &taskdag.DAG{DagKey: "dag-y", Metadata: dagDefaultRetryMetadata(t, 0, false)},
		nodesReply: []taskdag.Node{{
			DagKey:   "dag-y",
			NodeKey:  "node-B",
			RunID:    &runID,
			NodeType: "agent",
			Status:   string(nodeexec.NodeStatusReady),
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
		}},
		failNodeReply: &taskdag.FailNodeResult{
			OldStatus: string(nodeexec.NodeStatusReady),
			Node: &taskdag.Node{
				DagKey:  "dag-y",
				NodeKey: "node-B",
				RunID:   &runID,
				Status:  string(nodeexec.NodeStatusFailed),
			},
		},
	}
	d, _ := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	d.WithNodeRouter(NewNodeExecutorRouter(store, nil, nil, nil, nil, nil).WithEventBus(dispatcher))
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}

	select {
	case got := <-events:
		if got.DagKey != "dag-y" || got.NodeKey != "node-B" || got.RunID != runID {
			t.Fatalf("event identity = %s/%s/%d, want dag-y/node-B/%d", got.DagKey, got.NodeKey, got.RunID, runID)
		}
		if got.OldStatus != string(nodeexec.NodeStatusReady) || got.NewStatus != string(nodeexec.NodeStatusFailed) {
			t.Fatalf("event status = %q -> %q, want ready -> failed", got.OldStatus, got.NewStatus)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TaskNodeStatusChanged")
	}
}

func TestDispatcherDAGPermanentFailureSkipsCascadeWhenFailWakeupFenceMisses(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "permanent-fence-miss", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{"exec":`)
	store.failRowsSet = true
	store.failRows = 0
	d := newAgentFailureClassDispatcher(t, store, errors.New("launcher should not be called"))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 0 {
		t.Fatalf("ProcessBatch handled = %d, want 0 when FailWakeup fence misses", n)
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 0 {
		t.Fatalf("failNodeCalls = %d, want 0 when FailWakeup fence misses", len(store.failNodeCalls))
	}
}

func TestDispatcherDAGPermanentFailureIsAtomicWhenCascadeFails(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 5, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "permanent-cascade-fails", 1, now)
	store.failNodeErr = errors.New("cascade write failed")
	d := newAgentFailureClassDispatcher(t, store, errors.New("launcher should not be called"))
	w := store.claimReply[0]

	handled := d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassHard,
		ErrorSummary: "hard failure",
	})

	if handled {
		t.Fatal("handleFailedRouterOutcome handled = true, want false when atomic wakeup+node failure rolls back")
	}
	if len(store.atomicFailCalls) != 1 {
		t.Fatalf("atomicFailCalls = %d, want 1", len(store.atomicFailCalls))
	}
	if len(store.failCalls) != 0 {
		t.Fatalf("committed FailWakeup calls = %d, want 0 after cascade error rollback", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("failNodeCalls = %d, want 1 attempted cascade", len(store.failNodeCalls))
	}
}

func TestDispatcherDAGRetryExhaustionSkipsCascadeWhenFailWakeupFenceMisses(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 15, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "exhausted-fence-miss", 2, now)
	store.failRowsSet = true
	store.failRows = 0
	d := newAgentFailureClassDispatcher(t, store, errors.New("connection refused"))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 0 {
		t.Fatalf("ProcessBatch handled = %d, want 0 when FailWakeup fence misses", n)
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 0 {
		t.Fatalf("failNodeCalls = %d, want 0 when FailWakeup fence misses", len(store.failNodeCalls))
	}
}

// TestDispatcherDAGRetryFailsAtMaxAttemptsNoFailFast 验证非 fail_fast DAG 达到最大重试时的失败写入。
// dispatcher 仍会调用 FailNodeAndCancelDownstream，但必须把 FailFast=false 原样传给 store 层。
func TestDispatcherDAGRetryFailsAtMaxAttemptsNoFailFast(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(22, "dag-z", "node-C", "agent-C", 1, now)},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-z",
			Metadata: dagDefaultRetryMetadata(t, 0, false),
		},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("timeout")}}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("failNodeCalls = %d, want 1", len(store.failNodeCalls))
	}
	if store.failNodeCalls[0].FailFast {
		t.Fatalf("FailNodeInput.FailFast = true, want false")
	}
}

func TestDispatcherDAGRetryPolicyFailsFastWhenRunNodesUnavailable(t *testing.T) {
	now := time.Date(2026, 5, 14, 16, 30, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(32, "dag-policy", "node-A", "agent-A", 1, now)},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-policy",
			Metadata: dagDefaultRetryMetadata(t, 5, false),
		},
		nodesErr: errors.New("run nodes unavailable"),
	}
	d, _ := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 when retry policy node lookup fails", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1 fail-fast write", len(store.failCalls))
	}
	if !strings.Contains(store.failCalls[0].LastError, "list run nodes for retry policy") {
		t.Fatalf("FailWakeup LastError = %q, want list run nodes error", store.failCalls[0].LastError)
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("failNodeCalls = %d, want 1 DAG node failure", len(store.failNodeCalls))
	}
}

// TestDispatcherAgentFailureClassesRetryUntilMaxAttempts 固定 agent 执行失败分类的有界重试。
// transient/quota/validation 在这里都走相同重试上限；更细粒度的 by_class 路由不由本测试覆盖。
func TestDispatcherAgentFailureClassesRetryUntilMaxAttempts(t *testing.T) {
	now := time.Date(2026, 5, 13, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		launchErr error
	}{
		{name: "transient", launchErr: errors.New("connection refused")},
		{name: "quota", launchErr: errors.New("context_length_exceeded")},
		{name: "validation", launchErr: errors.New("401 unauthorized")},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name+"_first_failure_retries", func(t *testing.T) {
			store := newAgentFailureClassStore(t, tt.name, 1, now)
			d := newAgentFailureClassDispatcher(t, store, tt.launchErr)
			if _, err := d.ProcessBatch(context.Background()); err != nil {
				t.Fatalf("ProcessBatch err = %v", err)
			}
			if len(store.retryCalls) != 1 {
				t.Fatalf("retryCalls = %d, want 1", len(store.retryCalls))
			}
			if len(store.failCalls) != 0 {
				t.Fatalf("failCalls = %d, want 0 before max attempts", len(store.failCalls))
			}
			if len(store.failNodeCalls) != 0 {
				t.Fatalf("failNodeCalls = %d, want 0 before max attempts", len(store.failNodeCalls))
			}
		})
		t.Run(tt.name+"_second_failure_exhausts", func(t *testing.T) {
			store := newAgentFailureClassStore(t, tt.name, 2, now)
			d := newAgentFailureClassDispatcher(t, store, tt.launchErr)
			if _, err := d.ProcessBatch(context.Background()); err != nil {
				t.Fatalf("ProcessBatch err = %v", err)
			}
			if len(store.retryCalls) != 0 {
				t.Fatalf("retryCalls = %d, want 0 at max attempts", len(store.retryCalls))
			}
			if len(store.failCalls) != 1 {
				t.Fatalf("failCalls = %d, want 1 at max attempts", len(store.failCalls))
			}
			if len(store.failNodeCalls) != 1 {
				t.Fatalf("failNodeCalls = %d, want 1 at max attempts", len(store.failNodeCalls))
			}
		})
	}
}
