package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// ErrConsolidationExtractFuncRequired 表示 consolidation 入口没有可用的 LLM extract 函数。
var ErrConsolidationExtractFuncRequired = errors.New("dream extract func is not configured")

// AutoDreamConsolidator 负责把 durable memory 的索引、主题文件和日志合并成稳定记忆。
// 它通过磁盘锁串行化写入，避免并发 consolidation 互相覆盖 MEMORY.md 或 topic 文件。
type AutoDreamConsolidator struct {
	cfg       *Config
	extractor *MemoryExtractor
	extractFn ExtractFunc
	locks     *diskLockCoordinator
}

// consolidationRunOptions 收纳一次 consolidation 的可测试选项。
// now、onLocked 和 runtimeContext 只影响时间戳、测试同步点和 prompt 背景，不改变写入边界。
type consolidationRunOptions struct {
	cfg            *Config
	now            func() time.Time
	onLocked       func()
	runtimeContext string
}

// preparedConsolidation 是进入 extract 前已完成校验和加锁的运行上下文。
// guard 必须由调用方在成功提交或失败回滚时完成，确保锁文件生命周期闭合。
type preparedConsolidation struct {
	root           string
	cfg            *Config
	now            func() time.Time
	extractFn      ExtractFunc
	guard          *consolidationLockGuard
	runtimeContext string
	locks          *diskLockCoordinator
}

// Consolidate 对指定 memoryRoot 执行一次手动 consolidation。
// 路径会先经过 store root 规范化和 agent-memory 拒绝检查，缺少 extract 函数时直接返回错误。
func (c *AutoDreamConsolidator) Consolidate(ctx context.Context, memoryRoot string, extractFn ExtractFunc) error {
	cfg, err := c.consolidationConfig(memoryRoot, nil)
	if err != nil {
		return err
	}
	return c.consolidateWithOptions(ctx, memoryRoot, extractFn, consolidationRunOptions{cfg: cfg})
}

// consolidationConfig 选择本次 consolidation 使用的 Config。
// 显式 cfg 优先，其次使用 consolidator 注入配置；两者缺失时沿用路径拒绝逻辑返回配置错误。
func (c *AutoDreamConsolidator) consolidationConfig(path string, cfg *Config) (*Config, error) {
	if cfg != nil {
		return cfg, nil
	}
	if c != nil && c.cfg != nil {
		return c.cfg, nil
	}
	return nil, rejectConsolidationPath(nil, path)
}

// consolidateWithOptions 执行带测试钩子的 consolidation 主流程。
// 它负责获取锁、加载输入、决定是否调用 extract，并在提交失败时回滚锁文件时间戳。
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

// shouldRunConsolidationExtract 判断当前输入是否值得调用 LLM extract。
// 没有主题、日志和有效索引内容时只刷新索引时间戳，避免空输入触发无意义写入。
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

// refreshConsolidationWithoutExtract 在无可合并内容时只刷新索引和 consolidation 记录。
// 写入仍走磁盘锁，保证与正常 extract 路径的并发保护一致。
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

// prepareConsolidation 校验 root、配置和 extract 函数，并获取 consolidation 锁。
// 成功返回后调用方必须调用 guard.Complete，否则旧锁时间戳无法恢复或释放。
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

// runConsolidationExtract 调用 extract 函数生成记忆，并在同一个磁盘锁内删除旧文件、写新文件和刷新索引。
// 任一步失败都会向上返回错误，让外层 guard 回滚本次 consolidation 标记。
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

// resolveExtractFunc 选择调用方显式传入或 consolidator 注入的 extract 函数。
// nil 返回表示装配缺失，调用方需要 fail-fast。
func (c *AutoDreamConsolidator) resolveExtractFunc(extractFn ExtractFunc) ExtractFunc {
	if extractFn != nil {
		return extractFn
	}
	if c == nil {
		return nil
	}
	return c.extractFn
}

// limit 返回本次 extract 允许产出的最大记忆条数。
// extractor 缺失时使用默认上限，避免配置不完整导致无限制生成。
func (c *AutoDreamConsolidator) limit() int {
	if c == nil || c.extractor == nil {
		return defaultExtractMaxItems
	}
	return c.extractor.limit()
}

// ----- Auto Dream 调度器 -----

// autoDreamSchedulerQueueCap 限制等待 auto-dream eligibility 检查的 threadID 队列长度。
const autoDreamSchedulerQueueCap = 64

// autoDreamSchedulerDrainGrace 是关闭时排空队列并等待当前 dream task 的最长时间。
const autoDreamSchedulerDrainGrace = 10 * time.Second

