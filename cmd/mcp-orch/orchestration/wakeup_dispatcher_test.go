package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// dispatcherStubStore 是 Phase 3.1/3.2 单测用的 taskdag.Store 假实现，
// 关注 ClaimDueWakeups + MarkWakeupSent + RetryWakeup + FailWakeup 行为：
// 记录入参 + 按预设结果返回。其余方法返回安全 zero / 错误，避免
// dispatcher 误用未接通的 store 接口面。
type dispatcherStubStore struct {
	taskdag.Store // 默认 nil 嵌入，调用任何未覆盖方法都会 panic（暴露遗漏）

	claimCalls []taskdag.ClaimDueWakeupsInput
	claimReply []taskdag.Wakeup
	claimErr   error

	markSentCalls []taskdag.MarkWakeupSentInput
	markSentRows  int64
	markSentErr   error

	retryCalls []taskdag.RetryWakeupInput
	retryRows  int64 // <0 = 模拟 SQL attempt_count >= 8 触发的兜底 fail
	retryErr   error

	failCalls []taskdag.FailWakeupInput
	failRows  int64
	failErr   error

	// Phase 3.5w: DAG-aware 决策需要的 stub 字段。默认 dagReply=nil 让
	// resolveDAGRetryPolicy 退化到旧 RetryWakeup 路径，老用例不受影响。
	dagReply      *taskdag.DAG
	dagErr        error
	nodesReply    []taskdag.Node
	nodesErr      error
	failNodeCalls []taskdag.FailNodeInput
	failNodeReply *taskdag.FailNodeResult
	failNodeErr   error
	completeCalls []taskdag.CompleteNodeInput
	completeReply *taskdag.CompleteNodeWithDownstreamResult
	completeErr   error
}

func (s *dispatcherStubStore) GetDAG(_ context.Context, _ string) (*taskdag.DAG, error) {
	return s.dagReply, s.dagErr
}

func (s *dispatcherStubStore) ListNodes(_ context.Context, _ string) ([]taskdag.Node, error) {
	return s.nodesReply, s.nodesErr
}

func (s *dispatcherStubStore) FailNodeAndCancelDownstream(_ context.Context, input taskdag.FailNodeInput) (*taskdag.FailNodeResult, error) {
	s.failNodeCalls = append(s.failNodeCalls, input)
	if s.failNodeErr != nil {
		return nil, s.failNodeErr
	}
	if s.failNodeReply != nil {
		return s.failNodeReply, nil
	}
	return &taskdag.FailNodeResult{}, nil
}

func (s *dispatcherStubStore) CompleteNodeAndScheduleDownstream(_ context.Context, input taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	s.completeCalls = append(s.completeCalls, input)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	if s.completeReply != nil {
		return s.completeReply, nil
	}
	return &taskdag.CompleteNodeWithDownstreamResult{}, nil
}

func (s *dispatcherStubStore) ClaimDueWakeups(_ context.Context, input taskdag.ClaimDueWakeupsInput) ([]taskdag.Wakeup, error) {
	s.claimCalls = append(s.claimCalls, input)
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return s.claimReply, nil
}

func (s *dispatcherStubStore) MarkWakeupSent(_ context.Context, input taskdag.MarkWakeupSentInput) (int64, error) {
	s.markSentCalls = append(s.markSentCalls, input)
	if s.markSentErr != nil {
		return 0, s.markSentErr
	}
	if s.markSentRows == 0 {
		return 1, nil
	}
	return s.markSentRows, nil
}

func (s *dispatcherStubStore) RetryWakeup(_ context.Context, input taskdag.RetryWakeupInput) (int64, error) {
	s.retryCalls = append(s.retryCalls, input)
	if s.retryErr != nil {
		return 0, s.retryErr
	}
	// 默认返回 1（重试成功写入）；显式设 retryRows<0 模拟 SQL 兜底（attempt 上限）。
	if s.retryRows == 0 {
		return 1, nil
	}
	if s.retryRows < 0 {
		return 0, nil
	}
	return s.retryRows, nil
}

