package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	autoDreamMinInterval  = 24 * time.Hour
	autoDreamMinSessions  = 5
	autoDreamScanThrottle = 10 * time.Minute

	dreamTaskPhaseStarting = "starting"
	dreamTaskPhaseUpdating = "updating"
)

type autoDreamExecutionPlan struct {
	root         string
	lastSuccess  time.Time
	sessionCount int
	extractFn    ExtractFunc
}

type dreamTaskState struct {
	threadID string
	phase    string
	cancel   context.CancelFunc
	done     chan struct{}
}

type DreamTaskSnapshot struct {
	Running  bool
	ThreadID string
	Phase    string
}

var ErrDreamTaskNotRunning = errors.New("dream task is not running")

func (h *MemoryLifecycleHooks) GetDreamTaskStatus() DreamTaskSnapshot {
	return h.dreamTaskSnapshot()
}

func (h *MemoryLifecycleHooks) KillDreamTask() error {
	if !h.killDreamTask() {
		return ErrDreamTaskNotRunning
	}
	return nil
}

func (h *MemoryLifecycleHooks) startDreamTask(threadID string) (context.Context, bool) {
	if h == nil {
		return nil, false
	}
	h.dreamMu.Lock()
	defer h.dreamMu.Unlock()
	if h.dreamTask != nil {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.dreamTask = &dreamTaskState{
		threadID: threadID,
		phase:    dreamTaskPhaseStarting,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	return ctx, true
}

func (h *MemoryLifecycleHooks) setDreamTaskPhase(phase string) {
	if h == nil || strings.TrimSpace(phase) == "" {
		return
	}
	h.dreamMu.Lock()
	if h.dreamTask != nil {
		h.dreamTask.phase = strings.TrimSpace(phase)
	}
	h.dreamMu.Unlock()
}

func (h *MemoryLifecycleHooks) finishDreamTask() {
	if h == nil {
		return
	}
	h.dreamMu.Lock()
	task := h.dreamTask
	h.dreamTask = nil
	h.dreamMu.Unlock()
	if task != nil && task.done != nil {
		close(task.done)
	}
}

func (h *MemoryLifecycleHooks) dreamTaskSnapshot() DreamTaskSnapshot {
	if h == nil {
		return DreamTaskSnapshot{}
	}
	h.dreamMu.Lock()
	defer h.dreamMu.Unlock()
	if h.dreamTask == nil {
		return DreamTaskSnapshot{}
	}
	return DreamTaskSnapshot{
		Running:  true,
		ThreadID: h.dreamTask.threadID,
		Phase:    h.dreamTask.phase,
	}
}

func (h *MemoryLifecycleHooks) killDreamTask() bool {
	if h == nil {
		return false
	}
	h.dreamMu.Lock()
	cancel := context.CancelFunc(nil)
	if h.dreamTask != nil {
		cancel = h.dreamTask.cancel
	}
	h.dreamMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (h *MemoryLifecycleHooks) waitDreamTask(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.dreamMu.Lock()
	done := (<-chan struct{})(nil)
	if h.dreamTask != nil {
		done = h.dreamTask.done
	}
	h.dreamMu.Unlock()
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

// registerAutoDreamSubscriptions wires the thread.stopped bus subscription
// to the single auto-dream owner. Per P22 P2 Finding 7 the callback is now
// a non-blocking enqueue; the scheduler's tracked worker runs
// maybeScheduleAutoDream under its own ctx so Close(gate) + Drain is the
// sole path for shutdown, replacing the pre-P2 fire-and-forget `go`.
func registerAutoDreamSubscriptions(p memorySubscriptionDeps, scheduler *autoDreamScheduler, appendCancel func(context.CancelFunc)) {
	if p.Hooks == nil || !p.Hooks.enabled || scheduler == nil {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Stopped) {
		scheduler.Enqueue(ev.ThreadID)
	}, pkglogger.Get()))
}

func (h *MemoryLifecycleHooks) maybeScheduleAutoDream(ctx context.Context, threadID string) (bool, error) {
	threadID, ok := h.autoDreamThreadEligible(ctx, threadID)
	if !ok {
		return false, nil
	}
	plan, ok, err := h.prepareAutoDreamExecution(ctx, threadID)
	if err != nil || !ok {
		return false, err
	}
	taskCtx, started := h.startDreamTask(threadID)
	if !started {
		return false, nil
	}
	h.launchAutoDreamTask(taskCtx, threadID, plan)
	return true, nil
}

func (h *MemoryLifecycleHooks) autoDreamThreadEligible(ctx context.Context, threadID string) (string, bool) {
	if h == nil || h.consolidator == nil {
		return "", false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false
	}
	meta := h.resolveThreadRuntimeMetadata(ctx, threadID)
	if !h.autoDreamAllowed(meta) {
		return "", false
	}
	return threadID, true
}

func (h *MemoryLifecycleHooks) autoDreamAllowed(meta threadRuntimeMetadata) bool {
	return meta.isAutoMemoryRootThread() && !meta.hasAgentMemoryScope() && h.isGateOpen(meta)
}

func (h *MemoryLifecycleHooks) prepareAutoDreamExecution(ctx context.Context, threadID string) (autoDreamExecutionPlan, bool, error) {
	root, err := h.autoDreamRoot()
	if err != nil {
		return autoDreamExecutionPlan{}, false, err
	}
	lastSuccess, ok, err := h.prepareAutoDreamWindow(root)
	if err != nil || !ok {
		return autoDreamExecutionPlan{}, false, err
	}
	sessionCount, err := h.autoDreamSessionCount(ctx, threadID, lastSuccess)
	if err != nil {
		return autoDreamExecutionPlan{}, false, err
	}
	if sessionCount < autoDreamMinSessions {
		return autoDreamExecutionPlan{}, false, nil
	}
	extractFn, err := h.resolveDreamExtractFunc()
	if err != nil {
		return autoDreamExecutionPlan{}, false, err
	}
	return autoDreamExecutionPlan{
		root:         root,
		lastSuccess:  lastSuccess,
		sessionCount: sessionCount,
		extractFn:    extractFn,
	}, true, nil
}

func (h *MemoryLifecycleHooks) autoDreamRoot() (string, error) {
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return "", err
	}
	if err := rejectConsolidationPath(h.cfg, root); err != nil {
		return "", err
	}
	return root, nil
}

func (h *MemoryLifecycleHooks) prepareAutoDreamWindow(root string) (time.Time, bool, error) {
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		return time.Time{}, false, err
	}
	now := h.now()
	if !shouldAutoDreamScan(stamp, now) {
		return time.Time{}, false, nil
	}
	if err := recordConsolidationScan(root, now); err != nil {
		return time.Time{}, false, err
	}
	lastSuccess := stamp.lastSuccessTime()
	if !lastSuccess.IsZero() && now.Sub(lastSuccess) < autoDreamMinInterval {
		return time.Time{}, false, nil
	}
	return lastSuccess, true, nil
}

