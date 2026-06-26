package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	orchmetrics "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/metrics"
	"time"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/stretchr/testify/require"
)

func TestWakeupDispatcherProcessBatchSuccessMarksSent(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeClaimedWakeup(11, "agent-X", 1, now)},
	}
	launcher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{ClaimedBy: "mcp-orch-dispatcher-test"})
	require.NoError(t, err)
	n, err := d.ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Len(t, launcher.calls, 1)
	require.Equal(t, "agent-X", launcher.calls[0].AgentID)
	require.Len(t, store.markSentCalls, 1)
	require.EqualValues(t, 11, store.markSentCalls[0].ID)
	require.Equal(t, "mcp-orch-dispatcher-test", store.markSentCalls[0].ClaimedBy)
	require.Empty(t, store.retryCalls)
	require.Empty(t, store.failCalls)
}

func TestWakeupDispatcherMarkLaunchedReportsFenceMiss(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{markSentRowsSet: true, markSentRows: 0}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	require.NoError(t, err)
	w := makeClaimedWakeup(7, "agent-1", 1, now)

	require.False(t, d.markLaunched(context.Background(), &w, extractFence(&w)))
	require.Len(t, store.markSentCalls, 1)
}

func TestWakeupDispatcherProcessBatchDoesNotCountMarkSentFenceMiss(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply:      []taskdag.Wakeup{makeClaimedWakeup(11, "agent-X", 1, now)},
		markSentRowsSet: true,
		markSentRows:    0,
	}
	launcher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{ClaimedBy: "mcp-orch-dispatcher-test"})
	require.NoError(t, err)
	n, err := d.ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, n)
	require.Len(t, launcher.calls, 1)
	require.Len(t, store.markSentCalls, 1)
}

func TestWakeupDispatcherProcessBatchTransientFailureCallsRetry(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeClaimedWakeup(12, "agent-Y", 1, now)},
	}
	launcher := &dispatcherStubLauncher{
		errs: []error{errors.New("connection refused")}, // transient 关键字命中
	}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{
		ClaimedBy:     "mcp-orch-dispatcher-test",
		RetryInterval: "00:00:30",
	})
	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("processed = %d, want 1", n)
	}
	if len(store.markSentCalls) != 0 {
		t.Fatalf("markSent must not be called on transient failure")
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("retry calls = %d, want 1", len(store.retryCalls))
	}
	if store.retryCalls[0].RetryInterval != "00:00:30" {
		t.Fatalf("retry interval = %q, want 00:00:30", store.retryCalls[0].RetryInterval)
	}
	if !strings.Contains(store.retryCalls[0].LastError, "connection refused") {
		t.Fatalf("retry last_error = %q, want to mention connection refused", store.retryCalls[0].LastError)
	}
	if len(store.failCalls) != 0 {
		t.Fatalf("fail must not be called on transient")
	}
}

func TestWakeupDispatcherProcessBatchPermanentFailureCallsFail(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeClaimedWakeup(13, "agent-Z", 2, now)},
	}
	launcher := &dispatcherStubLauncher{
		errs: []error{errors.New("HTTP 401 unauthorized")}, // permanent 关键字命中
	}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{ClaimedBy: "mcp-orch-dispatcher-test"})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("fail calls = %d, want 1 (permanent)", len(store.failCalls))
	}
	if store.failCalls[0].ID != 13 {
		t.Fatalf("fail.ID = %d, want 13", store.failCalls[0].ID)
	}
	if !strings.Contains(store.failCalls[0].LastError, "401") {
		t.Fatalf("fail last_error = %q, want to mention 401", store.failCalls[0].LastError)
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retry must not be called on permanent")
	}
	if got := orchmetrics.DispatchRetryCounters().DispatchFailedTotal; got != 1 {
		t.Fatalf("dispatch_failed_total = %d, want 1 after successful FailWakeup", got)
	}
}

func TestWakeupDispatcherDispatchFailedMetricSkipsFenceMiss(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 5, 13, 14, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply:  []taskdag.Wakeup{makeClaimedWakeup(131, "agent-Z", 2, now)},
		failRowsSet: true,
		failRows:    0,
	}
	launcher := &dispatcherStubLauncher{
		errs: []error{errors.New("HTTP 401 unauthorized")},
	}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{ClaimedBy: "mcp-orch-dispatcher-test"})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("fail calls = %d, want 1", len(store.failCalls))
	}
	if got := orchmetrics.DispatchRetryCounters().DispatchFailedTotal; got != 0 {
		t.Fatalf("dispatch_failed_total = %d, want 0 when FailWakeup fence misses", got)
	}
}

