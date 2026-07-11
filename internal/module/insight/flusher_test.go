package insight

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	observation "github.com/anthropic-ai/super-agent-v3/internal/dto/observation"
	moduleobs "github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
)

// fakeInsightStore 捕获 flusher 尝试持久化的参数，未注入回调的方法保持空结果。
type fakeInsightStore struct {
	upsertFn        func(context.Context, UpsertParams) (Record, error)
	listRecentFn    func(context.Context, int32) ([]Record, error)
	listByThreadFn  func(context.Context, string, int32) ([]Record, error)
	listApprovalsFn func(context.Context, string, int32) ([]ApprovalRow, error)
}

func (f *fakeInsightStore) Upsert(ctx context.Context, p UpsertParams) (Record, error) {
	if f.upsertFn != nil {
		return f.upsertFn(ctx, p)
	}
	return Record{ID: 1, ThreadID: p.ThreadID, LocalTurnID: p.LocalTurnID, Status: p.Status}, nil
}
func (f *fakeInsightStore) ListByThread(ctx context.Context, threadID string, limit int32) ([]Record, error) {
	if f.listByThreadFn != nil {
		return f.listByThreadFn(ctx, threadID, limit)
	}
	return nil, nil
}
func (f *fakeInsightStore) ListRecent(ctx context.Context, limit int32) ([]Record, error) {
	if f.listRecentFn != nil {
		return f.listRecentFn(ctx, limit)
	}
	return nil, nil
}
func (f *fakeInsightStore) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalRow, error) {
	if f.listApprovalsFn != nil {
		return f.listApprovalsFn(ctx, threadID, limit)
	}
	return nil, nil
}

func newTestFlusher(t *testing.T, obs observation.Contract, store Writer) (*Flusher, *collector) {
	t.Helper()
	col := newCollector(slog.Default(), 16)
	f := NewFlusher(slog.Default(), obs, store, col)
	f.drainTimeout = 100 * time.Millisecond
	f.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	return f, col
}

// TestFlusherBuildsUpsertFromObservation 验证单个终态信号会从 observation 读取完整事实并写入 upsert 参数。
func TestFlusherBuildsUpsertFromObservation(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	seedCompletedFlusherObservation(mem)

	var got UpsertParams
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, p UpsertParams) (Record, error) {
			got = p
			return Record{ID: 1}, nil
		},
	}
	f, _ := newTestFlusher(t, mem, store)
	f.handle(context.Background(), flushSignal{
		LocalTurnID: "local-1",
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Provider:    "codex",
	})

	assertCompletedFlusherUpsert(t, got)
}

func seedCompletedFlusherObservation(mem *moduleobs.Memory) {
	start := time.Unix(1_700_000_000, 0).UTC()
	end := start.Add(3 * time.Second)
	mem.MapTurn("local-1", "provider-1")
	mem.RecordStartedAt("local-1", start)
	mem.RecordCompletedAt("local-1", end)
	successTrue := true
	mem.RecordTerminal("local-1", observation.Terminal{
		Kind:    observation.TerminalCompleted,
		Success: &successTrue,
		Reason:  "",
	})
	mem.RecordTokens("local-1", observation.TokenSnapshot{Input: 11, Output: 22, Total: 33, Observed: true, Projection: "turn"})
	mem.IncrementToolCalls("local-1")
	mem.IncrementToolCalls("local-1")
	mem.IncrementToolFailures("local-1")
	mem.IncrementApprovalRequests("local-1")
	mem.SetSkillsSelected("local-1", []string{"skill-a", "skill-b"})
}

func assertCompletedFlusherUpsert(t *testing.T, got UpsertParams) {
	t.Helper()
	assertCompletedFlusherStatus(t, got)
	assertCompletedFlusherCounts(t, got)
	assertCompletedFlusherIdentity(t, got)
}

func assertCompletedFlusherStatus(t *testing.T, got UpsertParams) {
	t.Helper()
	if got.Status != insightStatusCompleted {
		t.Fatalf("Status = %q, want completed", got.Status)
	}
	if got.Success == nil || !*got.Success {
		t.Fatalf("Success = %v, want pointer to true", got.Success)
	}
	if got.DurationMS != 3000 {
		t.Fatalf("DurationMS = %d, want 3000 (3s)", got.DurationMS)
	}
}

func assertCompletedFlusherCounts(t *testing.T, got UpsertParams) {
	t.Helper()
	if got.ToolCalls != 2 || got.ToolFailures != 1 || !got.ToolCallsObserved || !got.ToolFailuresObserved {
		t.Fatalf("tool counts wrong: calls=%d callsObserved=%t failures=%d failuresObserved=%t", got.ToolCalls, got.ToolCallsObserved, got.ToolFailures, got.ToolFailuresObserved)
	}
	if got.ApprovalRequests != 1 || !got.ApprovalRequestsObserved {
		t.Fatalf("approval snapshot wrong: %+v", got)
	}
	if got.TokenInput != 11 || got.TokenOutput != 22 || got.TokenTotal != 33 {
		t.Fatalf("token counts wrong: %+v", got)
	}
}

