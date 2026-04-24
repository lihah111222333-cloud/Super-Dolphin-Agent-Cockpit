package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// autoDreamSchedulerQueueCap bounds the in-memory queue of threadIDs waiting
// for auto-dream eligibility evaluation. Bursty thread.stopped events (e.g.
// multi-agent shutdown) must not pin bus callback latency; overflow falls
// through to a dropped-signal counter rather than blocking the dispatcher.
const autoDreamSchedulerQueueCap = 64

// autoDreamSchedulerDrainGrace is the P22 P2 shutdown budget for draining the
// queue + waiting for any in-flight dream task before registerMemoryHooks.OnStop
// returns.
const autoDreamSchedulerDrainGrace = 10 * time.Second

// autoDreamScheduler is the P22 P2 single owner of the auto-dream scheduling
// path (Finding 7).
//
// Pre-P2 shape: the thread.stopped bus callback directly called
// `go maybeScheduleAutoDream(...)`, which itself `go`'d the consolidation
// worker. The callback held the scheduler/worker ownership, outside any
// explicit RunnerModule drain.
//
// P2 shape: the callback only calls Enqueue (non-blocking; drops on overflow).
// A single tracked worker goroutine consumes the queue and runs the
// synchronous eligibility + scheduling path under the scheduler's own ctx;
// the consolidation worker spawned inside launchAutoDreamTask keeps using the
// pre-existing startDreamTask / waitDreamTask fence.
type autoDreamScheduler struct {
	hooks  *MemoryLifecycleHooks
	logger *slog.Logger

	queue chan string

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	// taskCtx is the worker's context; cancelled by Stop so any ongoing
	// maybeScheduleAutoDream / consolidator call sees ctx.Err() and unwinds.
	taskCtx    context.Context
	taskCancel context.CancelFunc

	droppedTotal    atomic.Int64
	processedTotal  atomic.Int64
	scheduledTotal  atomic.Int64
}

func newAutoDreamScheduler(hooks *MemoryLifecycleHooks, logger *slog.Logger) *autoDreamScheduler {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &autoDreamScheduler{
		hooks:      hooks,
		logger:     logger,
		queue:      make(chan string, autoDreamSchedulerQueueCap),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		taskCtx:    ctx,
		taskCancel: cancel,
	}
}

// Start spawns the worker goroutine. Idempotent. The worker is guaranteed to
// have exited before Stop returns, so OnStop orchestration can rely on
// quiescence.
func (s *autoDreamScheduler) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if s.hooks == nil || !s.hooks.enabled {
			// Auto-dream disabled: mark worker as immediately drained so Stop
			// is a no-op and Enqueue drops silently (stopCh stays open to
			// keep the drop branch in Enqueue selectable, but doneCh must
			// close to signal the worker never ran).
			close(s.doneCh)
			return
		}
		go s.runWorker()
	})
}

// Enqueue submits a threadID for auto-dream evaluation. Called from the bus
// callback registered in registerAutoDreamSubscriptions. Non-blocking: drops
// when the queue is full (bounded backpressure) or when Stop has fired.
func (s *autoDreamScheduler) Enqueue(threadID string) {
	if s == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	select {
	case <-s.stopCh:
		s.droppedTotal.Add(1)
		return
	default:
	}
	select {
	case s.queue <- threadID:
	default:
		s.droppedTotal.Add(1)
		if s.logger != nil {
			s.logger.Warn("memory auto-dream scheduler: queue full, dropping enqueue",
				"thread_id", threadID, "cap", autoDreamSchedulerQueueCap)
		}
	}
}

// Stop closes the gate, cancels the worker ctx, and waits bounded by ctx for
// the worker + any in-flight dream task to drain. Idempotent.
func (s *autoDreamScheduler) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var firstErr error
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.taskCancel()
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > autoDreamSchedulerDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = platformconfig.WithTimeout(waitCtx, autoDreamSchedulerDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-s.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// DroppedTotal / ProcessedTotal / ScheduledTotal expose the observability
// counters for tests and future metric hookup (P2 observability lane).
func (s *autoDreamScheduler) DroppedTotal() int64   { return s.droppedTotal.Load() }
func (s *autoDreamScheduler) ProcessedTotal() int64 { return s.processedTotal.Load() }
func (s *autoDreamScheduler) ScheduledTotal() int64 { return s.scheduledTotal.Load() }

func (s *autoDreamScheduler) runWorker() {
	defer close(s.doneCh)
	for {
		select {
		case <-s.stopCh:
			return
		case threadID := <-s.queue:
			s.process(threadID)
		}
	}
}

func (s *autoDreamScheduler) process(threadID string) {
	if strings.TrimSpace(threadID) == "" {
		return
	}
	s.processedTotal.Add(1)
	scheduled, err := s.hooks.maybeScheduleAutoDream(s.taskCtx, threadID)
	if err != nil && !errors.Is(err, context.Canceled) {
		if s.logger != nil {
			s.logger.Warn("memory auto-dream scheduler: schedule failed",
				"thread_id", threadID, "error", err)
		}
		return
	}
	if scheduled {
		s.scheduledTotal.Add(1)
	}
}
