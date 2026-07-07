package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SessionRuntime 管理单个 Codex 会话的 reader、健康探测和异步恢复 worker。
// Start 只启动一次，Stop 关闭 stop gate 并等待所有 worker 收尾；同步 RPC 恢复仍由调用方 goroutine 直接执行。
type SessionRuntime struct {
	s      *session
	logger *slog.Logger

	startedOnce sync.Once
	started     atomic.Bool
	stopOnce    sync.Once
	stopped     atomic.Bool
	stopCh      chan struct{}
	drainCh     chan struct{}

	wg sync.WaitGroup

	// signalCh 只缓存一个恢复信号，突发故障会合并计数，避免恢复 worker 积压重复重连。
	signalCh chan string

	// reader 生命周期归 runtime 管理，Stop 必须能取消并等待当前 ReadLoop 退出。
	readerMu     sync.Mutex
	readerDone   chan struct{}
	readerCancel context.CancelFunc

	// 健康探测配置，可由测试通过 sessionRuntimeOption 覆盖。
	healthInterval      time.Duration
	healthIdleThreshold time.Duration
	now                 func() time.Time

	// 恢复信号计数会在 Stop 时写入结构化日志，供测试和诊断读取。
	recoverySignalTotal    atomic.Int64
	recoveryCoalescedTotal atomic.Int64
	droppedSignalTotal     atomic.Int64
}

type sessionRuntimeOption func(*SessionRuntime)

func withHealthInterval(d time.Duration) sessionRuntimeOption {
	return func(r *SessionRuntime) { r.healthInterval = d }
}

func withHealthIdleThreshold(d time.Duration) sessionRuntimeOption {
	return func(r *SessionRuntime) { r.healthIdleThreshold = d }
}

func withClock(now func() time.Time) sessionRuntimeOption {
	return func(r *SessionRuntime) { r.now = now }
}

