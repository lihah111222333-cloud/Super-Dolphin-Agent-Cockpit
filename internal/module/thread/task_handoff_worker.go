package thread

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// taskHandoffDrainGrace bounds the shutdown wait for taskHandoffWorker so
// registerSubscriptions.OnStop can't hang if sharedFiles.Upsert stalls on
// I/O during drain. Matches nestedIngestDrainGrace in the memory module —
// both are owned by subscription OnStop hooks.
const taskHandoffDrainGrace = 10 * time.Second

// taskHandoffRefresher is the narrow contract over *service that the
// taskHandoffWorker needs. refreshTaskHandoffFromThread already does the
// threadStore read, document render and sharedFiles.Upsert — the worker
// just owns the goroutine and coalesces events per threadID.
type taskHandoffRefresher interface {
	refreshTaskHandoffFromThread(ctx context.Context, threadID string, seed taskHandoffRenderSeed) error
}

// taskHandoffWorker is the P22 P2 (thread S3) single owner of the
// onTurnCompleted -> refreshTaskHandoffFromThread slow-path.
//
// Pre-P22 shape: the bus callback body read from threadStore, rendered a
// handoff document and wrote to sharedFiles inline — all on the
// dispatcher's callback goroutine. Multiple TurnCompleted events for the
// same thread each redid the full work.
//
// P2 shape: the callback only calls Enqueue. A single tracked worker
// goroutine drains a pending map keyed by threadID (latest seed wins);
// multiple TurnCompleted events for the same thread collapse to one
// refresh. Stop drains pending bounded by ctx so subscription OnStop
// stays bounded even when the disk is slow.
type taskHandoffWorker struct {
	refresher taskHandoffRefresher
	logger    *slog.Logger

	mu      sync.Mutex
	pending map[string]taskHandoffRenderSeed

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	coalescedTotal atomic.Int64
	processedTotal atomic.Int64
}

func newTaskHandoffWorker(refresher taskHandoffRefresher, logger *slog.Logger) *taskHandoffWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &taskHandoffWorker{
		refresher: refresher,
		logger:    logger,
		pending:   map[string]taskHandoffRenderSeed{},
		wake:      make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// Start spawns the worker goroutine. Idempotent. When refresher is nil
// the worker short-circuits: doneCh closes so Stop is immediate and
// Enqueue remains a cheap no-op.
func (w *taskHandoffWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.refresher == nil {
			close(w.doneCh)
			return
		}
		go w.runWorker()
	})
}

// Enqueue records a TurnCompleted-driven handoff refresh. Safe to call
// from bus callbacks: O(1) map write + non-blocking wake, no disk I/O,
// no refresher call on the callback goroutine. Repeated events for the
// same threadID coalesce to the latest seed (handoff documents are
// idempotent — last-write-wins mirrors pre-P22 behavior).
func (w *taskHandoffWorker) Enqueue(threadID string, seed taskHandoffRenderSeed) {
	if w == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	if _, dup := w.pending[threadID]; dup {
		w.coalescedTotal.Add(1)
	}
	w.pending[threadID] = seed
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Stop closes the gate, drains pending, and waits bounded by ctx for the
// worker goroutine to exit. Idempotent. Enqueue after Stop is silently
// dropped (gate closed); this is the only drop path and is necessary
// because post-Stop delivery would race with cancelled subscriptions.
func (w *taskHandoffWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > taskHandoffDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = platformconfig.WithTimeout(waitCtx, taskHandoffDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-w.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	return firstErr
}

// EnqueuedTotal / CoalescedTotal / ProcessedTotal expose observability
// counters for tests and future metric hookup (P22 observability lane).
func (w *taskHandoffWorker) EnqueuedTotal() int64  { return w.enqueuedTotal.Load() }
func (w *taskHandoffWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }
func (w *taskHandoffWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

func (w *taskHandoffWorker) runWorker() {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			w.drainPending()
			return
		case <-w.wake:
			w.drainPending()
		}
	}
}

// drainPending pulls the current pending set out under the lock, then
// invokes refreshTaskHandoffFromThread for each entry with the lock
// released. Errors are logged — the handoff document is eventually
// consistent, so a single failed refresh is not fatal.
func (w *taskHandoffWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.mu.Unlock()
			return
		}
		batch := make([]taskHandoffPendingEntry, 0, len(w.pending))
		for threadID, seed := range w.pending {
			batch = append(batch, taskHandoffPendingEntry{threadID: threadID, seed: seed})
		}
		w.pending = map[string]taskHandoffRenderSeed{}
		w.mu.Unlock()
		for _, entry := range batch {
			if err := w.refresher.refreshTaskHandoffFromThread(context.Background(), entry.threadID, entry.seed); err != nil {
				if w.logger != nil {
					w.logger.Warn("thread: task handoff worker refresh failed",
						"thread_id", entry.threadID,
						"error", err,
					)
				}
			}
			w.processedTotal.Add(1)
		}
	}
}

type taskHandoffPendingEntry struct {
	threadID string
	seed     taskHandoffRenderSeed
}
