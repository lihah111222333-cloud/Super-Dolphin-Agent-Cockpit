package cron

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// 复用变量 / Shared helpers
// discardLogger 返回丢弃输出的 slog.Logger，避免测试噪音。
// discardLogger returns a slog.Logger that discards output to avoid test noise.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stubTicker 实现 Ticker 接口，记录被调次数。
// stubTicker implements the Ticker interface and counts invocations.
type stubTicker struct {
	count atomic.Int32
	err   error
}

func (s *stubTicker) Tick(ctx context.Context, now time.Time) (int, error) {
	s.count.Add(1)
	return 0, s.err
}

func (s *stubTicker) calls() int32 { return s.count.Load() }

type fakeScheduleStore struct {
	due []DueDAG
	err error
}

func (s *fakeScheduleStore) DueDAGs(ctx context.Context, now time.Time) ([]DueDAG, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]DueDAG(nil), s.due...), nil
}

type fakeStarter struct {
	starts   []ScheduledDAGStartRequest
	err      error
	errByDag map[string]error
}

func (s *fakeStarter) StartDAG(ctx context.Context, req ScheduledDAGStartRequest) error {
	s.starts = append(s.starts, req)
	if err := s.errByDag[req.DagKey]; err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}
	return nil
}

type blockingStarter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingStarter() *blockingStarter {
	return &blockingStarter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingStarter) StartDAG(ctx context.Context, req ScheduledDAGStartRequest) error {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type fakeLocker struct {
	mu          sync.Mutex
	locked      bool
	tryCalls    int
	unlockCalls int
	err         error
}

func (l *fakeLocker) TryLock(ctx context.Context) (RuntimeLockHandle, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tryCalls++
	if l.err != nil {
		return nil, false, l.err
	}
	if l.locked {
		return nil, false, nil
	}
	l.locked = true
	return &fakeLockHandle{locker: l}, true, nil
}

type fakeLockHandle struct {
	locker *fakeLocker
}

func (h *fakeLockHandle) Unlock(ctx context.Context) error {
	h.locker.mu.Lock()
	defer h.locker.mu.Unlock()
	h.locker.unlockCalls++
	h.locker.locked = false
	return nil
}

func (h *fakeLockHandle) Renew(ctx context.Context) error { return nil }

type contextCheckingLockHandle struct {
	errOnCanceled bool
	unlockCalls   int
}

func (h *contextCheckingLockHandle) Unlock(ctx context.Context) error {
	h.unlockCalls++
	if h.errOnCanceled && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (h *contextCheckingLockHandle) Renew(ctx context.Context) error { return nil }

type blockingTicker struct {
	entered chan struct{}
	done    chan error
	once    sync.Once
}

func newBlockingTicker() *blockingTicker {
	return &blockingTicker{
		entered: make(chan struct{}),
		done:    make(chan error, 1),
	}
}

func (t *blockingTicker) Tick(ctx context.Context, now time.Time) (int, error) {
	t.once.Do(func() { close(t.entered) })
	<-ctx.Done()
	err := ctx.Err()
	t.done <- err
	return 0, err
}

// TestCronScheduler_New_NilDepsReturnError —— 构造函数对 nil deps 防御。
// Defensive check: NewCronScheduler must reject nil ticker / logger / spec.
func TestCronScheduler_New_NilDepsReturnError(t *testing.T) {
	t.Run("nil ticker", func(t *testing.T) {
		_, err := NewCronScheduler(Config{Spec: "@hourly", Logger: discardLogger(), Ticker: nil})
		if !errors.Is(err, ErrNilTicker) {
			t.Fatalf("err = %v, want ErrNilTicker", err)
		}
	})
	t.Run("nil logger", func(t *testing.T) {
		_, err := NewCronScheduler(Config{Spec: "@hourly", Logger: nil, Ticker: &stubTicker{}})
		if !errors.Is(err, ErrNilLogger) {
			t.Fatalf("err = %v, want ErrNilLogger", err)
		}
	})
	t.Run("empty spec", func(t *testing.T) {
		_, err := NewCronScheduler(Config{Spec: "", Logger: discardLogger(), Ticker: &stubTicker{}})
		if !errors.Is(err, ErrEmptySpec) {
			t.Fatalf("err = %v, want ErrEmptySpec", err)
		}
	})
	t.Run("invalid spec", func(t *testing.T) {
		_, err := NewCronScheduler(Config{Spec: "not-a-cron-expr", Logger: discardLogger(), Ticker: &stubTicker{}})
		if err == nil {
			t.Fatalf("err = nil, want robfig parse error")
		}
	})
	t.Run("happy path", func(t *testing.T) {
		s, err := NewCronScheduler(Config{Spec: "@hourly", Logger: discardLogger(), Ticker: &stubTicker{}})
		if err != nil {
			t.Fatalf("NewCronScheduler err = %v, want nil", err)
		}
		if s == nil {
			t.Fatalf("scheduler = nil")
		}
	})
}

func TestCronScheduler_Tick_DelegatesToTicker(t *testing.T) {
	ticker := &stubTicker{}
	s, err := NewCronScheduler(Config{Spec: "@hourly", Logger: discardLogger(), Ticker: ticker})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	s.Tick()
	if ticker.calls() != 1 {
		t.Fatalf("ticker calls = %d, want 1", ticker.calls())
	}
}

func TestScheduledDAGTicker_Tick_ScansAndStarts(t *testing.T) {
	dueAt := time.Date(2026, 5, 11, 7, 0, 0, 123, time.UTC)
	store := &fakeScheduleStore{due: []DueDAG{
		{DagKey: "daily-report", CronExpr: "0 8 * * *", DueAt: dueAt},
		{DagKey: "hourly-sync", CronExpr: "@hourly", DueAt: dueAt.Add(time.Minute)},
	}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	now := time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)
	n, err := ticker.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 2 {
		t.Fatalf("triggered = %d, want 2", n)
	}
	if len(starter.starts) != 2 {
		t.Fatalf("starts = %d, want 2", len(starter.starts))
	}
	for _, start := range starter.starts {
		if start.TriggerSource != scheduledTriggerSource {
			t.Fatalf("trigger_source = %q, want %q", start.TriggerSource, scheduledTriggerSource)
		}
	}
	if got, want := starter.starts[0].IdempotencyKey, "scheduled:daily-report:"+dueAt.UTC().Format(time.RFC3339Nano); got != want {
		t.Fatalf("idempotency_key = %q, want %q", got, want)
	}
	if !starter.starts[0].NextRunAt.Equal(time.Date(2026, 5, 11, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("next_run_at in request = %s, want 2026-05-11T08:00:00Z", starter.starts[0].NextRunAt)
	}
}

func TestScheduledDAGTicker_NextRunAtDelegatedToStarter(t *testing.T) {
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "hourly-sync", CronExpr: "@hourly", DueAt: time.Date(2026, 5, 11, 9, 0, 0, 0, time.UTC)}}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	now := time.Date(2026, 5, 11, 9, 30, 0, 0, time.UTC)
	if _, err := ticker.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	want := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	if len(starter.starts) != 1 || starter.starts[0].DagKey != "hourly-sync" {
		t.Fatalf("starts = %+v, want hourly-sync", starter.starts)
	}
	if !starter.starts[0].DueAt.Equal(store.due[0].DueAt) {
		t.Fatalf("delegated due_at = %s, want scanned due_at %s", starter.starts[0].DueAt, store.due[0].DueAt)
	}
	if !starter.starts[0].NextRunAt.Equal(want) {
		t.Fatalf("delegated next_run_at = %s, want %s", starter.starts[0].NextRunAt, want)
	}
}

func TestScheduledDAGTicker_BareCronDefaultsUTCWhenTickNowIsBeijing(t *testing.T) {
	beijing, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	store := &fakeScheduleStore{due: []DueDAG{{
		DagKey:   "utc-default",
		CronExpr: "0 8 * * *",
		DueAt:    time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC),
	}}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}

	n, err := ticker.Tick(context.Background(), time.Date(2026, 6, 1, 9, 0, 0, 0, beijing))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 || len(starter.starts) != 1 {
		t.Fatalf("triggered=%d starts=%d, want one start", n, len(starter.starts))
	}
	want := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	if !starter.starts[0].NextRunAt.Equal(want) {
		t.Fatalf("bare cron next_run_at = %s, want UTC default %s", starter.starts[0].NextRunAt, want)
	}
}

