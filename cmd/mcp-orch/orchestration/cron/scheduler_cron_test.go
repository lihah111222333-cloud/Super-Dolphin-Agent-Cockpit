package cron

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	count int32
	err   error
}

func (s *stubTicker) Tick(ctx context.Context, now time.Time) (int, error) {
	atomic.AddInt32(&s.count, 1)
	return 0, s.err
}

func (s *stubTicker) calls() int32 { return atomic.LoadInt32(&s.count) }

type fakeScheduleStore struct {
	due     []DueDAG
	err     error
	updates []scheduledUpdate
}

type scheduledUpdate struct {
	dagKey    string
	nextRunAt time.Time
}

func (s *fakeScheduleStore) DueDAGs(ctx context.Context, now time.Time) ([]DueDAG, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]DueDAG(nil), s.due...), nil
}

func (s *fakeScheduleStore) UpdateNextRun(ctx context.Context, dagKey string, nextRunAt time.Time) error {
	s.updates = append(s.updates, scheduledUpdate{dagKey: dagKey, nextRunAt: nextRunAt})
	return nil
}

type fakeStarter struct {
	starts []startedDAG
	err    error
}

type startedDAG struct {
	dagKey        string
	triggerSource string
}

func (s *fakeStarter) StartDAG(ctx context.Context, dagKey string, triggerSource string) error {
	if s.err != nil {
		return s.err
	}
	s.starts = append(s.starts, startedDAG{dagKey: dagKey, triggerSource: triggerSource})
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

func (s *blockingStarter) StartDAG(ctx context.Context, dagKey string, triggerSource string) error {
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

func (l *fakeLocker) TryLock(ctx context.Context) (AdvisoryLockHandle, bool, error) {
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
	store := &fakeScheduleStore{due: []DueDAG{
		{DagKey: "daily-report", CronExpr: "0 8 * * *"},
		{DagKey: "hourly-sync", CronExpr: "@hourly"},
	}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	n, err := ticker.Tick(context.Background(), time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC))
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
		if start.triggerSource != scheduledTriggerSource {
			t.Fatalf("trigger_source = %q, want %q", start.triggerSource, scheduledTriggerSource)
		}
	}
}

func TestScheduledDAGTicker_NextRunAtUpdated(t *testing.T) {
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "hourly-sync", CronExpr: "@hourly"}}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	now := time.Date(2026, 5, 11, 9, 30, 0, 0, time.UTC)
	if _, err := ticker.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(store.updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(store.updates))
	}
	want := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	if !store.updates[0].nextRunAt.Equal(want) {
		t.Fatalf("next_run_at = %s, want %s", store.updates[0].nextRunAt, want)
	}
	if len(starter.starts) != 1 || starter.starts[0].dagKey != "hourly-sync" {
		t.Fatalf("starts = %+v, want hourly-sync", starter.starts)
	}
}

func TestScheduledDAGTicker_CronParseValidationError(t *testing.T) {
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "bad", CronExpr: "not-a-cron"}}}
	starter := &fakeStarter{}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter})
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
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter})
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
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *"}}}
	starter := &fakeStarter{err: startErr}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}
	_, err = ticker.Tick(context.Background(), time.Now())
	if !errors.Is(err, startErr) {
		t.Fatalf("Tick err = %v, want startErr passthrough", err)
	}
}

func TestScheduledDAGTicker_MultiInstance_OneAcquires(t *testing.T) {
	locker := &fakeLocker{}
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *"}}}
	starter := newBlockingStarter()
	first, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: locker})
	if err != nil {
		t.Fatalf("first ticker: %v", err)
	}
	secondStarter := &fakeStarter{}
	second, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: secondStarter, Locker: locker})
	if err != nil {
		t.Fatalf("second ticker: %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := first.Tick(context.Background(), time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC))
		firstDone <- err
	}()
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
	close(starter.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Tick err = %v", err)
	}
}

func TestScheduledDAGTicker_ReleaseOnExit(t *testing.T) {
	locker := &fakeLocker{}
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *"}}}
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
	go s.Tick()
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
	// 给 cron loop 一次调度机会 / give cron loop a scheduling chance
	time.Sleep(20 * time.Millisecond)
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 直接驱动 Tick 走错误路径；不应 panic / no panic
		s.Tick()
	}()
	wg.Wait()
	if ticker.calls() != 1 {
		t.Fatalf("ticker calls = %d, want 1", ticker.calls())
	}
}
