package cron

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

// TestCronScheduler_Tick_LogsPlaceholder —— F5.1 Tick 仅 log，不动 DB。
// Tick is a placeholder; real next_run_at scan lands in F5.2.
func TestCronScheduler_Tick_LogsPlaceholder(t *testing.T) {
	ticker := &stubTicker{}
	s, err := NewCronScheduler(Config{Spec: "@hourly", Logger: discardLogger(), Ticker: ticker})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	// 直接调 Tick；F5.1 仅委托给 Ticker.Tick 并 log。
	// Directly invoke Tick; F5.1 only delegates to Ticker.Tick and logs.
	s.Tick()
	if ticker.calls() != 1 {
		t.Fatalf("ticker calls = %d, want 1", ticker.calls())
	}
}

// TestCronScheduler_StartStop_GracefulShutdown —— Start + Stop 不泄露 goroutine。
// Start + Stop must not leak goroutines.
func TestCronScheduler_StartStop_GracefulShutdown(t *testing.T) {
	before := runtime.NumGoroutine()
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
	// 等待 robfig cron 内部 goroutine 退出（Stop 返回 ctx 完成后再等少量）
	// Wait for robfig cron goroutines to drain after Stop returns.
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	// 容忍 ±2 goroutine 的运行时抖动 / tolerate ±2 jitter
	if after > before+2 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
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
