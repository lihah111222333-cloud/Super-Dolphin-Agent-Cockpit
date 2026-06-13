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

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SessionRuntime is the session-private RunnerModule owner introduced by
// P22 P1c. It replaces the implicit reader / health / recovery goroutines
// that newSession() used to spawn, and collapses the three fire-and-forget
// paths (transport call failure, connection.dead notification, idle health
// failure) into a single coalesced, stop-gated signal stream.
//
// Lifecycle (per docs/plans/迁移/p22/P1c_CodexAppSessionRuntime.md §目标架构):
//
//	newSession()                     → builds session + runtime handle only
//	StartSession / ResumeSession     → runtime.Start() (single explicit startup)
//	session.handleConnectionDead /
//	session.checkIdleHealth /
//	session.callTransport            → only emit signals; runtime owns workers
//	session.Close / ForceStop        → runtime.Stop() (cancel + drain, single ingress)
//
// The sync path in callTransport still calls session.attemptRecovery directly
// because it is already owner-tracked by its caller goroutine; SessionRuntime
// only owns the async signal-driven recovery worker plus reader / health.
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

	// signalCh is a size-1 buffered channel. NotifyRecovery enqueues up to
	// one signal at a time; further bursts are counted as "coalesced" rather
	// than queued, which is what P1c §需冻结的兼容语义 demands.
	signalCh chan string

	// Reader lifecycle is owned here because P1c requires Close / ForceStop
	// to join reader without going through session.waitReadLoopStopped helper.
	readerMu     sync.Mutex
	readerDone   chan struct{}
	readerCancel context.CancelFunc

	// Config (overridable for tests via sessionRuntimeOption).
	healthInterval      time.Duration
	healthIdleThreshold time.Duration
	now                 func() time.Time

	// Observability counters. Emitted in structured logs on Stop.
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

// Start is idempotent: the first call spawns reader + health + recovery
// workers; every subsequent call is a no-op. StartSession / ResumeSession
// are the only production callers; tests call Start() directly.
// Start 启动codexapp provider流程。
func (r *SessionRuntime) Start() {
	r.startedOnce.Do(func() {
		r.started.Store(true)
		r.logger.Info("codexapp: session_runtime.start",
			"agent_id", r.s.agentID,
			"thread_id", r.s.ThreadID())
		// Reader is spawned via spawnReader (tracked outside wg so restart
		// on recovery does not race with wg.Wait).
		r.spawnReader()
		r.wg.Add(2)
		go r.safeRunHealthLoop()
		go r.safeRunRecoveryWorker()
	})
}

// Started reports whether Start has been called at least once.
// Started 记录阶段开始并返回开始时间。
func (r *SessionRuntime) Started() bool { return r.started.Load() }

// Stopped reports whether Stop has been initiated.
// Stopped 处理stopped。
func (r *SessionRuntime) Stopped() bool { return r.stopped.Load() }

