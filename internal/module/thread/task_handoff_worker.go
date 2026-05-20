package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// taskHandoffDrainGrace bounds subscription shutdown when handoff I/O stalls.
const taskHandoffDrainGrace = 10 * time.Second

// taskHandoffRefresher is the narrow contract over *service.
type taskHandoffRefresher interface {
	refreshTaskHandoffFromThread(ctx context.Context, threadID string, seed taskHandoffRenderSeed) error
}

// taskHandoffWorker owns async TurnCompleted handoff refreshes and coalesces
// repeated events per threadID (latest seed wins).
type taskHandoffWorker struct {
	refresher taskHandoffRefresher
	logger    *slog.Logger

	mu      sync.Mutex
	pending map[string]taskHandoffRenderSeed
	lastErr error

	wake chan struct{}

	startOnce, stopOnce sync.Once
	stopCh, doneCh      chan struct{}

	enqueuedTotal, coalescedTotal, processedTotal atomic.Int64
}

func newTaskHandoffWorker(refresher taskHandoffRefresher, logger *slog.Logger) *taskHandoffWorker {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &taskHandoffWorker{refresher: refresher, logger: logger, pending: map[string]taskHandoffRenderSeed{}, wake: make(chan struct{}, 1), stopCh: make(chan struct{}), doneCh: make(chan struct{})}
}

// Start spawns the worker goroutine. Idempotent.
func (w *taskHandoffWorker) Start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		if w.refresher == nil {
			close(w.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("thread: recovered task_handoff_worker panic", "panic", rec)
				}
			}()
			w.runWorker()
		}()
	})
}

// Enqueue records a TurnCompleted-driven refresh without doing I/O on the bus callback.
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

// Stop closes the gate, drains pending, and waits bounded by ctx.
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
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, taskHandoffDrainGrace)
			defer cancel()
			_ = deadline
		}
		select {
		case <-w.doneCh:
		case <-waitCtx.Done():
			firstErr = waitCtx.Err()
		}
	})
	if firstErr == nil {
		firstErr = w.LastError()
	}
	return firstErr
}

// EnqueuedTotal / CoalescedTotal / ProcessedTotal expose worker counters.
func (w *taskHandoffWorker) EnqueuedTotal() int64  { return w.enqueuedTotal.Load() }
func (w *taskHandoffWorker) CoalescedTotal() int64 { return w.coalescedTotal.Load() }
func (w *taskHandoffWorker) ProcessedTotal() int64 { return w.processedTotal.Load() }

func (w *taskHandoffWorker) LastError() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

func (w *taskHandoffWorker) recordRefreshError(threadID string, err error) {
	if w == nil || err == nil {
		return
	}
	w.mu.Lock()
	if w.lastErr == nil {
		w.lastErr = fmt.Errorf("task handoff refresh failed for thread %q: %w", threadID, err)
	}
	w.mu.Unlock()
}

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

// drainPending refreshes pending entries and records errors for Stop.
func (w *taskHandoffWorker) drainPending() {
	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			w.mu.Unlock()
			return
		}
		batch := w.pending
		w.pending = map[string]taskHandoffRenderSeed{}
		w.mu.Unlock()
		for threadID, seed := range batch {
			if err := w.refresher.refreshTaskHandoffFromThread(context.Background(), threadID, seed); err != nil {
				w.recordRefreshError(threadID, err)
				if w.logger != nil {
					w.logger.Warn("thread: task handoff worker refresh failed",
						"thread_id", threadID,
						"error", err,
					)
				}
				continue
			}
			w.processedTotal.Add(1)
		}
	}
}

