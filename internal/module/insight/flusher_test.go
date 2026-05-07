package insight

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	observation "github.com/anthropic-ai/super-agent-v3/internal/dto/observation"
	moduleobs "github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
)

// fakeInsightStore captures what the flusher attempts to persist.
type fakeInsightStore struct {
	upsertFn         func(context.Context, insightstore.UpsertParams) (insightstore.Insight, error)
	listRecentFn     func(context.Context, int32) ([]insightstore.Insight, error)
	listByThreadFn   func(context.Context, string, int32) ([]insightstore.Insight, error)
	listApprovalsFn  func(context.Context, string, int32) ([]insightstore.ApprovalRow, error)
	listTokenTurnsFn func(context.Context, string, int32) ([]insightstore.TokenRow, error)
	getByLocalTurnFn func(context.Context, string, string) (insightstore.Insight, error)
}

func (f *fakeInsightStore) Upsert(ctx context.Context, p insightstore.UpsertParams) (insightstore.Insight, error) {
	if f.upsertFn != nil {
		return f.upsertFn(ctx, p)
	}
	return insightstore.Insight{ID: 1, ThreadID: p.ThreadID, LocalTurnID: p.LocalTurnID, Status: p.Status}, nil
}
func (f *fakeInsightStore) GetByLocalTurn(ctx context.Context, threadID, localTurnID string) (insightstore.Insight, error) {
	if f.getByLocalTurnFn != nil {
		return f.getByLocalTurnFn(ctx, threadID, localTurnID)
	}
	return insightstore.Insight{}, nil
}
func (f *fakeInsightStore) ListByThread(ctx context.Context, threadID string, limit int32) ([]insightstore.Insight, error) {
	if f.listByThreadFn != nil {
		return f.listByThreadFn(ctx, threadID, limit)
	}
	return nil, nil
}
func (f *fakeInsightStore) ListRecent(ctx context.Context, limit int32) ([]insightstore.Insight, error) {
	if f.listRecentFn != nil {
		return f.listRecentFn(ctx, limit)
	}
	return nil, nil
}
func (f *fakeInsightStore) ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]insightstore.ApprovalRow, error) {
	if f.listApprovalsFn != nil {
		return f.listApprovalsFn(ctx, threadID, limit)
	}
	return nil, nil
}
func (f *fakeInsightStore) ListObservedTokenTurns(ctx context.Context, threadID string, limit int32) ([]insightstore.TokenRow, error) {
	if f.listTokenTurnsFn != nil {
		return f.listTokenTurnsFn(ctx, threadID, limit)
	}
	return nil, nil
}

func newTestFlusher(t *testing.T, obs observation.Contract, store insightstore.Store) (*Flusher, *collector) {
	t.Helper()
	col := newCollector(slog.Default(), 16)
	f := NewFlusher(slog.Default(), obs, store, col)
	f.drainTimeout = 100 * time.Millisecond
	f.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	return f, col
}

