package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/wakeupreclaim"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

// reclaimStubStore 是 wakeup lease 回收测试用的 taskdag.Store 假实现。
// 只覆盖 ReclaimStaleDispatchingWakeups，未覆盖方法通过 nil 嵌入暴露误调用。
type reclaimStubStore struct {
	taskdag.Store // 嵌入 nil，让未覆盖方法触发 panic（暴露遗漏）

	calls       int
	rowsReplies []int64 // FIFO；空时默认返回 0
	err         error
	recoveryErr error
}

func (s *reclaimStubStore) ReclaimStaleDispatchingWakeups(_ context.Context) (int64, error) {
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	if len(s.rowsReplies) == 0 {
		return 0, nil
	}
	r := s.rowsReplies[0]
	s.rowsReplies = s.rowsReplies[1:]
	return r, nil
}

func (s *reclaimStubStore) MarkDispatchIncompleteNodesWithoutActiveWakeup(context.Context) ([]taskdag.Node, error) {
	return nil, s.recoveryErr
}

type reclaimStoreWithoutRecovery struct {
	taskdag.Store
}

func (s reclaimStoreWithoutRecovery) ReclaimStaleDispatchingWakeups(context.Context) (int64, error) {
	return 0, nil
}

func TestNewWakeupReclaimerRejectsNilStore(t *testing.T) {
	if _, err := wakeupreclaim.NewWakeupReclaimer(nil, nil, wakeupreclaim.WakeupReclaimerConfig{}); err == nil {
		t.Fatalf("err = nil, want error for nil store")
	}
}

func TestNewWakeupReclaimerRejectsStoreWithoutDispatchRecovery(t *testing.T) {
	if _, err := wakeupreclaim.NewWakeupReclaimer(reclaimStoreWithoutRecovery{}, nil, wakeupreclaim.WakeupReclaimerConfig{}); err == nil {
		t.Fatalf("err = nil, want error for store without dispatch incomplete recovery")
	}
}

func TestWakeupReclaimerReclaimOnceNoStaleRowsReturnsZero(t *testing.T) {
	store := &reclaimStubStore{}
	r, err := wakeupreclaim.NewWakeupReclaimer(store, nil, wakeupreclaim.WakeupReclaimerConfig{})
	if err != nil {
		t.Fatalf("NewWakeupReclaimer err = %v", err)
	}
	rows, err := r.ReclaimOnce(context.Background())
	if err != nil {
		t.Fatalf("ReclaimOnce err = %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0 on empty reclaim set", rows)
	}
	if store.calls != 1 {
		t.Fatalf("ReclaimStaleDispatchingWakeups called %d times, want 1", store.calls)
	}
}

func TestWakeupReclaimerReclaimOnceStaleRowsReturnsCount(t *testing.T) {
	store := &reclaimStubStore{rowsReplies: []int64{3}}
	r, _ := wakeupreclaim.NewWakeupReclaimer(store, nil, wakeupreclaim.WakeupReclaimerConfig{})
	rows, err := r.ReclaimOnce(context.Background())
	if err != nil {
		t.Fatalf("ReclaimOnce err = %v", err)
	}
	if rows != 3 {
		t.Fatalf("rows = %d, want 3", rows)
	}
}

func TestWakeupReclaimerReclaimOncePropagatesError(t *testing.T) {
	store := &reclaimStubStore{err: errors.New("db unreachable")}
	r, _ := wakeupreclaim.NewWakeupReclaimer(store, nil, wakeupreclaim.WakeupReclaimerConfig{})
	rows, err := r.ReclaimOnce(context.Background())
	if err == nil {
		t.Fatalf("err = nil, want store err propagation")
	}
	if rows != 0 {
		t.Fatalf("rows = %d, want 0 on error", rows)
	}
}

func TestWakeupReclaimerReclaimOnceHandlesNilContextSafely(t *testing.T) {
	store := &reclaimStubStore{}
	r, _ := wakeupreclaim.NewWakeupReclaimer(store, nil, wakeupreclaim.WakeupReclaimerConfig{})
	//lint:ignore SA1012 本测试覆盖 nil context 会被显式替换为 background 的兼容路径。
	if _, err := r.ReclaimOnce(nil); err != nil { //nolint:staticcheck // 测试 nil context 会被显式替换为 background。
		t.Fatalf("nil ctx fallback err = %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("expected one call after nil ctx fallback, got %d", store.calls)
	}
}

func TestWakeupReclaimerRunStopsOnContextCancel(t *testing.T) {
	store := &reclaimStubStore{}
	r, _ := wakeupreclaim.NewWakeupReclaimer(store, nil, wakeupreclaim.WakeupReclaimerConfig{TickInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Run did not stop within 1s after ctx.cancel")
	}
	if store.calls < 2 {
		t.Fatalf("Run did not tick at least twice before stop, got %d ticks", store.calls)
	}
}

func TestWakeupReclaimerRunSurvivesStoreError(t *testing.T) {
	store := &reclaimStubStore{err: errors.New("transient db blip")}
	r, _ := wakeupreclaim.NewWakeupReclaimer(store, nil, wakeupreclaim.WakeupReclaimerConfig{TickInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	time.Sleep(35 * time.Millisecond)
	cancel()
	<-done
	if store.calls < 2 {
		t.Fatalf("Run gave up too early after store error: %d calls", store.calls)
	}
}

func TestWakeupReclaimerConfigOrDefaultsKeepsExplicit(t *testing.T) {
	cfg := wakeupreclaim.WakeupReclaimerConfig{TickInterval: 7 * time.Second}.ConfigOrDefaults()
	if cfg.TickInterval != 7*time.Second {
		t.Fatalf("explicit interval overridden: %v", cfg.TickInterval)
	}
}

func TestWakeupReclaimerConfigOrDefaultsFillsZero(t *testing.T) {
	cfg := wakeupreclaim.WakeupReclaimerConfig{}.ConfigOrDefaults()
	if cfg.TickInterval != wakeupreclaim.DefaultWakeupReclaimInterval {
		t.Fatalf("default interval = %v, want %v", cfg.TickInterval, wakeupreclaim.DefaultWakeupReclaimInterval)
	}
}