// autoDreamScheduler 串行消费 thread.stopped 信号并决定是否启动 auto-dream。
// 队列满时按 threadID 合并到 pending，Stop 会取消 taskCtx 并等待 worker 退出。
type autoDreamScheduler struct {
	hooks  *MemoryLifecycleHooks
	logger *slog.Logger

	queue     chan string
	pendingMu sync.Mutex
	pending   map[string]struct{}

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

// newAutoDreamScheduler 创建 auto-dream 调度器并绑定独立 task context。
// logger 缺失时使用包默认 logger，避免后台 worker panic 或丢弃事件时没有审计日志。
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

// Start 启动单个 auto-dream worker。
// hooks 未启用时直接关闭 doneCh，让 Stop 不会阻塞等待未启动的 goroutine。
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

// Enqueue 非阻塞地把 threadID 放入待检查队列。
// 空 threadID 和已停止调度器会丢弃；满队列会合并到 pending，避免总线回调被后台 consolidation 阻塞。
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
		s.coalesce(threadID)
		if s.logger != nil {
			s.logger.Warn("memory auto-dream scheduler: queue full, coalescing enqueue",
				"thread_id", threadID, "cap", autoDreamSchedulerQueueCap)
		}
	}
}

// Stop 关闭调度器并等待 worker 或正在执行的 dream task 退出。
// 传入 ctx 没有短 deadline 时会套用 drain grace，防止 RunnerModule 关闭无限等待。
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
			waitCtx, cancel = ctxutil.WithTimeout(waitCtx, autoDreamSchedulerDrainGrace)
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

// DroppedTotal 返回因停止或队列满而丢弃的 threadID 数量。
func (s *autoDreamScheduler) DroppedTotal() int64 { return s.droppedTotal.Load() }

// ProcessedTotal 返回 worker 已消费并尝试调度的 threadID 数量。
func (s *autoDreamScheduler) ProcessedTotal() int64 { return s.processedTotal.Load() }

// ScheduledTotal 返回实际启动 auto-dream task 的次数。
func (s *autoDreamScheduler) ScheduledTotal() int64 { return s.scheduledTotal.Load() }

func (s *autoDreamScheduler) coalesce(threadID string) {
	s.pendingMu.Lock()
	if s.pending == nil {
		s.pending = make(map[string]struct{})
	}
	s.pending[threadID] = struct{}{}
	s.pendingMu.Unlock()
}

func (s *autoDreamScheduler) takeCoalesced() (string, bool) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for threadID := range s.pending {
		delete(s.pending, threadID)
		return threadID, true
	}
	return "", false
}

// runWorker 串行处理队列，直到 Stop 关闭 stopCh。
// 它是唯一读取 queue 的 goroutine，保证 maybeScheduleAutoDream 不会并发启动多个 dream task。
func (s *autoDreamScheduler) runWorker() {
	defer close(s.doneCh)
	for {
		if threadID, ok := s.takeCoalesced(); ok {
			s.process(threadID)
			continue
		}
		select {
		case <-s.stopCh:
			return
		case threadID := <-s.queue:
			s.process(threadID)
		}
	}
}

// process 对单个 threadID 执行 eligibility 检查和调度。
// context.Canceled 属于关闭路径，不记录为失败；其他错误写 warning 但不终止 worker。
func (s *autoDreamScheduler) process(threadID string) {
	if strings.TrimSpace(threadID) == "" {
		return
	}
	s.processedTotal.Add(1)
	scheduled, err := s.hooks.maybeScheduleAutoDream(s.taskCtx, threadID)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.publishHealth(threadID, err)
		if s.logger != nil {
			s.logger.Warn("memory auto-dream scheduler: schedule failed",
				"thread_id", threadID, "error", err)
		}
		return
	}
	if scheduled {
		s.scheduledTotal.Add(1)
	}
	s.publishHealth(threadID, nil)
}

func (s *autoDreamScheduler) publishHealth(threadID string, err error) {
	if s == nil || s.hooks == nil {
		return
	}
	snapshot := autoDreamHealthSnapshot{
		DroppedTotal:   s.DroppedTotal(),
		ProcessedTotal: s.ProcessedTotal(),
		ScheduledTotal: s.ScheduledTotal(),
		LastAt:         time.Now().UTC(),
		LastThreadID:   strings.TrimSpace(threadID),
	}
	if err != nil {
		snapshot.LastError = err.Error()
	}
	s.hooks.recordAutoDreamSchedulerHealth(snapshot)
}