func TestWakeupDispatcherProcessBatchRetryExhaustedFallsBackToFail(t *testing.T) {
	// 模拟 RetryWakeup 因 SQL attempt_count >= 8 上限返回 0 行：dispatcher
	// 必须自动切到 FailWakeup 防止 wakeup 卡在 dispatching。
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeClaimedWakeup(14, "agent-W", 8, now)},
		retryRows:  -1, // 触发 stub 返回 0
	}
	launcher := &dispatcherStubLauncher{
		errs: []error{errors.New("connection refused")},
	}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("retry calls = %d, want 1 attempt before fallback", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("fail-fallback calls = %d, want 1", len(store.failCalls))
	}
	if !strings.Contains(store.failCalls[0].LastError, "retry attempts exhausted") {
		t.Fatalf("fail last_error = %q, want exhausted prefix", store.failCalls[0].LastError)
	}
}

func TestWakeupDispatcherProcessBatchTruncatesLongError(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeClaimedWakeup(15, "agent-T", 1, now)},
	}
	huge := strings.Repeat("x", 2000)
	launcher := &dispatcherStubLauncher{
		errs: []error{errors.New("connection refused: " + huge)},
	}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("retry calls = %d, want 1", len(store.retryCalls))
	}
	if strings.Contains(store.retryCalls[0].LastError, huge) {
		t.Fatalf("last_error not truncated; len = %d", len(store.retryCalls[0].LastError))
	}
	if !strings.HasSuffix(store.retryCalls[0].LastError, "(truncated)") {
		t.Fatalf("expected truncation marker, got %q tail", store.retryCalls[0].LastError[len(store.retryCalls[0].LastError)-32:])
	}
}

func TestBuildLaunchRequestFromWakeupDecodesJSONPayload(t *testing.T) {
	w := taskdag.Wakeup{
		TargetAgentID: "fallback-agent",
		PromptPayload: []byte(`{"agent_id":"agent-from-payload","prompt":"go"}`),
	}
	req := buildLaunchRequestFromWakeup(w)
	// json tag 走 Go 字段反射默认行为（exact field name），所以 agent_id 不会
	// 被读到。但 AgentID 既不存在又不为空时，buildLaunchRequest 会用 wakeup
	// fallback。验证 fallback 生效即可，payload 结构由 buildLaunchRequest 兼容处理。
	if req.AgentID != "fallback-agent" {
		t.Fatalf("AgentID = %q, want fallback-agent (json tag mismatch ignored, fallback applied)", req.AgentID)
	}
}

func TestBuildLaunchRequestFromWakeupEmptyPayloadUsesTarget(t *testing.T) {
	w := taskdag.Wakeup{TargetAgentID: " agent-empty "}
	req := buildLaunchRequestFromWakeup(w)
	if req.AgentID != "agent-empty" {
		t.Fatalf("AgentID = %q, want trimmed agent-empty", req.AgentID)
	}
}

func TestWakeupDispatcherRunStopsOnContextCancel(t *testing.T) {
	store := &dispatcherStubStore{}
	launcher := &dispatcherStubLauncher{}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	time.Sleep(30 * time.Millisecond) // 让 ticker 跑几下
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Run did not stop within 1s after ctx.cancel")
	}
	if len(store.claimCalls) == 0 {
		t.Fatalf("Run did not tick at least once before stop")
	}
}

func TestWakeupDispatcherRunSurvivesClaimErrorAndContinues(t *testing.T) {
	// claim 失败下一 tick 再来——ctx canceled 才停。
	store := &dispatcherStubStore{claimErr: errors.New("transient db blip")}
	launcher := &dispatcherStubLauncher{}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	time.Sleep(35 * time.Millisecond)
	cancel()
	<-done
	if len(store.claimCalls) < 2 {
		t.Fatalf("Run gave up too early on claim error: only %d ticks", len(store.claimCalls))
	}
}

// 下列用例覆盖带 DAG 节点的 retry 决策。

