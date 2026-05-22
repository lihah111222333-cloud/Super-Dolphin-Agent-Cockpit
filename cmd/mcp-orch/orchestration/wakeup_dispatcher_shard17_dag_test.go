package orchestration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformmetrics "github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
)

func TestDispatcherF151FiveNodeDAGMetricsEndpointAndAlert(t *testing.T) {
	resetDispatchRetryMetricsForTesting()
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

// TestDispatcherDAGRetryFailsAtMaxAttemptsWithFailFastCascade 验证�?
// default_retry=0（MaxAttempts=1�? fail_fast=true，AttemptCount=1（首次失败即达上限）
// �?markPermanentFail + FailNodeAndCancelDownstream(FailFast=true)，不再调 RetryWakeup�?
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

func TestDispatcherDAGPermanentFailureSkipsCascadeWhenFailWakeupFenceMisses(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "permanent-fence-miss", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{"exec":`)
	store.failRowsSet = true
	store.failRows = 0
	d := newAgentFailureClassDispatcher(t, store, errors.New("launcher should not be called"))

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 0 {
		t.Fatalf("failNodeCalls = %d, want 0 when FailWakeup fence misses", len(store.failNodeCalls))
	}
}

func TestDispatcherDAGRetryExhaustionSkipsCascadeWhenFailWakeupFenceMisses(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 15, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "exhausted-fence-miss", 2, now)
	store.failRowsSet = true
	store.failRows = 0
	d := newAgentFailureClassDispatcher(t, store, errors.New("connection refused"))

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 0 {
		t.Fatalf("failNodeCalls = %d, want 0 when FailWakeup fence misses", len(store.failNodeCalls))
	}
}

// TestDispatcherDAGRetryFailsAtMaxAttemptsNoFailFast 验证 fail_fast=false 时仍�?
// FailNodeAndCancelDownstream（store 层根�?FailFast 自决是否级联，这里只�?
// dispatcher �?FailFast=false 透传过去）�?
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

// TestDispatcherAgentFailureClassesRetryUntilMaxAttempts verifies the F1.4
// basic retry contract for AgentExecutor failure classes. transient/quota/
// validation all get the same bounded retry treatment here; smarter by_class
// routing remains F12.1.
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