func (s *dispatcherStubStore) FailWakeup(_ context.Context, input taskdag.FailWakeupInput) (int64, error) {
	s.failCalls = append(s.failCalls, input)
	if s.failErr != nil {
		return 0, s.failErr
	}
	if s.failRows == 0 {
		return 1, nil
	}
	return s.failRows, nil
}

// dispatcherStubLauncher 是 WakeupLauncher 的假实现：记录每次 LaunchAgent
// 的入参 + 按预设错误队列返回。空队列时一律成功。
type dispatcherStubLauncher struct {
	calls []LaunchRequest
	errs  []error // FIFO；超出长度后默认 nil
}

func (l *dispatcherStubLauncher) LaunchAgent(_ context.Context, req LaunchRequest) error {
	l.calls = append(l.calls, req)
	if len(l.errs) == 0 {
		return nil
	}
	err := l.errs[0]
	l.errs = l.errs[1:]
	return err
}

func TestNewWakeupDispatcherRejectsNilStore(t *testing.T) {
	if _, err := NewWakeupDispatcher(nil, nil, nil, WakeupDispatcherConfig{}); err == nil {
		t.Fatalf("err = nil, want error for nil store")
	}
}

func TestWakeupDispatcherTickEmptyClaimReturnsZero(t *testing.T) {
	store := &dispatcherStubStore{} // claimReply 默认 nil → 空 slice 等价
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	if n != 0 {
		t.Fatalf("Tick claimed = %d, want 0 on empty due-set", n)
	}
	if len(store.claimCalls) != 1 {
		t.Fatalf("ClaimDueWakeups called %d times, want 1", len(store.claimCalls))
	}
}

func TestWakeupDispatcherTickPassesConfigToClaim(t *testing.T) {
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{
		ClaimedBy:     "test-dispatcher",
		LeaseInterval: "00:01:00",
		BatchLimit:    5,
	})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	if _, err := d.Tick(context.Background()); err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	got := store.claimCalls[0]
	if got.ClaimedBy != "test-dispatcher" {
		t.Fatalf("claimedBy = %q, want test-dispatcher", got.ClaimedBy)
	}
	if got.LeaseInterval != "00:01:00" {
		t.Fatalf("leaseInterval = %q, want 00:01:00", got.LeaseInterval)
	}
	if got.Limit != 5 {
		t.Fatalf("limit = %d, want 5", got.Limit)
	}
	if d.ClaimedBy() != "test-dispatcher" {
		t.Fatalf("ClaimedBy() = %q, want test-dispatcher", d.ClaimedBy())
	}
}

func TestWakeupDispatcherTickFillsDefaultsForZeroConfig(t *testing.T) {
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	if _, err := d.Tick(context.Background()); err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	got := store.claimCalls[0]
	if got.Limit != defaultWakeupClaimBatchLimit {
		t.Fatalf("default limit = %d, want %d", got.Limit, defaultWakeupClaimBatchLimit)
	}
	if got.LeaseInterval != defaultWakeupLeaseInterval {
		t.Fatalf("default leaseInterval = %q, want %q", got.LeaseInterval, defaultWakeupLeaseInterval)
	}
	if !strings.HasPrefix(got.ClaimedBy, "mcp-orch-dispatcher-") {
		t.Fatalf("default claimedBy = %q, want mcp-orch-dispatcher-N prefix", got.ClaimedBy)
	}
}

func TestWakeupDispatcherTickReturnsClaimCountAndPreservesFence(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	leaseAt := now.Add(30 * time.Second)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{
			{
				ID:             1,
				DagKey:         "dag-a",
				NodeKey:        "node-a",
				WakeupKind:     "start",
				TargetAgentID:  "agent-1",
				Status:         "dispatching",
				AttemptCount:   1,
				ClaimedAt:      &now,
				ClaimedBy:      "mcp-orch-dispatcher-7",
				LeaseExpiresAt: &leaseAt,
			},
			{
				ID:             2,
				DagKey:         "dag-a",
				NodeKey:        "node-b",
				WakeupKind:     "start",
				TargetAgentID:  "agent-2",
				Status:         "dispatching",
				AttemptCount:   2,
				ClaimedAt:      &now,
				ClaimedBy:      "mcp-orch-dispatcher-7",
				LeaseExpiresAt: &leaseAt,
			},
		},
	}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{ClaimedBy: "mcp-orch-dispatcher-7"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick err = %v", err)
	}
	if n != 2 {
		t.Fatalf("Tick claimed = %d, want 2", n)
	}
	if got := store.claimCalls[0].ClaimedBy; got != "mcp-orch-dispatcher-7" {
		t.Fatalf("claimedBy fence not propagated: got %q", got)
	}
}

