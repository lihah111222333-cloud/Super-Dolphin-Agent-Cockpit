package memory

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// teamSyncCoordinatorDrainGrace bounds OnStop wait for the coordinator
// worker so RunnerModule shutdown never hangs on TeamSyncService's
// network/git slow-path. Aligned with the other P22 P2 drain budgets so
// the total OnStop cost stays predictable.
const teamSyncCoordinatorDrainGrace = 10 * time.Second

type teamSyncOpKind int

const (
	teamSyncOpStart teamSyncOpKind = iota + 1
	teamSyncOpStop
)

type teamSyncOp struct {
	kind    teamSyncOpKind
	started threaddto.Started
	stopped threaddto.Stopped
}

// teamSyncCoordinator is the P22 P2 Finding 5/6 single owner of the
// thread.{Started,Stopped} -> TeamSyncService.{StartSession,StopSession}
// slow-path.
//
// Pre-P2 shape: the memory module's bus callbacks directly called
// teampkg.{Start,Stop}SessionFromThreadEvent. Those helpers synchronously
// invoked ThreadStore.GetByThreadID (disk), resolveRuntime -> git repo-slug
// detection (exec.Command), pullLocked (remote HTTP), newTeamSyncWatcher
// (fsnotify) and teamSyncWatcher.Start (goroutine spawn). All of it ran on
// the dispatcher's callback goroutine.
//
// P2 shape: the callback only calls EnqueueStart/EnqueueStop. A single
// tracked worker goroutine drains the FIFO queue and dispatches the
// existing thread-event helpers serially, so the cross-thread runtime
// swap + final-flush invariant that TeamSyncService already enforces stays
// intact. The coordinator itself adds no coalescing: TeamSync events are
// linear per-thread, and "lossless" in the P2 overflow freeze table means
// every enqueued event reaches the service unless Stop fires first.
type teamSyncCoordinator struct {
	svc    teampkg.Lifecycle
	store  contract.ThreadMetadataStore
	logger *pkglogger.Logger

	mu    sync.Mutex
	queue []teamSyncOp

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	processedTotal atomic.Int64
}

func newTeamSyncCoordinator(svc teampkg.Lifecycle, store contract.ThreadMetadataStore, logger *pkglogger.Logger) *teamSyncCoordinator {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &teamSyncCoordinator{
		svc:    svc,
		store:  store,
		logger: logger,
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start spawns the worker goroutine. Idempotent. When svc is nil (TeamSync
// disabled) the worker short-circuits: doneCh closes immediately so Stop
// is a no-op and Enqueue stays a cheap silent drop.
// Start 启动记忆流程。
func (c *teamSyncCoordinator) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		if c.svc == nil {
			close(c.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("memory: recovered team_sync_coordinator worker panic", "panic", rec)
				}
			}()
			c.runWorker()
		}()
	})
}

// EnqueueStart records a thread.Started event. Non-blocking: the only work
// on the callback path is an O(1) slice append + non-blocking wake.
// EnqueueStart 处理enqueue起点。
func (c *teamSyncCoordinator) EnqueueStart(ev threaddto.Started) {
	if c == nil {
		return
	}
	if strings.TrimSpace(ev.ThreadID) == "" {
		return
	}
	c.enqueue(teamSyncOp{kind: teamSyncOpStart, started: ev})
}

// EnqueueStop records a thread.Stopped event with the same non-blocking
// contract as EnqueueStart. StopSession semantics (final flush, last-session
// watcher close) stay inside TeamSyncService — the coordinator only
// dispatches events to it serially.
// EnqueueStop 处理enqueuestop。
func (c *teamSyncCoordinator) EnqueueStop(ev threaddto.Stopped) {
	if c == nil {
		return
	}
	if strings.TrimSpace(ev.ThreadID) == "" {
		return
	}
	c.enqueue(teamSyncOp{kind: teamSyncOpStop, stopped: ev})
}

func (c *teamSyncCoordinator) enqueue(op teamSyncOp) {
	select {
	case <-c.stopCh:
		return
	default:
	}
	c.mu.Lock()
	c.queue = append(c.queue, op)
	c.mu.Unlock()
	c.enqueuedTotal.Add(1)
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Stop closes the gate, drains any pending ops through the worker, and
// waits bounded by ctx for the worker to exit. Idempotent. Post-Stop
// enqueue is silently dropped because the subscription is about to be
// cancelled by RunnerModule shutdown anyway.
// Stop 停止记忆流程。
func (c *teamSyncCoordinator) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var firstErr error
	c.stopOnce.Do(func() {
		close(c.stopCh)
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > teamSyncCoordinatorDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = kernel.WithTimeout(waitCtx, teamSyncCoordinatorDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-c.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// EnqueuedTotal / ProcessedTotal expose the observability counters for
// tests and future metric hookup (P22 observability lane).
// EnqueuedTotal 处理enqueuedtotal。
func (c *teamSyncCoordinator) EnqueuedTotal() int64 { return c.enqueuedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (c *teamSyncCoordinator) ProcessedTotal() int64 { return c.processedTotal.Load() }

func (c *teamSyncCoordinator) runWorker() {
	defer close(c.doneCh)
	for {
		select {
		case <-c.stopCh:
			c.drainPending()
			return
		case <-c.wake:
			c.drainPending()
		}
	}
}

// drainPending pops each queued op under the lock and dispatches it with
// the lock released, preserving FIFO order. Ops are linear per thread so
// no cross-thread reordering is possible. Errors are logged but never
// halt the worker — the existing TeamSync helpers log + swallow already.
func (c *teamSyncCoordinator) drainPending() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.mu.Unlock()
			return
		}
		ops := c.queue
		c.queue = nil
		c.mu.Unlock()
		for _, op := range ops {
			c.dispatch(op)
			c.processedTotal.Add(1)
		}
	}
}

// dispatch 派发记忆。
func (c *teamSyncCoordinator) dispatch(op teamSyncOp) {
	switch op.kind {
	case teamSyncOpStart:
		if err := teampkg.StartSessionFromThreadEvent(c.svc, c.store, op.started); err != nil && c.logger != nil {
			c.logger.Warn("memory: team sync start session failed",
				"thread_id", op.started.ThreadID, "error", err)
		}
	case teamSyncOpStop:
		if err := teampkg.StopSessionFromThreadEvent(c.svc, op.stopped); err != nil && c.logger != nil {
			c.logger.Warn("memory: team sync stop session failed",
				"thread_id", op.stopped.ThreadID, "error", err)
		}
	}
}
