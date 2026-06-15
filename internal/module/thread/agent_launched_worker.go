package thread

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// agentLaunchedDrainGrace bounds the shutdown wait for
// agentLaunchedWorker so registerSubscriptions.OnStop can't hang if the
// binding store write or prompt-assembly invalidate stalls on I/O during
// drain. Matches nestedIngestDrainGrace so subscription shutdown stays bounded.
const agentLaunchedDrainGrace = 10 * time.Second

// agentLaunchedProcessor is the narrow contract over *service the worker
// needs. processAgentLaunched carries the full pre-P22 body of
// onAgentLaunched (resolveBindingForEvent + syncAgentLaunchCWD +
// UpdateSessionUUID + invalidatePromptAssembly); the worker just owns
// the goroutine and coalesces per-agent bursts.
type agentLaunchedProcessor interface {
	processAgentLaunched(ev agentdto.AgentLaunched)
}

// agentLaunchedWorker is the P22 P2 (thread S4) single owner of the
// onAgentLaunched -> binding store + prompt invalidation slow-path.
//
// Pre-P22 shape: the bus callback synchronously resolved the binding,
// updated the session UUID, synced the CWD and invalidated the
// prompt-assembly cache — all on the dispatcher's callback goroutine.
// Multiple events for the same agent each redid the same DB reads /
// writes.
//
// S4 shape: the callback only calls Enqueue. A single tracked worker
// goroutine drains a pending map keyed by agentID (or threadID when
// agentID is absent on the event); repeated events for the same key
// coalesce to the latest payload. Stop drains pending bounded by ctx so
// subscription OnStop stays bounded even when the DB is slow.
type agentLaunchedWorker struct {
	processor agentLaunchedProcessor
	logger    *slog.Logger

	mu      sync.Mutex
	pending map[string]agentdto.AgentLaunched

	wake chan struct{}

	startOnce, stopOnce sync.Once
	stopCh, doneCh      chan struct{}

	enqueuedTotal, coalescedTotal, processedTotal atomic.Int64
}

func newAgentLaunchedWorker(processor agentLaunchedProcessor, logger *slog.Logger) *agentLaunchedWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &agentLaunchedWorker{processor: processor, logger: logger, pending: map[string]agentdto.AgentLaunched{}, wake: make(chan struct{}, 1), stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

// Start spawns the worker goroutine. Idempotent. When processor is nil
// the worker short-circuits: doneCh closes so Stop is immediate and
// Enqueue remains a cheap no-op.
// Start 启动线程流程。
func (w *agentLaunchedWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.processor == nil {
			close(w.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("thread: recovered agent_launched_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Enqueue records an AgentLaunched event for deferred processing. Safe
// to call from bus callbacks: O(1) map write + non-blocking wake, no DB
// I/O, no invalidation on the callback goroutine. The key is the
// event's agentID; callers pass threadID as a fallback when agentID is
// empty (Claude system:init omits agent_id on first turn).
// Enqueue 把项目追加到队尾。
func (w *agentLaunchedWorker) Enqueue(key string, ev agentdto.AgentLaunched) {
	if w == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	if _, dup := w.pending[key]; dup {
		w.coalescedTotal.Add(1)
	}
	w.pending[key] = ev
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Stop closes the gate, drains pending, and waits bounded by ctx for
// the worker goroutine to exit. Idempotent. Enqueue after Stop is
// silently dropped (gate closed); this is the only drop path and is
// necessary because post-Stop delivery would race with cancelled
// subscriptions.
// Stop 停止线程流程。
func (w *agentLaunchedWorker) Stop(ctx context.Context) error {
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
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > agentLaunchedDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, agentLaunchedDrainGrace)
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
// EnqueuedTotal 处理enqueuedtotal。
func (w *agentLaunchedWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// CoalescedTotal 处理coalescedtotal。
func (w *agentLaunchedWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (w *agentLaunchedWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

func (w *agentLaunchedWorker) runWorker() {
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
// invokes processAgentLaunched for each entry with the lock released.
// processAgentLaunched already handles its own errors (warn-log + skip);
// the worker only has to count.
func (w *agentLaunchedWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.mu.Unlock()
			return
		}
		batch := make([]agentdto.AgentLaunched, 0, len(w.pending))
		for _, ev := range w.pending {
			batch = append(batch, ev)
		}
		w.pending = map[string]agentdto.AgentLaunched{}
		w.mu.Unlock()
		for _, ev := range batch {
			w.processor.processAgentLaunched(ev)
			w.processedTotal.Add(1)
		}
	}
}