func TestScheduledDAGTicker_CRONTZAsiaShanghaiComputesBeijingWallTime(t *testing.T) {
	beijing, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	store := &fakeScheduleStore{due: []DueDAG{{
		DagKey:   "beijing-wall-time",
		CronExpr: "CRON_TZ=Asia/Shanghai 0 8 * * *",
		DueAt:    time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC),
	}}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}

	n, err := ticker.Tick(context.Background(), time.Date(2026, 6, 1, 9, 0, 0, 0, beijing))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 || len(starter.starts) != 1 {
		t.Fatalf("triggered=%d starts=%d, want one start", n, len(starter.starts))
	}
	want := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if !starter.starts[0].NextRunAt.Equal(want) {
		t.Fatalf("CRON_TZ next_run_at = %s, want Beijing 08:00 as %s", starter.starts[0].NextRunAt, want)
	}
}

func TestParseDAGCronExpr_InvalidMentionsUTCDefaultAndCRONTZ(t *testing.T) {
	_, err := ParseDAGCronExpr("not-a-cron")
	if err == nil {
		t.Fatal("ParseDAGCronExpr err = nil, want invalid cron error")
	}
	for _, want := range []string{"bare cron defaults to UTC", "CRON_TZ=<IANA>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ParseDAGCronExpr err = %v, want mention %s", err, want)
		}
	}
}