func (h *MemoryLifecycleHooks) resolveDreamExtractFunc() (ExtractFunc, error) {
	extractFn := h.extractFn
	if extractFn == nil {
		extractFn = h.consolidator.resolveExtractFunc(nil)
	}
	if extractFn == nil {
		return nil, ErrConsolidationExtractFuncRequired
	}
	return extractFn, nil
}

func (h *MemoryLifecycleHooks) launchAutoDreamTask(taskCtx context.Context, threadID string, plan autoDreamExecutionPlan) {
	go func() {
		defer h.finishDreamTask()
		err := h.consolidator.consolidateWithOptions(taskCtx, plan.root, plan.extractFn, consolidationRunOptions{
			cfg:            h.cfg,
			now:            h.now,
			runtimeContext: buildConsolidationRuntimeContext("background auto-dream stop hook", plan.sessionCount, plan.lastSuccess, threadID),
			onLocked: func() {
				h.setDreamTaskPhase(dreamTaskPhaseUpdating)
			},
		})
		if err != nil {
			if h.logger != nil && !errors.Is(err, context.Canceled) {
				h.logger.Warn("memory auto-dream execution failed", "thread_id", threadID, "error", err)
			}
			return
		}
		// Consolidation rewrote MEMORY.md; flush the prompt cache so the
		// next AssembleStart picks up the consolidated index.
		h.invalidateMemorySections()
	}()
}

