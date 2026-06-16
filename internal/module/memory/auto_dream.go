package memory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// ErrConsolidationExtractFuncRequired reports a consolidation run without an extractor.
var ErrConsolidationExtractFuncRequired = errors.New("dream extract func is not configured")

// AutoDreamConsolidator coordinates periodic memory compaction into dream summaries.
type AutoDreamConsolidator struct {
	cfg       *Config
	extractor *MemoryExtractor
	extractFn ExtractFunc
	locks     *diskLockCoordinator
}

type consolidationRunOptions struct {
	cfg            *Config
	now            func() time.Time
	onLocked       func()
	runtimeContext string
}

type preparedConsolidation struct {
	root           string
	cfg            *Config
	now            func() time.Time
	extractFn      ExtractFunc
	guard          *consolidationLockGuard
	runtimeContext string
	locks          *diskLockCoordinator
}

// Consolidate 处理consolidate。
func (c *AutoDreamConsolidator) Consolidate(ctx context.Context, memoryRoot string, extractFn ExtractFunc) error {
	cfg, err := c.consolidationConfig(memoryRoot, nil)
	if err != nil {
		return err
	}
	return c.consolidateWithOptions(ctx, memoryRoot, extractFn, consolidationRunOptions{cfg: cfg})
}

func (c *AutoDreamConsolidator) consolidationConfig(path string, cfg *Config) (*Config, error) {
	if cfg != nil {
		return cfg, nil
	}
	if c != nil && c.cfg != nil {
		return c.cfg, nil
	}
	return nil, rejectConsolidationPath(nil, path)
}

// consolidateWithOptions 处理带选项的consolidate。
func (c *AutoDreamConsolidator) consolidateWithOptions(
	ctx context.Context,
	memoryRoot string,
	extractFn ExtractFunc,
	opts consolidationRunOptions,
) (err error) {
	run, err := c.prepareConsolidation(ctx, memoryRoot, extractFn, opts)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if cleanupErr := run.guard.Complete(committed); err == nil && cleanupErr != nil {
			err = cleanupErr
		}
	}()
	input, err := loadConsolidationPromptInput(run.root, run.cfg)
	if err != nil {
		return err
	}
	input.Limit = c.limit()
	if !shouldRunConsolidationExtract(input) {
		err = refreshConsolidationWithoutExtract(run.root, run.now, run.locks)
		committed = err == nil
		return err
	}
	err = c.runConsolidationExtract(ctx, run, input)
	committed = err == nil
	return err
}

func shouldRunConsolidationExtract(input consolidationPromptInput) bool {
	if len(consolidationCandidates(input.TopicEntries)) > 0 {
		return true
	}
	if len(input.LogDocuments) > 0 {
		return true
	}
	indexContent := strings.TrimSpace(input.Index.Content)
	return indexContent != "" && indexContent != "(missing)" && indexContent != "(empty)"
}

func refreshConsolidationWithoutExtract(root string, now func() time.Time, locks *diskLockCoordinator) error {
	if locks == nil {
		locks = newDiskLockCoordinator()
	}
	return locks.withDiskStoreLock(root, func() error {
		if _, err := UpdateMemoryIndex(root); err != nil {
			return err
		}
		return recordConsolidation(root, now())
	})
}

// prepareConsolidation 准备consolidation。
func (c *AutoDreamConsolidator) prepareConsolidation(
	ctx context.Context,
	memoryRoot string,
	extractFn ExtractFunc,
	opts consolidationRunOptions,
) (preparedConsolidation, error) {
	if err := contextErr(ctx); err != nil {
		return preparedConsolidation{}, err
	}
	root, err := normalizeStoreRoot(memoryRoot)
	if err != nil {
		return preparedConsolidation{}, err
	}
	if opts.cfg, err = c.consolidationConfig(root, opts.cfg); err != nil {
		return preparedConsolidation{}, err
	}
	if err := rejectConsolidationPath(opts.cfg, root); err != nil {
		return preparedConsolidation{}, err
	}
	extractFn = c.resolveExtractFunc(extractFn)
	if extractFn == nil {
		return preparedConsolidation{}, ErrConsolidationExtractFuncRequired
	}
	now := opts.now
	if now == nil {
		now = time.Now
	}
	guard, err := acquireConsolidationLock(root, consolidationLockOptions{Now: now})
	if err != nil {
		return preparedConsolidation{}, err
	}
	if opts.onLocked != nil {
		opts.onLocked()
	}
	return preparedConsolidation{root: root, cfg: opts.cfg, now: now, extractFn: extractFn, guard: guard, runtimeContext: opts.runtimeContext, locks: c.locks}, nil
}

