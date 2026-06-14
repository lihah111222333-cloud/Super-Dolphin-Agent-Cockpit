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

// sessionRecoveryDrainGrace bounds the shutdown wait for
// sessionRecoveryWorker. Unlike the other thread-domain workers the
// recovery path includes a 3s delay + a Resume network round-trip, so
// its grace budget is a bit wider. Matches teamSyncCoordinator's drain
// grace which has a similar cost profile.
const sessionRecoveryDrainGrace = 15 * time.Second

// sessionRecoveryReconnectDelay is the default reconnect delay used when
// constructing a new service. Tests can override via service.reconnectDelay.
const sessionRecoveryReconnectDelay = 3 * time.Second

// sessionRecoverer is the narrow contract over *service that the worker
// needs. processSessionRecovery carries the pre-P22 body of
// onAgentFailed minus the outer SafeGo + time.Sleep: it rate-limits,
// evicts the zombie session, waits (ctx-aware), and then invokes
// backgroundResumeIfNeeded.
type sessionRecoverer interface {
	processSessionRecovery(ctx context.Context, ev agentdto.AgentFailed)
}

// sessionRecoveryWorker is the P22 P2 (thread S2) single owner of the
// onAgentFailed -> session-level recovery slow-path.
//
// Pre-P22 shape: the bus callback checked the rate limit, evicted the
// zombie session, and then fired a naked
// runtimesafe.SafeGo(context.Background(), ...) that slept 3 seconds
// before calling backgroundResumeIfNeeded. Nothing tracked those
// goroutines; shutdown could never wait for them.
//
// S2 shape: the callback only calls Enqueue. The worker owns:
//   - A pending map keyed by target (threadID preferred, agentID
//     fallback) so repeated AgentFailed events for the same agent
//     collapse to one recovery attempt per burst.
//   - An inflight WaitGroup tracking per-event recovery goroutines so
//     multi-agent failures still recover in parallel, but drain is
//     bounded: Stop cancels the worker context (breaking the 3s sleep)
//     and then waits on inflight before closing doneCh.
//
// The 3s "wait for Codex to close the thread" lives inside
// processSessionRecovery as a ctx-aware select so Stop short-circuits
// instead of blocking.
type sessionRecoveryWorker struct {
	recoverer sessionRecoverer
	logger    *slog.Logger

	// ctx is the per-worker context threaded into every
	// processSessionRecovery call. cancel() fires from Stop, which
	// unblocks any in-flight reconnect delay / Resume.
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	pending map[string]agentdto.AgentFailed

	wake chan struct{}

	startOnce, stopOnce sync.Once
	stopCh, doneCh      chan struct{}

	// inflight tracks per-event recovery goroutines. runWorker defers
	// inflight.Wait() before close(doneCh) so Stop returns only after
	// every recovery has observed ctx cancellation and returned.
	inflight sync.WaitGroup

	enqueuedTotal, coalescedTotal, processedTotal atomic.Int64
}

func newSessionRecoveryWorker(recoverer sessionRecoverer, logger *slog.Logger) *sessionRecoveryWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &sessionRecoveryWorker{recoverer: recoverer, logger: logger, ctx: ctx, cancel: cancel, pending: map[string]agentdto.AgentFailed{}, wake: make(chan struct{}, 1), stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

// Start spawns the worker dispatcher goroutine. Idempotent. When
// recoverer is nil the worker short-circuits: doneCh closes so Stop is
// immediate and Enqueue is a cheap no-op.
// Start 启动线程流程。
func (w *sessionRecoveryWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.recoverer == nil {
			close(w.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("thread: recovered session_recovery_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Enqueue records an AgentFailed event for deferred recovery. Safe to
// call from bus callbacks: O(1) map write + non-blocking wake. The key
// is target (threadID preferred, agentID fallback) to match
// processSessionRecovery's shared.FirstNonEmpty semantics.
// Enqueue 把项目追加到队尾。
func (w *sessionRecoveryWorker) Enqueue(target string, ev agentdto.AgentFailed) {
	if w == nil {
		return
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	select {
	case <-w.stopCh:
		return
	default:
	}
	w.mu.Lock()
	if _, dup := w.pending[target]; dup {
		w.coalescedTotal.Add(1)
	}
	w.pending[target] = ev
	w.mu.Unlock()
	w.enqueuedTotal.Add(1)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Stop closes the gate, cancels the worker context (short-circuiting
// the 3s reconnect delay and any in-flight Resume), and waits bounded
// by ctx for the dispatcher + every recovery goroutine to exit.
// Stop 停止线程流程。
func (w *sessionRecoveryWorker) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	var firstErr error
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.cancel()
		waitCtx := ctx
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		if deadline, ok := waitCtx.Deadline(); !ok || time.Until(deadline) > sessionRecoveryDrainGrace {
			var cancel context.CancelFunc
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, sessionRecoveryDrainGrace)
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
// counters for tests and future metric hookup.
// EnqueuedTotal 处理enqueuedtotal。
func (w *sessionRecoveryWorker) EnqueuedTotal() int64 { return w.enqueuedTotal.Load() }

// CoalescedTotal 处理coalescedtotal。
func (w *sessionRecoveryWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (w *sessionRecoveryWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

func (w *sessionRecoveryWorker) runWorker() {
	// inflight.Wait is load-bearing: every recovery goroutine observes
	// w.ctx.Done() (from cancel in Stop) and exits; this Wait ensures
	// they all do before we close doneCh so Stop returns at the right
	// moment.
	defer close(w.doneCh)
	defer w.inflight.Wait()
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
// spawns one tracked goroutine per entry. Parallel dispatch preserves
// pre-P22 behavior where concurrent failures recovered in parallel via
// separate SafeGo goroutines; the difference is that those goroutines
// are now WaitGroup-tracked so Stop is bounded.
func (w *sessionRecoveryWorker) drainPending() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}
	batch := make([]agentdto.AgentFailed, 0, len(w.pending))
	for _, ev := range w.pending {
		batch = append(batch, ev)
	}
	w.pending = map[string]agentdto.AgentFailed{}
	w.mu.Unlock()
	for _, ev := range batch {
		w.inflight.Add(1)
		go func(ev agentdto.AgentFailed) {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("thread: recovered session_recovery per-event panic", "panic", rec)
				}
				w.inflight.Done()
			}()
			w.recoverer.processSessionRecovery(w.ctx, ev)
			w.processedTotal.Add(1)
		}(ev)
	}
}