func (h *MemoryLifecycleHooks) isGateOpen(meta threadRuntimeMetadata) bool {
	if h == nil || !meta.isAutoMemoryRootThread() || meta.hasAgentMemoryScope() {
		return false
	}
	gate := ResolveMemoryGate(meta.buildCtx(), h.cfg)
	if gate.KairosActive {
		return false
	}
	return gate.AutoEnabled
}

func (h *MemoryLifecycleHooks) autoDreamSessionCount(ctx context.Context, currentThreadID string, since time.Time) (int, error) {
	if h == nil || h.threadStore == nil {
		return 0, nil
	}
	threads, err := h.threadStore.ListAll(ctx)
	if err != nil {
		return 0, err
	}
	projectKey := h.autoDreamProjectKey()
	count := 0
	for idx := range threads {
		if shouldCountAutoDreamThread(threads[idx], currentThreadID, projectKey, since) {
			count++
		}
	}
	return count, nil
}

func shouldCountAutoDreamThread(thread contract.ThreadMetadata, currentThreadID, projectKey string, since time.Time) bool {
	threadID := strings.TrimSpace(thread.ThreadID)
	if threadID == "" || threadID == currentThreadID {
		return false
	}
	meta := resolveThreadRuntimeMetadataFromThread(&thread)
	if !meta.isAutoMemoryRootThread() || meta.hasAgentMemoryScope() {
		return false
	}
	if projectKey != "" && !sameAutoDreamProject(projectKey, strings.TrimSpace(thread.Cwd)) {
		return false
	}
	observedAt := threadObservedAt(thread)
	return since.IsZero() || observedAt.After(since)
}

func (h *MemoryLifecycleHooks) autoDreamProjectKey() string {
	if h == nil || strings.TrimSpace(h.autoMemPathOverride) != "" {
		return ""
	}
	return autoDreamProjectKey(strings.TrimSpace(h.projectRoot))
}

func autoDreamProjectKey(projectRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return ""
	}
	if canonical, err := FindCanonicalGitRoot(context.Background(), projectRoot); err == nil && strings.TrimSpace(canonical) != "" {
		return filepath.Clean(canonical)
	}
	if cleaned, err := shared.CleanAbsolutePath(projectRoot); err == nil {
		return cleaned
	}
	return projectRoot
}

func sameAutoDreamProject(currentKey, cwd string) bool {
	if strings.TrimSpace(currentKey) == "" {
		return true
	}
	if strings.TrimSpace(cwd) == "" {
		return false
	}
	return autoDreamProjectKey(cwd) == currentKey
}

func threadObservedAt(thread contract.ThreadMetadata) time.Time {
	switch {
	case thread.FinishedAt != nil && *thread.FinishedAt > 0:
		return time.Unix(*thread.FinishedAt, 0)
	case thread.UpdatedAt > 0:
		return time.Unix(thread.UpdatedAt, 0)
	case thread.CreatedAt > 0:
		return time.Unix(thread.CreatedAt, 0)
	default:
		return time.Time{}
	}
}

func shouldAutoDreamScan(stamp consolidationStamp, now time.Time) bool {
	lastScan := stamp.lastScanTime()
	if lastScan.IsZero() {
		return true
	}
	if now.IsZero() {
		now = time.Now()
	}
	return now.Sub(lastScan) >= autoDreamScanThrottle
}