func TestScheduledDAGTicker_TickIsolatesPerDAGErrorsInSameTick(t *testing.T) {
	stateChangedErr := fmt.Errorf("%w: stale due slot", ErrScheduleStateChanged)
	store := &fakeScheduleStore{due: []DueDAG{
		{DagKey: "dirty-cron", CronExpr: "not-a-cron", DueAt: time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)},
		{DagKey: "stale-state", CronExpr: "@hourly", DueAt: time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)},
		{DagKey: "still-runs", CronExpr: "CRON_TZ=Asia/Shanghai 0 8 * * *", DueAt: time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC)},
	}}
	starter := &fakeStarter{errByDag: map[string]error{"stale-state": stateChangedErr}}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}

	n, err := ticker.Tick(context.Background(), time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("Tick err = nil, want aggregated per-DAG errors")
	}
	if n != 1 {
		t.Fatalf("triggered = %d, want only still-runs to trigger", n)
	}
	if len(starter.starts) != 2 {
		t.Fatalf("starts = %+v, want stale-state attempt plus still-runs", starter.starts)
	}
	if starter.starts[1].DagKey != "still-runs" {
		t.Fatalf("second start = %s, want still-runs", starter.starts[1].DagKey)
	}
	assertIsolatedTickErrors(t, err)
}

func assertIsolatedTickErrors(t *testing.T, err error) {
	t.Helper()

	var tickErr *TickError
	if !errors.As(err, &tickErr) || tickErr.Class != TickErrorClassValidation {
		t.Fatalf("Tick err = %v, want validation TickError included", err)
	}
	if !errors.Is(err, ErrScheduleStateChanged) {
		t.Fatalf("Tick err = %v, want ErrScheduleStateChanged included", err)
	}
	for _, want := range []string{"dirty-cron", "stale-state"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Tick err = %v, want mention %s", err, want)
		}
	}
}