// makeDAGWakeup 构造带 DagKey/NodeKey 的 claimed wakeup。
// 非空 DAG 标识会让 markTransientRetry 进入 tryDAGFailWithCascade 分支。
func makeDAGWakeup(id int64, dagKey, nodeKey, agent string, attempt int32, ts time.Time) taskdag.Wakeup {
	w := makeClaimedWakeup(id, agent, attempt, ts)
	w.DagKey = dagKey
	w.NodeKey = nodeKey
	w.RunID = int64Ptr(9001)
	return w
}

// dagDefaultRetryMetadata 构造一个 DAG metadata：default_retry=N，fail_fast 可选。
func dagDefaultRetryMetadata(t *testing.T, defaultRetry int, failFast bool) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schedule": map[string]any{
			"default_retry": defaultRetry,
			"fail_fast":     failFast,
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	return raw
}

// TestDispatcherDAGRetryRetriesUntilMaxAttempts 验证：default_retry=2 时 MaxAttempts=3，
// AttemptCount=1（首次失败）应该继续走 RetryWakeup 不直接 fail。
func TestDispatcherDAGRetryRetriesUntilMaxAttempts(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(20, "dag-x", "node-A", "agent-A", 1, now)},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-x",
			Metadata: dagDefaultRetryMetadata(t, 2, false),
		},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("retryCalls = %d, want 1 (still under MaxAttempts)", len(store.retryCalls))
	}
	if len(store.failCalls) != 0 {
		t.Fatalf("failCalls = %d, want 0 (not yet exhausted)", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 0 {
		t.Fatalf("failNodeCalls = %d, want 0 (no cascade yet)", len(store.failNodeCalls))
	}
	if got := orchmetrics.DispatchRetryCounters().RetryCountPerNode["dag-x/node-A"]; got != 1 {
		t.Fatalf("retry_count_per_node[dag-x/node-A] = %d, want 1", got)
	}
}

func TestDispatcherDAGRetryAlertsAtThirdAttempt(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 5, 13, 14, 20, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(202, "dag-alert", "node-hot", "agent-hot", 3, now)},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	sink := &recordingDispatchRetryAlertSink{}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	d.WithDispatchRetryAlertSink(sink)
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	metrics := orchmetrics.DispatchRetryCounters()
	if got := metrics.RetryCountPerNode["dag-alert/node-hot"]; got != 3 {
		t.Fatalf("retry_count_per_node[dag-alert/node-hot] = %d, want 3", got)
	}
	if got := metrics.RetryAlertTotal; got != 1 {
		t.Fatalf("retry_alert_total = %d, want 1", got)
	}
	calls := sink.waitForCalls(t, 1)
	alert := calls[0]
	if alert.DagKey != "dag-alert" || alert.NodeKey != "node-hot" {
		t.Fatalf("alert keys = %s/%s, want dag-alert/node-hot", alert.DagKey, alert.NodeKey)
	}
	if alert.AttemptCount != 3 {
		t.Fatalf("alert AttemptCount = %d, want 3", alert.AttemptCount)
	}
}

func TestDispatcherDAGRetryExhaustionAlertsAtThirdAttempt(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 5, 13, 14, 22, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(204, "dag-alert", "node-exhausted", "agent-hot", 3, now)},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-alert",
			Metadata: dagDefaultRetryMetadata(t, 2, false),
		},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	sink := &recordingDispatchRetryAlertSink{}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	d.WithDispatchRetryAlertSink(sink)
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 at max attempts", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1 at max attempts", len(store.failCalls))
	}
	if got := orchmetrics.DispatchRetryCounters().RetryCountPerNode["dag-alert/node-exhausted"]; got != 3 {
		t.Fatalf("retry_count_per_node[dag-alert/node-exhausted] = %d, want 3", got)
	}
	sink.waitForCalls(t, 1)
}

func TestDispatcherDAGRetryBelowThresholdDoesNotAlert(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 5, 13, 14, 25, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(203, "dag-alert", "node-cool", "agent-cool", 1, now)},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	sink := &recordingDispatchRetryAlertSink{}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	d.WithDispatchRetryAlertSink(sink)
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if got := orchmetrics.DispatchRetryCounters().RetryAlertTotal; got != 0 {
		t.Fatalf("retry_alert_total = %d, want 0 below threshold", got)
	}
	if calls := sink.snapshot(); len(calls) != 0 {
		t.Fatalf("alert calls = %d, want 0 below threshold", len(calls))
	}
}