// TestFlusherBuildsUpsertFromObservation verifies the single-signal path
// reads every fact out of observation and lands it in the upsert params.
func TestFlusherBuildsUpsertFromObservation(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	// Seed observation with a completed turn.
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

	var got insightstore.UpsertParams
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, p insightstore.UpsertParams) (insightstore.Insight, error) {
			got = p
			return insightstore.Insight{ID: 1}, nil
		},
	}
	f, _ := newTestFlusher(t, mem, store)
	f.handle(context.Background(), flushSignal{
		LocalTurnID: "local-1",
		ThreadID:    "thread-1",
		AgentID:     "agent-1",
		Provider:    "codex",
	})

	if got.Status != insightstore.StatusCompleted {
		t.Fatalf("Status = %q, want completed", got.Status)
	}
	if got.Success == nil || !*got.Success {
		t.Fatalf("Success = %v, want pointer to true", got.Success)
	}
	if got.ToolCalls != 2 || got.ToolFailures != 1 || !got.ToolCallsObserved || !got.ToolFailuresObserved {
		t.Fatalf("tool counts wrong: calls=%d callsObserved=%t failures=%d failuresObserved=%t", got.ToolCalls, got.ToolCallsObserved, got.ToolFailures, got.ToolFailuresObserved)
	}
	if got.ApprovalRequests != 1 || !got.ApprovalRequestsObserved {
		t.Fatalf("approval snapshot wrong: %+v", got)
	}
	if got.TokenInput != 11 || got.TokenOutput != 22 || got.TokenTotal != 33 {
		t.Fatalf("token counts wrong: %+v", got)
	}
	if got.DurationMS != 3000 {
		t.Fatalf("DurationMS = %d, want 3000 (3s)", got.DurationMS)
	}
	if got.ProviderTurnID != "provider-1" {
		t.Fatalf("ProviderTurnID = %q, want provider-1 (from MapTurn)", got.ProviderTurnID)
	}
	if string(got.SkillsSelected) != `["skill-a","skill-b"]` {
		t.Fatalf("SkillsSelected = %q", got.SkillsSelected)
	}
}

// TestFlusherMapsUnknownTerminalToStatusUnknown exercises the boundary
// translation documented in P3 plan: observation.TerminalKind="" must
// land as insight.Status="unknown" in the DB.
func TestFlusherMapsUnknownTerminalToStatusUnknown(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	mem.RecordTerminal("t", observation.Terminal{Kind: observation.TerminalUnknown})

	var got insightstore.UpsertParams
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, p insightstore.UpsertParams) (insightstore.Insight, error) {
			got = p
			return insightstore.Insight{}, nil
		},
	}
	f, _ := newTestFlusher(t, mem, store)
	f.handle(context.Background(), flushSignal{LocalTurnID: "t", ThreadID: "th"})
	if got.Status != insightstore.StatusUnknown {
		t.Fatalf("Status = %q, want %q", got.Status, insightstore.StatusUnknown)
	}
}

// TestFlusherRequeuesWhenObservationEmpty covers the race where the
// collector fires a terminal signal before observation's own subscribers
// have written the corresponding Terminal.
func TestFlusherRequeuesWhenObservationEmpty(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory() // intentionally empty
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, _ insightstore.UpsertParams) (insightstore.Insight, error) {
			t.Fatal("Upsert should not run when observation has no terminal")
			return insightstore.Insight{}, nil
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

	var got insightstore.UpsertParams
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, p insightstore.UpsertParams) (insightstore.Insight, error) {
			got = p
			return insightstore.Insight{}, nil
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

// TestFlusherDrainRunsOnCancel verifies the 5s-bounded drain actually
// flushes any in-flight signals when the runner context is cancelled.
func TestFlusherDrainRunsOnCancel(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	mem.RecordTerminal("t", observation.Terminal{Kind: observation.TerminalCompleted})

	done := make(chan struct{}, 1)
	store := &fakeInsightStore{
		upsertFn: func(_ context.Context, _ insightstore.UpsertParams) (insightstore.Insight, error) {
			select {
			case done <- struct{}{}:
			default:
			}
			return insightstore.Insight{}, nil
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

// TestFlusherLogsButIgnoresStoreError proves a DB hiccup does not tear
// the runner down — the next terminal for the same turn will merge via
// UPSERT anyway.
func TestFlusherLogsButIgnoresStoreError(t *testing.T) {
	t.Parallel()

	mem := moduleobs.NewMemory()
	mem.RecordTerminal("t", observation.Terminal{Kind: observation.TerminalFailed})
	store := &fakeInsightStore{
		upsertFn: func(context.Context, insightstore.UpsertParams) (insightstore.Insight, error) {
			return insightstore.Insight{}, errors.New("db down")
		},
	}
	f, _ := newTestFlusher(t, mem, store)
	// handle must not panic or return anything observable; absence of a
	// fatal here is the assertion.
	f.handle(context.Background(), flushSignal{LocalTurnID: "t", ThreadID: "th"})
}