func TestWakeupDispatcherTickWrapsStoreError(t *testing.T) {
	store := &dispatcherStubStore{claimErr: errors.New("boom")}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.Tick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want wrapped 'boom'", err)
	}
	if n != 0 {
		t.Fatalf("claimed = %d, want 0 on error", n)
	}
}

func TestWakeupDispatcherTickHandlesNilContextSafely(t *testing.T) {
	// dispatcher 不应该 panic 当 caller 传 nil ctx——内部回退到 Background。
	store := &dispatcherStubStore{}
	d, err := NewWakeupDispatcher(store, nil, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	if _, err := d.Tick(nil); err != nil { //nolint:staticcheck // intentionally testing nil-ctx fallback
		t.Fatalf("Tick(nil) err = %v, want graceful fallback", err)
	}
}

func TestWakeupDispatcherConfigOrDefaultsKeepsExplicitValues(t *testing.T) {
	cfg := WakeupDispatcherConfig{
		ClaimedBy:     "explicit-1",
		LeaseInterval: "00:00:45",
		BatchLimit:    7,
	}.ConfigOrDefaults()
	if cfg.ClaimedBy != "explicit-1" || cfg.LeaseInterval != "00:00:45" || cfg.BatchLimit != 7 {
		t.Fatalf("ConfigOrDefaults overrode explicit values: %+v", cfg)
	}
}

// === Phase 3.2 tests · 主循环 + launch + 状态推进 =====================

func makeClaimedWakeup(id int64, agentID string, attempt int32, now time.Time) taskdag.Wakeup {
	lease := now.Add(30 * time.Second)
	return taskdag.Wakeup{
		ID:             id,
		DagKey:         "dag-x",
		NodeKey:        "node-x",
		WakeupKind:     "start",
		TargetAgentID:  agentID,
		Status:         "dispatching",
		AttemptCount:   attempt,
		ClaimedAt:      &now,
		ClaimedBy:      "mcp-orch-dispatcher-test",
		LeaseExpiresAt: &lease,
	}
}

func TestWakeupDispatcherProcessBatchSuccessMarksSent(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeClaimedWakeup(11, "agent-X", 1, now)},
	}
	launcher := &dispatcherStubLauncher{}
	d, err := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{ClaimedBy: "mcp-orch-dispatcher-test"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("processed = %d, want 1", n)
	}
	if len(launcher.calls) != 1 || launcher.calls[0].AgentID != "agent-X" {
		t.Fatalf("launcher calls = %#v, want one with agent-X", launcher.calls)
	}
	if len(store.markSentCalls) != 1 || store.markSentCalls[0].ID != 11 {
		t.Fatalf("markSent calls = %#v, want one for wakeup 11", store.markSentCalls)
	}
	if store.markSentCalls[0].ClaimedBy != "mcp-orch-dispatcher-test" {
		t.Fatalf("markSent.ClaimedBy = %q, want fence to be carried through", store.markSentCalls[0].ClaimedBy)
	}
	if len(store.retryCalls) != 0 || len(store.failCalls) != 0 {
		t.Fatalf("on success retry/fail must not be called: retry=%d fail=%d", len(store.retryCalls), len(store.failCalls))
	}
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
	// fallback。验证 fallback 生效即可——payload 正式 schema 在 Phase 3.4 定。
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

// Phase 3.5w 新增：DAG-aware retry 决策。