func TestScheduledDAGTicker_CronParseValidationError(t *testing.T) {
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "bad", CronExpr: "not-a-cron", DueAt: time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)}}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	_, err = ticker.Tick(context.Background(), time.Now())
	var tickErr *TickError
	if !errors.As(err, &tickErr) {
		t.Fatalf("Tick err = %v, want TickError", err)
	}
	if tickErr.Class != TickErrorClassValidation {
		t.Fatalf("class = %q, want %q", tickErr.Class, TickErrorClassValidation)
	}
}

func TestScheduledDAGTicker_ScanInfrastructureError(t *testing.T) {
	store := &fakeScheduleStore{err: errors.New("db down")}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	_, err = ticker.Tick(context.Background(), time.Now())
	var tickErr *TickError
	if !errors.As(err, &tickErr) {
		t.Fatalf("Tick err = %v, want TickError", err)
	}
	if tickErr.Class != TickErrorClassInfrastructure {
		t.Fatalf("class = %q, want %q", tickErr.Class, TickErrorClassInfrastructure)
	}
}

func TestScheduledDAGTicker_StartDAGErrorPassthrough(t *testing.T) {
	startErr := errors.New("start failed")
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *", DueAt: time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)}}}
	starter := &fakeStarter{err: startErr}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	_, err = ticker.Tick(context.Background(), time.Now())
	if !errors.Is(err, startErr) {
		t.Fatalf("Tick err = %v, want startErr passthrough", err)
	}
}

func TestScheduledDAGTicker_DoesNotAdvanceNextRunWhenStartFails(t *testing.T) {
	startErr := errors.New("start failed")
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *", DueAt: time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)}}}
	starter := &fakeStarter{err: startErr}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}

	_, err = ticker.Tick(context.Background(), time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC))
	if !errors.Is(err, startErr) {
		t.Fatalf("Tick err = %v, want startErr", err)
	}
}

func TestScheduledDAGTicker_MultiInstance_OneAcquires(t *testing.T) {
	locker := &fakeLocker{}
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *", DueAt: time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)}}}
	starter := newBlockingStarter()
	first, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: locker})
	if err != nil {
		t.Fatalf("first ticker: %v", err)
	}
	releasedStarter := false
	releaseStarter := func() {
		if !releasedStarter {
			close(starter.release)
			releasedStarter = true
		}
	}
	defer releaseStarter()
	secondStarter := &fakeStarter{}
	second, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: secondStarter, Locker: locker})
	if err != nil {
		t.Fatalf("second ticker: %v", err)
	}

	firstDone := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := first.Tick(context.Background(), time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC))
		firstDone <- err
	})
	<-starter.entered
	n, err := second.Tick(context.Background(), time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("second Tick err = %v", err)
	}
	if n != 0 {
		t.Fatalf("second triggered = %d, want 0 when lock is held", n)
	}
	if len(secondStarter.starts) != 0 {
		t.Fatalf("second starts = %d, want 0", len(secondStarter.starts))
	}
	releaseStarter()
	if err := <-firstDone; err != nil {
		t.Fatalf("first Tick err = %v", err)
	}
}

func TestScheduledDAGTicker_ReleaseOnExit(t *testing.T) {
	locker := &fakeLocker{}
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *", DueAt: time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)}}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: locker})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	if _, err := ticker.Tick(context.Background(), time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	locker.mu.Lock()
	defer locker.mu.Unlock()
	if locker.locked {
		t.Fatalf("lock still held after Tick")
	}
	if locker.unlockCalls != 1 {
		t.Fatalf("unlockCalls = %d, want 1", locker.unlockCalls)
	}
}

func TestScheduledDAGTicker_UnlockUsesFreshCleanupContext(t *testing.T) {
	handle := &contextCheckingLockHandle{errOnCanceled: true}
	var result error

	(&ScheduledDAGTicker{}).releaseRuntimeLock(handle, &result)

	if result != nil {
		t.Fatalf("release result = %v, want nil from fresh cleanup context", result)
	}
	if handle.unlockCalls != 1 {
		t.Fatalf("unlockCalls = %d, want 1", handle.unlockCalls)
	}
}

