package memory

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// nestedIngestDrainGrace bounds the shutdown wait for nestedIngestWorker so
// RunnerModule shutdown can't hang if AddToolReadResult stalls on disk
// I/O during drain. Kept in the same order of magnitude as the auto-dream
// scheduler drain grace — both are owned by the same OnStop hook.
const nestedIngestDrainGrace = 10 * time.Second

// nestedIngestRuntime is the subset of *nestedpkg.NestedRuntime the worker
// uses. Declaring it here keeps the worker testable without spinning up a
// full NestedRuntime, and documents that the only contract the worker needs
// is a single method — all coalescing / dedupe lives on the worker side.
type nestedIngestRuntime interface {
	AddToolReadResult(threadID, toolName, result, persistedPath string)
}

// nestedIngestKey identifies a coalescable ingest request. The P22 P2 Finding
// 10 contract is "lossless pending-set / wake-signal": repeated events for
// the same (thread, tool, persisted-path) triple collapse to the latest
// payload, but nothing is silently dropped.
type nestedIngestKey struct {
	threadID      string
	toolName      string
	persistedPath string
}

type nestedIngestRequest struct {
	threadID      string
	toolName      string
	result        string
	persistedPath string
}

// nestedIngestWorker is the P22 P2 Finding 10 single owner of the
// ToolCallEnd → NestedRuntime.AddToolReadResult slow-path.
//
// Pre-P2 shape: the bus callback called NestedRuntime.AddToolReadResult
// directly; extractNestedReadToolPaths synchronously os.ReadFile'd the
// persisted path while holding the dispatcher's callback goroutine.
//
// P2 shape: the callback only calls Enqueue. A single tracked worker
// goroutine drains the pending map (deduped by nestedIngestKey, latest
// payload wins) and invokes AddToolReadResult off the callback path. The
// worker drains on Stop bounded by ctx so shutdown stays bounded even when
// the disk is slow.
type nestedIngestWorker struct {
	runtime nestedIngestRuntime
	logger  *pkglogger.Logger

	mu      sync.Mutex
	pending map[nestedIngestKey]nestedIngestRequest

	wake chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
	doneOnce  sync.Once
	started   atomic.Bool
	stopCh    chan struct{}
	doneCh    chan struct{}

	enqueuedTotal  atomic.Int64
	coalescedTotal atomic.Int64
	processedTotal atomic.Int64
}

func newNestedIngestWorker(runtime nestedIngestRuntime, logger *pkglogger.Logger) *nestedIngestWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &nestedIngestWorker{
		runtime: runtime,
		logger:  logger,
		pending: map[nestedIngestKey]nestedIngestRequest{},
		wake:    make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start spawns the worker goroutine. Idempotent. When runtime is nil the
// worker short-circuits: doneCh closes so Stop is immediate and Enqueue
// remains a cheap no-op.
// Start 启动记忆流程。
func (w *nestedIngestWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.runtime == nil {
			w.closeDone()
			return
		}
		select {
		case <-w.stopCh:
			w.closeDone()
			return
		default:
		}
		w.started.Store(true)
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("memory: recovered nested_ingest_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Enqueue records a ToolCallEnd ingest request. Safe to call from bus
// callbacks: O(1) map write + non-blocking wake signal, no file I/O, no
// AddToolReadResult call on the callback goroutine. Repeated events for
// the same (thread, tool, persistedPath) coalesce into the latest payload.
// Enqueue 把项目追加到队尾。
func (w *nestedIngestWorker) Enqueue(threadID, toolName, result, persistedPath string) {
	if w == nil {
		return
	}
	if w.runtime == nil {
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
	key := nestedIngestKey{
		threadID:      threadID,
		toolName:      strings.TrimSpace(toolName),
		persistedPath: strings.TrimSpace(persistedPath),
	}
	req := nestedIngestRequest{
		threadID:      threadID,
		toolName:      key.toolName,
		result:        result,
		persistedPath: key.persistedPath,
	}
	w.mu.Lock()
	if _, dup := w.pending[key]; dup {
		w.coalescedTotal.Add(1)
	}
	w.pending[key] = req
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Stop closes the gate, drains any pending requests, and waits bounded by
// ctx for the worker goroutine to exit. Idempotent. Enqueue after Stop is
// silently dropped (gate closed) — this is the only drop path in the
// lossless contract and is necessary because post-Stop delivery would race
// with cancelled subscriptions.
// Stop 停止记忆流程。
func (w *nestedIngestWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		if !w.started.Load() && w.runtime != nil {
			w.drainPending()
			w.closeDone()
			return
		}
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > nestedIngestDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = kernel.WithTimeout(waitCtx, nestedIngestDrainGrace)
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

// EnqueuedTotal / CoalescedTotal / ProcessedTotal expose the observability
// counters for tests and future metric hookup (P22 observability lane).
// EnqueuedTotal 处理enqueuedtotal。
func (w *nestedIngestWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// CoalescedTotal 处理coalescedtotal。
func (w *nestedIngestWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (w *nestedIngestWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

func (w *nestedIngestWorker) runWorker() {
	defer w.closeDone()
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

func (w *nestedIngestWorker) closeDone() {
	w.doneOnce.Do(func() {
		close(w.doneCh)
	})
}

// drainPending pulls the current pending set out under the lock, then
// invokes AddToolReadResult for each request with the lock released. That
// keeps the synchronous os.ReadFile inside AddToolReadResult off the
// callback goroutine and off the worker's own enqueue path.
func (w *nestedIngestWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.mu.Unlock()
			return
		}
		reqs := make([]nestedIngestRequest, 0, len(w.pending))
		for _, r := range w.pending {
			reqs = append(reqs, r)
		}
		w.pending = map[nestedIngestKey]nestedIngestRequest{}
		w.mu.Unlock()
		for _, r := range reqs {
			w.runtime.AddToolReadResult(r.threadID, r.toolName, r.result, r.persistedPath)
			w.processedTotal.Add(1)
		}
	}
}