// makeDAGWakeup 是 makeClaimedWakeup 的 DAG 版：DagKey/NodeKey 非空让
// markTransientRetry 走新加的 tryDAGFailWithCascade 决策路径。
func makeDAGWakeup(id int64, dagKey, nodeKey, agent string, attempt int32, ts time.Time) taskdag.Wakeup {
	w := makeClaimedWakeup(id, agent, attempt, ts)
	w.DagKey = dagKey
	w.NodeKey = nodeKey
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
}

// TestDispatcherDAGRetryFailsAtMaxAttemptsWithFailFastCascade 验证：
// default_retry=0（MaxAttempts=1）+ fail_fast=true，AttemptCount=1（首次失败即达上限）
// → markPermanentFail + FailNodeAndCancelDownstream(FailFast=true)，不再调 RetryWakeup。
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

// TestDispatcherDAGRetryFailsAtMaxAttemptsNoFailFast 验证 fail_fast=false 时仍调
// FailNodeAndCancelDownstream（store 层根据 FailFast 自决是否级联，这里只验
// dispatcher 把 FailFast=false 透传过去）。
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

func TestDispatcherAgentHardValidationFailureFailsWithoutRetry(t *testing.T) {
	now := time.Date(2026, 5, 13, 9, 30, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "bad-config", 1, now)
	store.nodesReply[0].Config = json.RawMessage(`{"exec":`)
	d := newAgentFailureClassDispatcher(t, store, errors.New("launcher should not be called"))

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 for non-retryable config validation", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1 for non-retryable config validation", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("failNodeCalls = %d, want 1 for non-retryable config validation", len(store.failNodeCalls))
	}
}

