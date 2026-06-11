package wails

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	bridgeEventName = "bridge-event"
	agentEventName  = "agent-event"
	quitOverlayName = "app-will-quit"
	quitErrorName   = "app-quit-error"
	quitGraceDelay  = 320 * time.Millisecond
	// 误判防护：armShutdownTimer 使用 shutdownHardDeadline 作为桌面关闭的硬截止守卫。
	shutdownHardDeadline = 15 * time.Second
)

type lifecycleTimer interface {
	Stop() bool
}

type ActiveAgentCounter interface {
	ActiveAgentCount(context.Context) (int, error)
}

type ActiveAgentCounterFunc func(context.Context) (int, error)

func (f ActiveAgentCounterFunc) ActiveAgentCount(ctx context.Context) (int, error) {
	return f(ctx)
}

type WailsLifecycle struct {
	logger    *slog.Logger
	counter   ActiveAgentCounter
	afterFunc func(time.Duration, func()) lifecycleTimer

	mu             sync.RWMutex
	quitFunc       func()
	shutdownerFunc func()
	emitFunc       func(string, any)
	shutdownTimer  lifecycleTimer

	quitIntercepted atomic.Bool
	quitAllowed     atomic.Bool
	shutdownStarted atomic.Bool
	frontendReady   atomic.Bool
	pendingQuit     atomic.Bool
}

func NewWailsLifecycle(counter ActiveAgentCounter, slogLogger *slog.Logger) *WailsLifecycle {
	if slogLogger == nil {
		slogLogger = pkglogger.Get()
	}
	return &WailsLifecycle{
		logger:    slogLogger,
		counter:   counter,
		afterFunc: func(d time.Duration, fn func()) lifecycleTimer { return time.AfterFunc(d, fn) },
	}
}

func (l *WailsLifecycle) SetQuitFunc(fn func()) {
	l.mu.Lock()
	l.quitFunc = fn
	l.mu.Unlock()

	l.flushPendingQuit()
}

func (l *WailsLifecycle) SetShutdownerFunc(fn func()) {
	l.mu.Lock()
	l.shutdownerFunc = fn
	l.mu.Unlock()

	if l.shutdownStarted.Load() {
		l.requestBackendShutdown()
	}
}

func (l *WailsLifecycle) SetEventEmitter(fn func(string, any)) {
	l.mu.Lock()
	l.emitFunc = fn
	l.mu.Unlock()
}

func (l *WailsLifecycle) MarkFrontendReady() {
	l.frontendReady.Store(true)
	l.flushPendingQuit()
}

func (l *WailsLifecycle) ShouldQuit() bool {
	if l.quitAllowed.Load() {
		return true
	}
	if !l.quitIntercepted.CompareAndSwap(false, true) {
		return false
	}

	activeCount, err := l.activeAgentCount()
	if err != nil {
		l.emitQuitError(err)
		l.requestBackendShutdown()
		return false
	}
	if activeCount > 0 {
		l.emitQuitOverlay(activeCount)
		l.afterFunc(quitGraceDelay, l.requestBackendShutdown)
		return false
	}

	l.requestBackendShutdown()
	return false
}

func (l *WailsLifecycle) OnShutdown() {
	l.requestBackendShutdown()
}

func (l *WailsLifecycle) NotifyBackendStopped() {
	l.allowQuitAfterBackendShutdown()
}

func (l *WailsLifecycle) RequestQuit() {
	if l == nil {
		return
	}
	l.quitAllowed.Store(true)
	if !l.frontendReady.Load() {
		l.pendingQuit.Store(true)
		return
	}
	l.invokeQuit()
}

func (l *WailsLifecycle) NotifyBackendFailed() {
	l.allowQuitAfterBackendShutdown()
}

func (l *WailsLifecycle) allowQuitAfterBackendShutdown() {
	l.stopShutdownTimer()
	l.quitAllowed.Store(true)
	if !l.frontendReady.Load() {
		l.pendingQuit.Store(true)
		return
	}
	l.invokeQuit()
}

func (l *WailsLifecycle) EmitEvent(name string, payload any) {
	if l == nil {
		return
	}
	emit := l.loadEmitter()
	if emit != nil {
		emit(name, payload)
	}
}

func (l *WailsLifecycle) activeAgentCount() (int, error) {
	if l == nil {
		return 0, errors.New("wails lifecycle is not configured")
	}
	if l.counter == nil {
		return 0, errors.New("active agent counter is not configured")
	}

	count, err := l.counter.ActiveAgentCount(context.Background())
	if err != nil {
		if l.logger != nil {
			l.logger.Warn("wails: failed to count active agents", "error", err)
		}
		return 0, err
	}
	return count, nil
}

func (l *WailsLifecycle) emitQuitOverlay(activeCount int) {
	l.EmitEvent(quitOverlayName, map[string]any{
		"active_agents": activeCount,
		"delay_ms":      quitGraceDelay.Milliseconds(),
		"at":            time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (l *WailsLifecycle) emitQuitError(err error) {
	if err == nil {
		return
	}
	l.EmitEvent(quitErrorName, map[string]any{
		"message": err.Error(),
		"at":      time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (l *WailsLifecycle) requestBackendShutdown() {
	if l == nil || !l.shutdownStarted.CompareAndSwap(false, true) {
		return
	}
	l.armShutdownTimer()
	shutdown := l.loadShutdowner()
	if shutdown == nil {
		l.NotifyBackendFailed()
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("wails: recovered shutdown callback panic", "panic", rec)
			}
		}()
		shutdown()
	}()
}

func (l *WailsLifecycle) flushPendingQuit() {
	if l == nil || !l.frontendReady.Load() {
		return
	}
	if l.pendingQuit.CompareAndSwap(true, false) {
		l.invokeQuit()
	}
}

func (l *WailsLifecycle) invokeQuit() {
	l.stopShutdownTimer()
	quit := l.loadQuit()
	if quit == nil {
		l.pendingQuit.Store(true)
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				pkglogger.Error("wails: recovered quit callback panic", "panic", rec)
			}
		}()
		quit()
	}()
}

func (l *WailsLifecycle) loadQuit() func() {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.quitFunc
}

func (l *WailsLifecycle) loadShutdowner() func() {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.shutdownerFunc
}

func (l *WailsLifecycle) loadEmitter() func(string, any) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.emitFunc
}

func (l *WailsLifecycle) armShutdownTimer() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shutdownTimer != nil {
		l.shutdownTimer.Stop()
	}
	l.shutdownTimer = l.afterFunc(shutdownHardDeadline, func() {
		l.logger.Warn("wails: shutdown hard deadline exceeded", "deadline", shutdownHardDeadline.String())
		l.NotifyBackendFailed()
	})
}

func (l *WailsLifecycle) stopShutdownTimer() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shutdownTimer == nil {
		return
	}
	l.shutdownTimer.Stop()
	l.shutdownTimer = nil
}

func ShutdownHardDeadline() time.Duration {
	return shutdownHardDeadline
}