func assertCompletedFlusherIdentity(t *testing.T, got UpsertParams) {
	t.Helper()
	if got.ProviderTurnID != "provider-1" {
		t.Fatalf("ProviderTurnID = %q, want provider-1 (from MapTurn)", got.ProviderTurnID)
	}
	if string(got.SkillsSelected) != `["skill-a","skill-b"]` {
		t.Fatalf("SkillsSelected = %q", got.SkillsSelected)
	}
}

// TestFlusherMapsUnknownTerminalToStatusUnknown 验证未知终态会落成 insight 的 unknown 状态。
func TestFlusherMapsUnknownTerminalToStatusUnknown(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	mem.RecordTerminal("t", observation.Terminal{Kind: observation.TerminalUnknown})

	var got UpsertParams
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, p UpsertParams) (Record, error) {
			got = p
			return Record{}, nil
		},
	}
	f, _ := newTestFlusher(t, mem, store)
	f.handle(context.Background(), flushSignal{LocalTurnID: "t", ThreadID: "th"})
	if got.Status != insightStatusUnknown {
		t.Fatalf("Status = %q, want %q", got.Status, insightStatusUnknown)
	}
}

// TestFlusherRequeuesWhenObservationEmpty 覆盖 collector 先于 observation 订阅者写入终态的竞争。
func TestFlusherRequeuesWhenObservationEmpty(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory() // intentionally empty
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, _ UpsertParams) (Record, error) {
			t.Fatal("Upsert should not run when observation has no terminal")
			return Record{}, nil
		},
	}
	f, col := newTestFlusher(t, mem, store)
	col.queue <- flushSignal{LocalTurnID: "local-missing", ThreadID: "t"}
	f.handle(context.Background(), <-col.queue)
	if len(col.queue) != 1 {
		t.Fatalf("signal should have been requeued; queue=%d", len(col.queue))
	}
	sig := <-col.queue
	if !sig.Retried {
		t.Fatalf("requeued signal must carry retry marker: %+v", sig)
	}
	f.handle(context.Background(), sig)
	if len(col.queue) != 0 {
		t.Fatalf("retried signal must be dropped after second miss; queue=%d", len(col.queue))
	}
}

func TestFlusherUsesSignalTimestampProviderAndCodexApprovalObserved(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	mem.RecordTerminal("local-1", observation.Terminal{Kind: observation.TerminalCompleted})
	stamp := time.Unix(1_700_000_123, 0).UTC()

	var got UpsertParams
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, p UpsertParams) (Record, error) {
			got = p
			return Record{}, nil
		},
	}
	f, _ := newTestFlusher(t, mem, store)
	f.handle(context.Background(), flushSignal{LocalTurnID: "local-1", ThreadID: "thread-1", AgentID: "agent-1", Provider: "codex", Timestamp: stamp})

	if got.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", got.Provider)
	}
	if !got.CompletedAt.Equal(stamp) {
		t.Fatalf("CompletedAt = %v, want signal timestamp %v", got.CompletedAt, stamp)
	}
	if got.ApprovalRequests != 0 || !got.ApprovalRequestsObserved {
		t.Fatalf("codex zero-approval turn must be observed=true with zero count: %+v", got)
	}
	if got.ToolCalls != 0 || got.ToolCallsObserved {
		t.Fatalf("tool call family should remain unobserved before ToolCallBegin: %+v", got)
	}
}

// TestFlusherDrainRunsOnCancel 验证 runner ctx 取消后仍会在有界 drain 内刷新队列中的信号。
func TestFlusherDrainRunsOnCancel(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	mem.RecordTerminal("t", observation.Terminal{Kind: observation.TerminalCompleted})

	done := make(chan struct{}, 1)
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, _ UpsertParams) (Record, error) {
			select {
			case done <- struct{}{}:
			default:
			}
			return Record{}, nil
		},
	}
	f, col := newTestFlusher(t, mem, store)
	col.queue <- flushSignal{LocalTurnID: "t", ThreadID: "th"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // trigger immediate drain path
	_ = f.Run(ctx)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drain did not flush queued signal")
	}
}

// TestFlusherLogsButIgnoresStoreError 验证单次 DB 写入失败只记录日志，不会终止 runner。
// 同一 turn 后续终态仍可通过 UPSERT 合并修正。
func TestFlusherLogsButIgnoresStoreError(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	mem.RecordTerminal("t", observation.Terminal{Kind: observation.TerminalFailed})
	store := &fakeInsightStore{
		upsertFn: func(context.Context, UpsertParams) (Record, error) {
			return Record{}, errors.New("db down")
		},
	}
	f, _ := newTestFlusher(t, mem, store)
	// handle 不应 panic 或暴露可观察返回值；没有 fatal 就是本断言。
	f.handle(context.Background(), flushSignal{LocalTurnID: "t", ThreadID: "th"})
}