func newAgentFailureClassStore(t *testing.T, suffix string, attempt int32, now time.Time) *dispatcherStubStore {
	t.Helper()
	dagKey := "dag-f14-" + suffix
	nodeKey := "agent-" + suffix
	return &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(70+int64(attempt), dagKey, nodeKey, "agent-"+suffix, attempt, now)},
		dagReply: &taskdag.DAG{
			DagKey:   dagKey,
			Metadata: dagDefaultRetryMetadata(t, 1, true),
		},
		nodesReply: []taskdag.Node{{
			DagKey:   dagKey,
			NodeKey:  nodeKey,
			NodeType: "agent",
			Title:    nodeKey,
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"go"}`),
			Status:   string(nodeexec.NodeStatusReady),
		}},
	}
}

func newAgentFailureClassDispatcher(t *testing.T, store *dispatcherStubStore, launchErr error) *WakeupDispatcher {
	t.Helper()
	agentExec := nodeexec.NewAgentExecutor(&stubAgentLauncher{err: launchErr})
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	return d.WithNodeRouter(router)
}

// TestDispatcherNonDAGWakeupKeepsLegacyRetry 验证：Wakeup 没有 DagKey/NodeKey
// 时（非 DAG 来源），新代码不应该走 DAG 决策；保持旧 RetryWakeup 路径。
func TestDispatcherNonDAGWakeupKeepsLegacyRetry(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	w := makeClaimedWakeup(23, "agent-D", 5, now) // DagKey/NodeKey 留空
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{w},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("retryCalls = %d, want 1 (legacy path)", len(store.retryCalls))
	}
	if len(store.failNodeCalls) != 0 {
		t.Fatalf("failNodeCalls = %d, want 0 (no DAG cascade for non-DAG wakeup)", len(store.failNodeCalls))
	}
}

// Phase 3.9 新增：dispatcher 把上游产出路径注入下一节点 prompt。

// TestBuildLaunchRequestPhase39_InjectsUpstreamOutputsIntoPrompt 验证：
// payload 是 DownstreamWakeupPayload + UpstreamOutputs 非空时，prompt 含
// 路径列表 + Read 提示文案。AgentID 取 payload 优先，fallback 到 wakeup。
func TestBuildLaunchRequestPhase39_InjectsUpstreamOutputsIntoPrompt(t *testing.T) {
	payload, err := json.Marshal(taskdag.DownstreamWakeupPayload{
		AgentID: "agent-downstream",
		UpstreamOutputs: []taskdag.DownstreamUpstreamRef{
			{NodeKey: "node-A", Path: "dag/dag-x/node-A/output.json"},
			{NodeKey: "node-B", Path: "dag/dag-x/node-B/output.json"},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := buildLaunchRequestFromWakeup(taskdag.Wakeup{
		TargetAgentID: "agent-fallback",
		PromptPayload: payload,
	})
	if req.AgentID != "agent-downstream" {
		t.Fatalf("AgentID = %q, want agent-downstream (payload override)", req.AgentID)
	}
	if !strings.Contains(req.Prompt, "node-A: dag/dag-x/node-A/output.json") {
		t.Fatalf("Prompt missing node-A path:\n%s", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "node-B: dag/dag-x/node-B/output.json") {
		t.Fatalf("Prompt missing node-B path:\n%s", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "Read") {
		t.Fatalf("Prompt missing Read hint:\n%s", req.Prompt)
	}
}

// TestBuildLaunchRequestPhase39_FallsBackToWakeupAgentWhenPayloadAgentEmpty:
// payload 含 UpstreamOutputs 但 AgentID 空时退化用 wakeup.TargetAgentID。
func TestBuildLaunchRequestPhase39_FallsBackToWakeupAgentWhenPayloadAgentEmpty(t *testing.T) {
	payload, _ := json.Marshal(taskdag.DownstreamWakeupPayload{
		UpstreamOutputs: []taskdag.DownstreamUpstreamRef{
			{NodeKey: "X", Path: "dag/d/X/output.json"},
		},
	})
	req := buildLaunchRequestFromWakeup(taskdag.Wakeup{
		TargetAgentID: "agent-fallback",
		PromptPayload: payload,
	})
	if req.AgentID != "agent-fallback" {
		t.Fatalf("AgentID = %q, want fallback", req.AgentID)
	}
	if req.Prompt == "" {
		t.Fatalf("Prompt empty, want render with X path")
	}
}

// TestBuildLaunchRequestPhase39_LegacyPayloadStillWorks:
// 老式 LaunchRequest payload（无 upstream_outputs）仍然走 fallback 解析路径。
func TestBuildLaunchRequestPhase39_LegacyPayloadStillWorks(t *testing.T) {
	// 仿照 TestBuildLaunchRequestFromWakeupDecodesJSONPayload 的形状。
	legacy := `{"agent_id":"agent-legacy","prompt":"hello"}`
	req := buildLaunchRequestFromWakeup(taskdag.Wakeup{
		TargetAgentID: "agent-fallback",
		PromptPayload: json.RawMessage(legacy),
	})
	// LaunchRequest JSON tag 不是 snake_case（看类型签名是大驼峰，json 默认大驼峰），
	// 老 case 里测试拿到的是 fallback agent_id；这里只验证 Phase 3.9 新分支不破老路径。
	if strings.Contains(req.Prompt, "上游节点已完成") {
		t.Fatalf("legacy payload incorrectly routed to upstream prompt branch:\n%s", req.Prompt)
	}
}

// TestRenderUpstreamPromptHint_SkipsEmptyPathRefs 验证渲染对 path 为空的 ref
// 安静跳过（不留下 "- :" 这种空行垃圾）。
func TestRenderUpstreamPromptHint_SkipsEmptyPathRefs(t *testing.T) {
	prompt := renderUpstreamPromptHint([]taskdag.DownstreamUpstreamRef{
		{NodeKey: "A", Path: "dag/d/A/output.json"},
		{NodeKey: "B", Path: ""},
		{NodeKey: "", Path: "dag/d/anon/output.json"},
	})
	if !strings.Contains(prompt, "A: dag/d/A/output.json") {
		t.Fatalf("missing A entry:\n%s", prompt)
	}
	if strings.Contains(prompt, "- B:") || strings.Contains(prompt, "- :") {
		t.Fatalf("empty path ref leaked into prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- dag/d/anon/output.json") {
		t.Fatalf("missing anon path entry:\n%s", prompt)
	}
}