// Stop closes the stop gate, cancels the session context, joins reader /
// health / recovery, and records the drain latency. Idempotent: second and
// subsequent callers block on drainCh until the first Stop finishes.
// Stop 停止codexapp provider流程。
func (r *SessionRuntime) Stop() {
	first := false
	r.stopOnce.Do(func() {
		first = true
		r.stopped.Store(true)
		startedAt := r.now()
		close(r.stopCh)
		// Cancel the session's own ctx — propagates to reader ReadLoop,
		// health ticker, recovery worker's Reconnect.
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

// Drained returns a channel closed once Stop has completed.
// Drained 标记运行时已经完成收尾。
func (r *SessionRuntime) Drained() <-chan struct{} { return r.drainCh }

// NotifyRecovery enqueues a recovery signal under the stop gate.
//
//   - Gate closed  → signal dropped (droppedSignalTotal incremented).
//   - Inbox empty  → signal enqueued (recoverySignalTotal incremented).
//   - Inbox full   → signal coalesced (recoveryCoalescedTotal incremented).
//
// The source tag is a short identifier: "connection-dead", "health-failure",
// "transport-call" — matching P1c §覆盖问题 three ingress paths.
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

// RecoverySignalsTotal / RecoveryCoalescedTotal / DroppedSignalsTotal expose
// the internal counters for test assertions and future metric hookup (P2).
// RecoverySignalsTotal 处理recoverysignalstotal。
func (r *SessionRuntime) RecoverySignalsTotal() int64 { return r.recoverySignalTotal.Load() }

// RecoveryCoalescedTotal 处理recoverycoalescedtotal。
func (r *SessionRuntime) RecoveryCoalescedTotal() int64 { return r.recoveryCoalescedTotal.Load() }

// DroppedSignalsTotal 处理droppedsignalstotal。
func (r *SessionRuntime) DroppedSignalsTotal() int64 { return r.droppedSignalTotal.Load() }

// -----------------------------------------------------------------------------
// Health loop
// -----------------------------------------------------------------------------

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

// tickHealth runs one health probe. On transport failure it converts the
// failure into a recovery signal; it never spawns its own worker.
// tickHealth 处理tickhealth。
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
	// RPC protocol errors are "server alive, returned error"; not a transport
	// problem, no recovery.
	if strings.Contains(msg, "rpc error") ||
		strings.Contains(msg, "invalid request") ||
		strings.Contains(msg, "method not found") {
		r.s.noteReadActivity()
		return
	}
	r.NotifyRecovery("health-failure", err.Error())
}

// -----------------------------------------------------------------------------
// Recovery worker
// -----------------------------------------------------------------------------

func (r *SessionRuntime) safeRunRecoveryWorker() {
	defer r.wg.Done()
	defer func() { r.recoverWorkerPanic("session_runtime.recoveryWorker", recover()) }()
	r.runRecoveryWorker()
}

// runRecoveryWorker 运行recoveryworker。
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

// -----------------------------------------------------------------------------
// Reader management
// -----------------------------------------------------------------------------

// spawnReader starts a new reader goroutine if the stop gate is open and no
// reader is currently tracked. Returns true when a goroutine was spawned.
// Callers that need to replace an existing reader (e.g. attemptRecovery after
// Reconnect) must waitReader first.
// spawnReader 处理spawn读取器。
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
	go func() {
		defer func() { r.recoverWorkerPanic("session_runtime.reader", recover()) }()
		defer close(done)
		r.s.transport.ReadLoop(readCtx, r.s.onInboundMessage)
		r.logger.Warn("codexapp: read loop exited",
			"agent_id", r.s.agentID,
			"thread_id", r.s.ThreadID(),
			"ctx_err", readCtx.Err())
	}()
	return true
}

// restartReader is attemptRecovery's hook for spawning a fresh reader after a
// successful Reconnect. It assumes waitReader has already joined the old one.
// Returns false when the stop gate is closed.
func (r *SessionRuntime) restartReader() bool {
	return r.spawnReader()
}

// cancelReader cancels the current reader's context so ReadLoop exits.
// Called by Stop and from tests that need to prove drain semantics.
func (r *SessionRuntime) cancelReader() {
	r.readerMu.Lock()
	cancel := r.readerCancel
	r.readerMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// waitReader blocks until the current reader's goroutine has finished, or
// ctx cancels. Returns nil if no reader is tracked.
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

// waitReaderDone blocks unconditionally on the current reader's done channel.
// Used by Stop, which cannot fail.
func (r *SessionRuntime) waitReaderDone() {
	r.readerMu.Lock()
	done := r.readerDone
	r.readerMu.Unlock()
	if done == nil {
		return
	}
	<-done
}

// errRuntimeStopped is returned by attemptRecovery when the runtime has been
// stopped mid-flight (e.g. Close raced with a callTransport retry).
var (
	errRuntimeStopped = errors.New("codexapp: session runtime stopped")
	errSessionClosing = errors.New("codexapp: session closing")
)

// recoverWorkerPanic catches any panic from a session runtime worker goroutine,
// logging it with structured context so the process stays alive. This replaces
// what would otherwise be a fatal crash from an unrecovered panic.
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