// runConsolidationExtract 运行consolidationextract。
func (c *AutoDreamConsolidator) runConsolidationExtract(
	ctx context.Context,
	run preparedConsolidation,
	input consolidationPromptInput,
) error {
	promptText := appendConsolidationRuntimeContext(buildConsolidationPrompt(input), run.runtimeContext)
	raw, err := run.extractFn(ctx, promptText)
	if err != nil {
		return err
	}
	items, err := parseExtractedMemories(raw, input.Limit)
	if err != nil {
		return err
	}
	return run.locks.withDiskStoreLock(run.root, func() error {
		if err := removeMemoryFiles(run.root, staleMemoryPaths(input.TopicEntries)); err != nil {
			return err
		}
		if err := writeConsolidatedMemories(run.root, items); err != nil {
			return err
		}
		if _, err := UpdateMemoryIndex(run.root); err != nil {
			return err
		}
		return recordConsolidation(run.root, run.now())
	})
}

func (c *AutoDreamConsolidator) resolveExtractFunc(extractFn ExtractFunc) ExtractFunc {
	if extractFn != nil {
		return extractFn
	}
	if c == nil {
		return nil
	}
	return c.extractFn
}

func (c *AutoDreamConsolidator) limit() int {
	if c == nil || c.extractor == nil {
		return defaultExtractMaxItems
	}
	return c.extractor.limit()
}

// ---------------------------------------------------------------------------
// autoDreamScheduler (was auto_dream_scheduler.go)
// ---------------------------------------------------------------------------

// autoDreamSchedulerQueueCap bounds the in-memory queue of threadIDs waiting
// for auto-dream eligibility evaluation.
const autoDreamSchedulerQueueCap = 64

// autoDreamSchedulerDrainGrace is the shutdown budget for draining the
// queue + waiting for any in-flight dream task before RunnerModule shutdown.
const autoDreamSchedulerDrainGrace = 10 * time.Second

type autoDreamScheduler struct {
	hooks  *MemoryLifecycleHooks
	logger *pkglogger.Logger

	queue chan string

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}

	taskCtx    context.Context
	taskCancel context.CancelFunc

	droppedTotal   atomic.Int64
	processedTotal atomic.Int64
	scheduledTotal atomic.Int64
}

func newAutoDreamScheduler(hooks *MemoryLifecycleHooks, logger *pkglogger.Logger) *autoDreamScheduler {
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

// Start 启动记忆流程。
func (s *autoDreamScheduler) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		if s.hooks == nil || !s.hooks.enabled {
			close(s.doneCh)
			return
		}
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					pkglogger.Error("memory: recovered auto_dream_scheduler worker panic", "panic", rec)
				}
			}()
			s.runWorker()
		}()
	})
}

// Enqueue 把项目追加到队尾。
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

// Stop 停止记忆流程。
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
			waitCtx, cancel = kernel.WithTimeout(waitCtx, autoDreamSchedulerDrainGrace)
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

// DroppedTotal 处理droppedtotal。
func (s *autoDreamScheduler) DroppedTotal() int64 { return s.droppedTotal.Load() }

// ProcessedTotal 处理processedtotal。
func (s *autoDreamScheduler) ProcessedTotal() int64 { return s.processedTotal.Load() }

// ScheduledTotal 处理scheduledtotal。
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

// process 处理进程。
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
