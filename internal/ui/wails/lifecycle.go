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
	// Wails 生命周期事件名。
	bridgeEventName = "bridge-event"
	agentEventName  = "agent-event"
	quitOverlayName = "app-will-quit"
	quitErrorName   = "app-quit-error"
	quitGraceDelay  = 320 * time.Millisecond
	// 误判防护：armShutdownTimer 使用 shutdownHardDeadline 作为桌面关闭的硬截止守卫。
	shutdownHardDeadline = 15 * time.Second
)

// lifecycleTimer 抽象 time.Timer 的 Stop 方法，便于测试替换 afterFunc。
type lifecycleTimer interface {
	Stop() bool
}

// ActiveAgentCounter 统计当前仍在运行的 agent 数量。
type ActiveAgentCounter interface {
	ActiveAgentCount(context.Context) (int, error)
}

// ActiveAgentCounterFunc 允许用函数适配 ActiveAgentCounter。
type ActiveAgentCounterFunc func(context.Context) (int, error)

// ActiveAgentCount 调用底层函数统计运行中 agent。
func (f ActiveAgentCounterFunc) ActiveAgentCount(ctx context.Context) (int, error) {
	return f(ctx)
}

// WailsLifecycle 协调桌面窗口退出、后端 shutdown 和前端提示事件。
// 回调字段用 mutex 保护，退出状态用 atomic 标记，避免 Wails 生命周期回调并发重入。
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

// NewWailsLifecycle 创建 Wails 生命周期协调器。
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

// SetQuitFunc 设置最终关闭桌面窗口的回调，并尝试消费等待中的 quit 请求。
func (l *WailsLifecycle) SetQuitFunc(fn func()) {
	l.mu.Lock()
	l.quitFunc = fn
	l.mu.Unlock()

	l.flushPendingQuit()
}

// SetShutdownerFunc 设置后端 shutdown 回调；若退出已开始则立即触发。
func (l *WailsLifecycle) SetShutdownerFunc(fn func()) {
	l.mu.Lock()
	l.shutdownerFunc = fn
	l.mu.Unlock()

	if l.shutdownStarted.Load() {
		l.requestBackendShutdown()
	}
}

// SetEventEmitter 设置发往前端的事件回调。
func (l *WailsLifecycle) SetEventEmitter(fn func(string, any)) {
	l.mu.Lock()
	l.emitFunc = fn
	l.mu.Unlock()
}

// MarkFrontendReady 标记前端已能接收退出相关事件。
func (l *WailsLifecycle) MarkFrontendReady() {
	l.frontendReady.Store(true)
	l.flushPendingQuit()
}

// ShouldQuit 拦截首次窗口退出，先提示活跃 agent 并启动后端 shutdown。
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

// OnShutdown 在 Wails shutdown 回调里触发后端停止。
func (l *WailsLifecycle) OnShutdown() {
	l.requestBackendShutdown()
}

// NotifyBackendStopped 在后端已停止后放行最终窗口退出。
func (l *WailsLifecycle) NotifyBackendStopped() {
	l.allowQuitAfterBackendShutdown()
}

// RequestQuit 由 app update 或内部流程请求退出，前端未 ready 时会挂起。
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

// NotifyBackendFailed 通知前端后端启动失败。
func (l *WailsLifecycle) NotifyBackendFailed() {
	l.allowQuitAfterBackendShutdown()
}

// allowQuitAfterBackendShutdown 停止硬截止定时器并安排最终退出。
func (l *WailsLifecycle) allowQuitAfterBackendShutdown() {
	l.stopShutdownTimer()
	l.quitAllowed.Store(true)
	if !l.frontendReady.Load() {
		l.pendingQuit.Store(true)
		return
	}
	l.invokeQuit()
}

// EmitEvent 向前端发送生命周期事件，未绑定 emitter 时静默跳过。
func (l *WailsLifecycle) EmitEvent(name string, payload any) {
	if l == nil {
		return
	}
	emit := l.loadEmitter()
	if emit != nil {
		emit(name, payload)
	}
}

// activeAgentCount 读取活跃 agent 数，缺失 counter 视为生命周期配置错误。
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

// emitQuitOverlay 通知前端展示退出等待提示。
func (l *WailsLifecycle) emitQuitOverlay(activeCount int) {
	l.EmitEvent(quitOverlayName, map[string]any{
		"active_agents": activeCount,
		"delay_ms":      quitGraceDelay.Milliseconds(),
		"at":            time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// emitQuitError 通知前端展示退出前的后端错误。
func (l *WailsLifecycle) emitQuitError(err error) {
	if err == nil {
		return
	}
	l.EmitEvent(quitErrorName, map[string]any{
		"message": err.Error(),
		"at":      time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// requestBackendShutdown 只触发一次后端停止，并用 goroutine 隔离回调 panic。
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

// flushPendingQuit 在前端 ready 后消费挂起的 quit 请求。
func (l *WailsLifecycle) flushPendingQuit() {
	if l == nil || !l.frontendReady.Load() {
		return
	}
	if l.pendingQuit.CompareAndSwap(true, false) {
		l.invokeQuit()
	}
}

// invokeQuit 异步调用窗口退出函数，缺失回调时保留 pending 状态。
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

// loadQuit 在线程安全范围内读取窗口退出回调。
func (l *WailsLifecycle) loadQuit() func() {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.quitFunc
}

// loadShutdowner 在线程安全范围内读取后端 shutdown 回调。
func (l *WailsLifecycle) loadShutdowner() func() {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.shutdownerFunc
}

// loadEmitter 在线程安全范围内读取前端事件回调。
func (l *WailsLifecycle) loadEmitter() func(string, any) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.emitFunc
}

// armShutdownTimer 启动后端 shutdown 硬截止定时器。
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

// stopShutdownTimer 停止并清空后端 shutdown 硬截止定时器。
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

// ShutdownHardDeadline 返回桌面退出等待后端停止的硬截止时间。
func ShutdownHardDeadline() time.Duration {
	return shutdownHardDeadline
}