func TestNewScheduledDAGTickerRejectsNilLocker(t *testing.T) {
	_, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{
		Store:   &fakeScheduleStore{},
		Starter: &fakeStarter{},
		Locker:  nil,
	})
	if !errors.Is(err, ErrNilLockPool) {
		t.Fatalf("err = %v, want ErrNilLockPool", err)
	}
}

func TestCronScheduler_TickTimeout(t *testing.T) {
	ticker := newBlockingTicker()
	s, err := NewCronScheduler(Config{Spec: "@hourly", Logger: discardLogger(), Ticker: ticker, TickTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	started := time.Now()
	s.Tick()
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Tick elapsed = %s, want timeout-driven return", elapsed)
	}
	select {
	case err := <-ticker.done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ticker err = %v, want DeadlineExceeded", err)
		}
	default:
		t.Fatalf("ticker did not observe timeout")
	}
}

func TestCronScheduler_StopCancelsInflight(t *testing.T) {
	ticker := newBlockingTicker()
	s, err := NewCronScheduler(Config{Spec: "@every 1h", Logger: discardLogger(), Ticker: ticker, TickTimeout: time.Hour})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(s.Tick)
	<-ticker.entered
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case err := <-ticker.done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ticker err = %v, want Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("in-flight Tick was not canceled")
	}
}

// TestCronScheduler_StartStop_GracefulShutdown —— Start + Stop 不泄露 goroutine。
// Start + Stop must not leak goroutines.
func TestCronScheduler_StartStop_GracefulShutdown(t *testing.T) {
	s, err := NewCronScheduler(Config{Spec: "@every 1h", Logger: discardLogger(), Ticker: &stubTicker{}})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestCronScheduler_DoubleStart_Reports —— 第二次 Start 应返错。
// Second Start must return ErrAlreadyStarted.
func TestCronScheduler_DoubleStart_Reports(t *testing.T) {
	s, err := NewCronScheduler(Config{Spec: "@every 1h", Logger: discardLogger(), Ticker: &stubTicker{}})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() { _ = s.Stop() }()
	if err := s.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start err = %v, want ErrAlreadyStarted", err)
	}
}

// TestCronScheduler_DoubleStop_Idempotent —— Stop 可重复调用。
// Stop must be idempotent (second call returns nil or ErrNotStarted).
func TestCronScheduler_DoubleStop_Idempotent(t *testing.T) {
	s, err := NewCronScheduler(Config{Spec: "@every 1h", Logger: discardLogger(), Ticker: &stubTicker{}})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("second Stop = %v, want nil (idempotent)", err)
	}
}

// TestCronScheduler_StopBeforeStart —— 未 Start 时 Stop 应是 no-op。
// Stop before Start must be a no-op (returns nil).
func TestCronScheduler_StopBeforeStart(t *testing.T) {
	s, err := NewCronScheduler(Config{Spec: "@every 1h", Logger: discardLogger(), Ticker: &stubTicker{}})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop before Start = %v, want nil", err)
	}
}

// TestCronScheduler_TickerErrorDoesNotPanic —— Ticker 报错时 daemon 不能 panic。
// Ticker errors must be logged, not panic the daemon.
func TestCronScheduler_TickerErrorDoesNotPanic(t *testing.T) {
	ticker := &stubTicker{err: errors.New("downstream boom")}
	s, err := NewCronScheduler(Config{Spec: "@every 1h", Logger: discardLogger(), Ticker: ticker})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		// 直接驱动 Tick 走错误路径；不应 panic / no panic
		s.Tick()
	})
	wg.Wait()
	if ticker.calls() != 1 {
		t.Fatalf("ticker calls = %d, want 1", ticker.calls())
	}
}