func newSessionRuntime(s *session, logger *slog.Logger, opts ...sessionRuntimeOption) *SessionRuntime {
	if logger == nil {
		logger = pkglogger.Get()
	}
	r := &SessionRuntime{
		s:                   s,
		logger:              logger,
		stopCh:              make(chan struct{}),
		drainCh:             make(chan struct{}),
		signalCh:            make(chan string, 1),
		healthInterval:      healthCheckInterval,
		healthIdleThreshold: healthCheckIdleThreshold,
		now:                 time.Now,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Start 幂等启动 reader、健康探测和恢复 worker。
// 生产路径只应由 StartSession/ResumeSession 调用；重复调用不会创建第二组 goroutine。
func (r *SessionRuntime) Start() {
	r.startedOnce.Do(func() {
		r.started.Store(true)
		r.logger.Info("codexapp: session_runtime.start",
			"agent_id", r.s.agentID,
			"thread_id", r.s.ThreadID())
		// reader 单独跟踪，恢复时可以替换 reader，而不会和 Stop 的 wg.Wait 竞争。
		r.spawnReader()
		r.wg.Add(2)
		r.startRuntimeWorker("codexapp.sessionRuntime.healthLoop", r.safeRunHealthLoop)
		r.startRuntimeWorker("codexapp.sessionRuntime.recoveryWorker", r.safeRunRecoveryWorker)
	})
}

func (r *SessionRuntime) startRuntimeWorker(label string, fn func()) {
	safego.Go(r.s.ctx, nil, label, func(context.Context) {
		fn()
	})
}

// Started 返回 Start 是否至少执行过一次。
// 该状态只表示 runtime 已进入启动路径，不保证 reader 当前仍存活。
func (r *SessionRuntime) Started() bool { return r.started.Load() }

// Stopped 返回 Stop 是否已经开始执行。
// 一旦为 true，新的恢复信号和 reader 重启都必须被 stop gate 拒绝。
func (r *SessionRuntime) Stopped() bool { return r.stopped.Load() }

// Stop 关闭 stop gate、取消 session context，并等待 reader/health/recovery 全部退出。
// 多个调用方并发 Stop 时，只有第一个执行清理，其余调用方等待 drainCh 完成。
func (r *SessionRuntime) Stop() {
	first := false
	r.stopOnce.Do(func() {
		first = true
		r.stopped.Store(true)
		startedAt := r.now()
		close(r.stopCh)
		// 取消 session context 会传播到 ReadLoop、健康探测和恢复中的 Reconnect。
		r.s.cancel()
		r.cancelReader()
		r.wg.Wait()
		r.waitReaderDone()
		drainNanos := r.now().Sub(startedAt).Nanoseconds()
		r.logger.Info("codexapp: session_runtime.drained",
			"agent_id", r.s.agentID,
			"thread_id", r.s.ThreadID(),
			"signals", r.recoverySignalTotal.Load(),
			"coalesced", r.recoveryCoalescedTotal.Load(),
			"dropped", r.droppedSignalTotal.Load(),
			"drain_nanos", drainNanos,
		)
		close(r.drainCh)
	})
	if !first {
		<-r.drainCh
	}
}

// Drained 返回 Stop 完成后关闭的通道。
// 外部只应等待该通道，不应尝试自行关闭或替换 runtime 内部状态。
func (r *SessionRuntime) Drained() <-chan struct{} { return r.drainCh }

// NotifyRecovery 在 stop gate 未关闭时提交一次恢复信号。
//
//   - gate 已关闭：丢弃并计入 droppedSignalTotal。
//   - inbox 为空：入队并计入 recoverySignalTotal。
//   - inbox 已满：合并并计入 recoveryCoalescedTotal。
//
// source 是短标签，当前用于区分 connection-dead、health-failure、transport-call 等入口。
func (r *SessionRuntime) NotifyRecovery(source, reason string) {
	select {
	case <-r.stopCh:
		r.droppedSignalTotal.Add(1)
		r.logger.Debug("codexapp: recovery.dropped",
			"source", source,
			"reason", reason)
		return
	default:
	}
	r.recoverySignalTotal.Add(1)
	tagged := source + ": " + strings.TrimSpace(reason)
	select {
	case r.signalCh <- tagged:
	default:
		r.recoveryCoalescedTotal.Add(1)
		r.logger.Debug("codexapp: recovery.coalesced",
			"source", source,
			"reason", reason)
	}
}

// RecoverySignalsTotal 返回已进入恢复队列的信号数。
// 该值用于测试和诊断，不代表恢复最终成功次数。
func (r *SessionRuntime) RecoverySignalsTotal() int64 { return r.recoverySignalTotal.Load() }

// RecoveryCoalescedTotal 返回因恢复队列已满而被合并的信号数。
// 用它可以判断连接抖动是否集中爆发，而不是逐个排队重连。
func (r *SessionRuntime) RecoveryCoalescedTotal() int64 { return r.recoveryCoalescedTotal.Load() }

// DroppedSignalsTotal 返回 stop gate 关闭后被丢弃的恢复信号数。
// Stop 期间出现增长是预期行为，表示关闭路径没有再启动恢复。
func (r *SessionRuntime) DroppedSignalsTotal() int64 { return r.droppedSignalTotal.Load() }

func (r *SessionRuntime) safeRunHealthLoop() {
	defer r.wg.Done()
	defer func() { r.recoverWorkerPanic("session_runtime.healthLoop", recover()) }()
	r.runHealthLoop()
}

func (r *SessionRuntime) runHealthLoop() {
	ticker := time.NewTicker(r.healthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-r.s.ctx.Done():
			return
		case <-ticker.C:
			r.tickHealth()
		}
	}
}

// tickHealth 执行一次空闲健康探测，并把 transport 故障转换为恢复信号。
// RPC 协议类错误说明服务仍在响应，不会触发恢复。
func (r *SessionRuntime) tickHealth() {
	if r.s.recovery == nil {
		return
	}
	if r.now().Sub(r.s.lastReadTime()) < r.healthIdleThreshold {
		return
	}
	err := r.s.recovery.CheckHealth(r.s.ctx)
	if err == nil {
		r.s.noteReadActivity()
		return
	}
	r.logger.Warn("codexapp: health check failed", "error", err)
	msg := strings.ToLower(err.Error())
	// RPC 协议错误代表 server 活着但拒绝了请求，不属于连接失活。
	if strings.Contains(msg, "rpc error") ||
		strings.Contains(msg, "invalid request") ||
		strings.Contains(msg, "method not found") {
		r.s.noteReadActivity()
		return
	}
	r.NotifyRecovery("health-failure", err.Error())
}

func (r *SessionRuntime) safeRunRecoveryWorker() {
	defer r.wg.Done()
	defer func() { r.recoverWorkerPanic("session_runtime.recoveryWorker", recover()) }()
	r.runRecoveryWorker()
}

// runRecoveryWorker 串行消费恢复信号并调用 session 恢复流程。
// worker 只在 stop gate 关闭时退出，单次恢复失败会记录告警但不终止 worker。
func (r *SessionRuntime) runRecoveryWorker() {
	for {
		select {
		case <-r.stopCh:
			return
		case reason := <-r.signalCh:
			if err := r.s.attemptRecovery(reason); err != nil {
				r.logger.Warn("codexapp: session_runtime recovery failed",
					"reason", reason,
					"error", err)
			}
		}
	}
}

// spawnReader 在 stop gate 未关闭且没有活跃 reader 时启动新的 ReadLoop goroutine。
// 恢复路径替换 reader 前必须先 waitReader，避免两个 reader 同时消费同一 WebSocket。
func (r *SessionRuntime) spawnReader() bool {
	r.readerMu.Lock()
	defer r.readerMu.Unlock()
	select {
	case <-r.stopCh:
		return false
	default:
	}
	if r.readerDone != nil {
		select {
		case <-r.readerDone:
			// previous reader already finished; safe to replace
		default:
			// previous reader still running — refuse to spawn concurrent one
			return false
		}
	}
	done := make(chan struct{})
	readCtx, cancel := context.WithCancel(r.s.ctx)
	r.readerDone = done
	r.readerCancel = cancel
	r.startReaderLoop(readCtx, done)
	return true
}

func (r *SessionRuntime) startReaderLoop(readCtx context.Context, done chan struct{}) {
	safego.Go(readCtx, nil, "codexapp.sessionRuntime.reader", func(context.Context) {
		defer func() { r.recoverWorkerPanic("session_runtime.reader", recover()) }()
		defer close(done)
		r.s.transport.ReadLoop(readCtx, r.s.onInboundMessage)
		r.logger.Warn("codexapp: read loop exited",
			"agent_id", r.s.agentID,
			"thread_id", r.s.ThreadID(),
			"ctx_err", readCtx.Err())
	})
}

// restartReader 在 Reconnect 成功后启动新的读取 goroutine。
// 调用方必须先等待旧 reader 退出；Stop gate 已关闭时返回 false。
func (r *SessionRuntime) restartReader() bool {
	return r.spawnReader()
}

// cancelReader 取消当前 reader 上下文，让 ReadLoop 自行退出。
// Stop 和验证 drain 行为的测试会共用这条关闭路径。
func (r *SessionRuntime) cancelReader() {
	r.readerMu.Lock()
	cancel := r.readerCancel
	r.readerMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// waitReader 等待当前 reader goroutine 退出，或在 ctx 取消时返回错误。
// 没有已登记 reader 时返回 nil，表示恢复流程无需等待旧读取器。
func (r *SessionRuntime) waitReader(ctx context.Context) error {
	r.readerMu.Lock()
	done := r.readerDone
	r.readerMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitReaderDone 无条件等待当前 reader 的 done 通道。
// Stop 路径不能返回错误，因此使用这个不可失败的收尾 helper。
func (r *SessionRuntime) waitReaderDone() {
	r.readerMu.Lock()
	done := r.readerDone
	r.readerMu.Unlock()
	if done == nil {
		return
	}
	<-done
}

// Close 关闭 Codex app 会话并执行优雅清理。
func (s *session) Close(context.Context) error { return s.shutdownSession(true) }

// ForceStop 强制停止 Codex app 会话。
func (s *session) ForceStop() error { return s.shutdownSession(false) }

// SessionRuntime 返回会话运行时状态。
func (s *session) SessionRuntime() *SessionRuntime { return s.runtime }

// errRuntimeStopped 表示恢复过程中 runtime 已停止。
// 常见场景是 Close 与 callTransport 重试并发，调用方应按会话关闭处理。
var (
	errRuntimeStopped = errors.New("codexapp: session runtime stopped")
	errSessionClosing = errors.New("codexapp: session closing")
)

// recoverWorkerPanic 捕获 session runtime worker goroutine 的 panic。
// 它记录结构化上下文并保住进程，避免单个后台 worker 的未恢复 panic 直接崩溃宿主。
func (r *SessionRuntime) recoverWorkerPanic(label string, rec any) {
	if rec != nil {
		r.logger.Error("codexapp: recovered worker panic",
			"label", label,
			"agent_id", r.s.agentID,
			"thread_id", r.s.ThreadID(),
			"panic", rec,
			"stack", string(debug.Stack()),
		)
	}
}