// FlushForThread synchronously processes the pending refresh entry for
// the given threadID, if any. Used by Phase 1.8d fork-pre-check to ensure
// the handoff document on disk reflects the most recent turn before a
// continuation thread reads it (handoff worker is otherwise async / event-
// driven, last-write-wins). Returns nil if no pending entry exists, or
// the refresher error if the synchronous refresh fails. ctx controls timeout.
func (w *taskHandoffWorker) FlushForThread(ctx context.Context, threadID string) error {
	if w == nil || w.refresher == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	w.mu.Lock()
	seed, ok := w.pending[threadID]
	if ok {
		delete(w.pending, threadID)
	}
	w.mu.Unlock()
	if !ok {
		return nil
	}
	if err := w.refresher.refreshTaskHandoffFromThread(ctx, threadID, seed); err != nil {
		return err
	}
	w.processedTotal.Add(1)
	return nil
}

// ---------------------------------------------------------------------------
// PromoteTaskFromThread (was promote_task.go)
// ---------------------------------------------------------------------------

func (s *service) PromoteTaskFromThread(ctx context.Context, threadID string) (PromoteTaskResult, error) {
	if s == nil {
		return PromoteTaskResult{}, errors.New("thread service unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return PromoteTaskResult{}, errors.New("threadId required")
	}
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		return PromoteTaskResult{}, err
	}
	if thread == nil {
		return PromoteTaskResult{}, fmt.Errorf("thread %q not found", threadID)
	}

	stored, existing, err := promotedTaskConfig(thread.ConfigOverride)
	if err != nil {
		return PromoteTaskResult{}, err
	}
	if strings.TrimSpace(existing.TaskID) != "" {
		return PromoteTaskResult{
			ThreadID:    threadID,
			TaskID:      strings.TrimSpace(existing.TaskID),
			TaskTitle:   strings.TrimSpace(existing.TaskTitle),
			HandoffFile: strings.TrimSpace(existing.HandoffFile),
			AlreadyTask: true,
		}, nil
	}

	meta := taskHandoffMeta{
		TaskID:    idgen.NewID("task"),
		TaskTitle: util.FirstNonEmpty(strings.TrimSpace(thread.Name), strings.TrimSpace(thread.Prompt), threadID),
	}
	meta.HandoffFile = defaultTaskHandoffPath(meta.TaskID)
	meta.RootTaskID = meta.TaskID

	if err := s.ensureTaskHandoffShell(ctx, meta, threadID); err != nil {
		return PromoteTaskResult{}, err
	}

	stored.Runtime = withPromotedTaskRuntime(stored.Runtime, meta)
	raw, err := encodeStoredThreadConfig(stored)
	if err != nil {
		return PromoteTaskResult{}, fmt.Errorf("encode promoted thread config: %w", err)
	}
	thread.ConfigOverride = raw
	thread.UpdatedAt = time.Now().Unix()
	if err := s.upsertThread(ctx, *thread); err != nil {
		return PromoteTaskResult{}, fmt.Errorf("persist promoted thread config: %w", err)
	}

	result := PromoteTaskResult{
		ThreadID:    threadID,
		TaskID:      meta.TaskID,
		TaskTitle:   meta.TaskTitle,
		HandoffFile: meta.HandoffFile,
	}

	s.emitThreadPromotedTask(threadID)
	return result, nil
}

func promotedTaskConfig(raw json.RawMessage) (storedThreadConfig, taskHandoffMeta, error) {
	stored, err := decodeStoredThreadConfig(raw)
	if err != nil {
		return storedThreadConfig{}, taskHandoffMeta{}, err
	}
	existing, err := taskHandoffMetaFromRuntimeConfig(stored.Runtime)
	return stored, existing, err
}

func withPromotedTaskRuntime(runtime map[string]any, meta taskHandoffMeta) map[string]any {
	next := clone.RuntimeConfigMap(runtime)
	if next == nil {
		next = map[string]any{}
	}
	next[taskConfigKeyAuto] = true
	next[taskConfigKeyID] = meta.TaskID
	if meta.TaskTitle != "" {
		next[taskConfigKeyTitle] = meta.TaskTitle
	}
	if meta.HandoffFile != "" {
		next[taskConfigKeyHandoffFile] = meta.HandoffFile
	}
	if meta.RootTaskID != "" {
		next[taskConfigKeyRoot] = meta.RootTaskID
	}
	return next
}
