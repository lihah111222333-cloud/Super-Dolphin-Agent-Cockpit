package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
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

// DreamTaskSnapshot 是 UI/RPC 查询后台 dream task 时暴露的只读状态。
// 它只复制当前锁内状态，不暴露 cancel/done 等生命周期控制句柄。
type DreamTaskSnapshot struct {
	Running        bool
	ThreadID       string
	Phase          string
	DroppedTotal   int64
	ProcessedTotal int64
	ScheduledTotal int64
	LastError      string
	LastAt         time.Time
	LastThreadID   string
}

type autoDreamHealthSnapshot struct {
	DroppedTotal   int64
	ProcessedTotal int64
	ScheduledTotal int64
	LastError      string
	LastAt         time.Time
	LastThreadID   string
}

// recordAutoDreamSchedulerHealth 合并 auto-dream 调度与执行的健康快照。
// 计数是单调递增值；错误和时间只在调用方给出非空值时覆盖，避免成功路径清掉最后失败线索。
func (h *MemoryLifecycleHooks) recordAutoDreamSchedulerHealth(snapshot autoDreamHealthSnapshot) {
	if h == nil {
		return
	}
	h.dreamMu.Lock()
	defer h.dreamMu.Unlock()
	if snapshot.DroppedTotal != 0 {
		h.dreamHealth.DroppedTotal = snapshot.DroppedTotal
	}
	if snapshot.ProcessedTotal != 0 {
		h.dreamHealth.ProcessedTotal = snapshot.ProcessedTotal
	}
	if snapshot.ScheduledTotal != 0 {
		h.dreamHealth.ScheduledTotal = snapshot.ScheduledTotal
	}
	if snapshot.LastError != "" {
		h.dreamHealth.LastError = snapshot.LastError
	}
	if !snapshot.LastAt.IsZero() {
		h.dreamHealth.LastAt = snapshot.LastAt
	}
	if snapshot.LastThreadID != "" {
		h.dreamHealth.LastThreadID = snapshot.LastThreadID
	}
}

// autoDreamHealthEmpty 判断 auto-dream 健康快照是否完全没有可展示状态。
// UI 空态依赖这个判断，新增健康字段时需要同步扩展这里。
func autoDreamHealthEmpty(snapshot autoDreamHealthSnapshot) bool {
	return snapshot.DroppedTotal == 0 &&
		snapshot.ProcessedTotal == 0 &&
		snapshot.ScheduledTotal == 0 &&
		snapshot.LastError == "" &&
		snapshot.LastThreadID == "" &&
		snapshot.LastAt.IsZero()
}

// ErrDreamTaskNotRunning 表示用户请求取消时当前没有可取消的 dream task。
var ErrDreamTaskNotRunning = errors.New("dream task is not running")

// GetDreamTaskStatus 返回当前后台 dream task 快照。
// 读取过程只持有短锁，不等待任务完成，适合状态轮询。
func (h *MemoryLifecycleHooks) GetDreamTaskStatus() DreamTaskSnapshot {
	return h.dreamTaskSnapshot()
}

// KillDreamTask 请求取消当前后台 dream task。
// 没有运行中任务时返回 ErrDreamTaskNotRunning，避免调用方误判取消成功。
func (h *MemoryLifecycleHooks) KillDreamTask() error {
	if !h.killDreamTask() {
		return ErrDreamTaskNotRunning
	}
	return nil
}

func (h *MemoryLifecycleHooks) startDreamTask(parent context.Context, threadID string) (context.Context, bool) {
	if h == nil {
		return nil, false
	}
	if parent == nil {
		parent = context.Background()
	}
	select {
	case <-parent.Done():
		return nil, false
	default:
	}
	h.dreamMu.Lock()
	defer h.dreamMu.Unlock()
	if h.dreamTask != nil {
		return nil, false
	}
	ctx, cancel := context.WithCancel(parent)
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
	snapshot := DreamTaskSnapshot{
		DroppedTotal:   h.dreamHealth.DroppedTotal,
		ProcessedTotal: h.dreamHealth.ProcessedTotal,
		ScheduledTotal: h.dreamHealth.ScheduledTotal,
		LastError:      h.dreamHealth.LastError,
		LastAt:         h.dreamHealth.LastAt,
		LastThreadID:   h.dreamHealth.LastThreadID,
	}
	if h.dreamTask == nil {
		return snapshot
	}
	snapshot.Running = true
	snapshot.ThreadID = h.dreamTask.threadID
	snapshot.Phase = h.dreamTask.phase
	return snapshot
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

// waitDreamTask 等待当前 dream task 结束或 ctx 取消。
// 它只读取一次 done channel；调用时没有任务则立即返回。
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

// registerAutoDreamSubscriptions 把 thread.stopped 事件接到唯一的 auto-dream scheduler。
// 订阅回调只做非阻塞 enqueue；真正的调度在 scheduler worker 中执行，关闭路径由 Stop 统一 drain。
func registerAutoDreamSubscriptions(p memorySubscriptionDeps, scheduler *autoDreamScheduler, appendCancel func(context.CancelFunc)) {
	if p.Hooks == nil || !p.Hooks.enabled || scheduler == nil {
		return
	}
	appendCancel(contract.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Stopped) {
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
	taskCtx, started := h.startDreamTask(ctx, threadID)
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

// prepareAutoDreamExecution 组装一次 auto-dream 运行计划。
// 它会解析 memory root、检查扫描节流和最小会话数，并在 extract 函数缺失时 fail-fast。
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

// prepareAutoDreamWindow 判断本次 auto-dream 是否进入扫描窗口。
// 函数会先记录 scan 时间，再用上次成功时间做最小间隔判断，避免多线程同时触发重复 consolidation。
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
	// SafeGo 包装 panic recovery 和结构化日志，避免后台 consolidation panic 直接打崩进程。
	safego.Go(taskCtx, h.logger, "memory.autoDream.task", func(ctx context.Context) {
		defer h.finishDreamTask()
		err := h.consolidator.consolidateWithOptions(ctx, plan.root, plan.extractFn, consolidationRunOptions{
			cfg:            h.cfg,
			now:            h.now,
			runtimeContext: buildConsolidationRuntimeContext("background auto-dream stop hook", plan.sessionCount, plan.lastSuccess, threadID),
			onLocked: func() {
				h.setDreamTaskPhase(dreamTaskPhaseUpdating)
			},
		})
		if err != nil {
			if h.logger != nil && !errors.Is(err, context.Canceled) {
				h.logger.Error("memory auto-dream execution failed", "thread_id", threadID, "error", err)
			}
			if !errors.Is(err, context.Canceled) {
				h.recordAutoDreamSchedulerHealth(autoDreamHealthSnapshot{
					LastError:    err.Error(),
					LastAt:       h.now(),
					LastThreadID: threadID,
				})
			}
			return
		}
		// Consolidation rewrote MEMORY.md; flush the prompt cache so the
		// next AssembleStart picks up the consolidated index.
		h.invalidateMemorySections()
	})
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

// autoDreamSessionCount 统计同项目内上次成功 consolidation 之后的可计数线程。
// thread store 缺失时返回 0，表示当前运行环境无法触发 auto-dream，而不是静默写入。
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

// shouldCountAutoDreamThread 判断某个线程是否计入 auto-dream 触发窗口。
// 当前线程、agent memory 线程、不同项目线程或早于 since 的线程都不计数。
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

type uiSimilarityConsolidateAllStatusParams struct {
	CWD   string `json:"cwd,omitempty"`
	JobID string `json:"jobId"`
}

type uiSimilarityConsolidateAllStartResult struct {
	JobID  string                            `json:"jobId"`
	Status string                            `json:"status"`
	Result *uiSimilarityConsolidateAllResult `json:"result,omitempty"`
	Error  string                            `json:"error,omitempty"`
}

type uiSimilarityConsolidateAllStatusResult = uiSimilarityConsolidateAllStartResult

func startConsolidateAllHandler(_ context.Context, p memoryHandlerDeps, req uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllStartResult, error) {
	if p.Service == nil {
		return uiSimilarityConsolidateAllStartResult{}, errors.New("memory service is not configured")
	}
	return uiMemoryConsolidationJobs().start(p, req)
}

func statusConsolidateAllHandler(_ context.Context, p memoryHandlerDeps, req uiSimilarityConsolidateAllStatusParams) (uiSimilarityConsolidateAllStatusResult, error) {
	if p.Service == nil {
		return uiSimilarityConsolidateAllStatusResult{}, errors.New("memory service is not configured")
	}
	return uiMemoryConsolidationJobs().status(req)
}

const (
	uiMemoryConsolidationStatusRunning   = "running"
	uiMemoryConsolidationStatusSucceeded = "succeeded"
)

type uiMemoryConsolidationRunner func(context.Context, memoryHandlerDeps, uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllResult, error)

type uiMemoryConsolidationJob struct {
	id          string
	cwdKey      string
	status      string
	result      *uiSimilarityConsolidateAllResult
	err         string
	completedAt time.Time
}

type uiMemoryConsolidationJobStore struct {
	mu      sync.Mutex
	jobs    map[string]*uiMemoryConsolidationJob
	running map[string]string
	run     uiMemoryConsolidationRunner
	timeout time.Duration
	ttl     time.Duration
	now     func() time.Time
}

var uiMemoryConsolidationJobs = sync.OnceValue(func() *uiMemoryConsolidationJobStore {
	return newUIMemoryConsolidationJobStore(runConsolidateAll, ctxutil.DreamConsolidationTimeout)
})

func newUIMemoryConsolidationJobStore(run uiMemoryConsolidationRunner, timeout time.Duration) *uiMemoryConsolidationJobStore {
	if timeout <= 0 {
		timeout = ctxutil.DreamConsolidationTimeout
	}
	return &uiMemoryConsolidationJobStore{
		jobs:    make(map[string]*uiMemoryConsolidationJob),
		running: make(map[string]string),
		run:     run,
		timeout: timeout,
		ttl:     10 * time.Minute,
		now:     time.Now,
	}
}

func (s *uiMemoryConsolidationJobStore) start(deps memoryHandlerDeps, req uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllStartResult, error) {
	if s == nil || s.run == nil {
		return uiSimilarityConsolidateAllStartResult{}, errors.New("memory consolidation runner is not configured")
	}
	cwdKey := uiMemoryConsolidationCWDKey(deps, req.CWD)
	now := s.now()

	s.mu.Lock()
	s.pruneLocked(now)
	if runningID := s.running[cwdKey]; runningID != "" {
		out := s.snapshotLocked(s.jobs[runningID])
		s.mu.Unlock()
		return out, nil
	}
	jobID := fmt.Sprintf("memory-consolidate-%d-%d", now.UnixNano(), len(s.jobs)+1)
	job := &uiMemoryConsolidationJob{id: jobID, cwdKey: cwdKey, status: uiMemoryConsolidationStatusRunning}
	s.jobs[jobID] = job
	s.running[cwdKey] = jobID
	out := s.snapshotLocked(job)
	s.mu.Unlock()

	safego.Go(context.Background(), nil, "memory.ui_consolidation.job", func(context.Context) {
		s.runJob(jobID, deps, req)
	})
	return out, nil
}

// status 返回指定 UI consolidation job 的当前快照，并按 cwdKey 隔离不同工作区查询。
func (s *uiMemoryConsolidationJobStore) status(req uiSimilarityConsolidateAllStatusParams) (uiSimilarityConsolidateAllStatusResult, error) {
	if s == nil {
		return uiSimilarityConsolidateAllStatusResult{}, errors.New("memory consolidation job store is not configured")
	}
	jobID := strings.TrimSpace(req.JobID)
	if jobID == "" {
		return uiSimilarityConsolidateAllStatusResult{}, publicValidationErr("jobId is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	job := s.jobs[jobID]
	if job == nil {
		return uiSimilarityConsolidateAllStatusResult{}, publicValidationErr("memory consolidation job not found")
	}
	if cwdKey := strings.TrimSpace(req.CWD); cwdKey != "" && cwdKey != job.cwdKey {
		return uiSimilarityConsolidateAllStatusResult{}, publicValidationErr("memory consolidation job not found")
	}
	return s.snapshotLocked(job), nil
}

func (s *uiMemoryConsolidationJobStore) runJob(jobID string, deps memoryHandlerDeps, req uiSimilarityConsolidateAllParams) {
	ctx, cancel := ctxutil.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	result, err := s.run(ctx, deps, req)
	s.finishJob(jobID, deps, result, err)
}

func (s *uiMemoryConsolidationJobStore) finishJob(jobID string, deps memoryHandlerDeps, result uiSimilarityConsolidateAllResult, err error) {
	if err == nil {
		publishUIMemoryChanged(deps, "consolidate-similarities")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.jobs[jobID]
	if job == nil {
		return
	}
	job.completedAt = s.now()
	if err != nil {
		job.status = "failed"
		job.err = err.Error()
	} else {
		job.status = uiMemoryConsolidationStatusSucceeded
		job.result = &result
	}
	delete(s.running, job.cwdKey)
}

func (s *uiMemoryConsolidationJobStore) snapshotLocked(job *uiMemoryConsolidationJob) uiSimilarityConsolidateAllStatusResult {
	if job == nil {
		return uiSimilarityConsolidateAllStatusResult{}
	}
	out := uiSimilarityConsolidateAllStatusResult{JobID: job.id, Status: job.status, Error: job.err}
	if job.result != nil {
		copyResult := *job.result
		out.Result = &copyResult
	}
	return out
}

// pruneLocked 在持锁状态下删除过期 UI consolidation job。
// 调用方必须已持有 job store 锁，避免 jobs 与 running 两张索引出现不一致。
func (s *uiMemoryConsolidationJobStore) pruneLocked(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	for id, job := range s.jobs {
		if job == nil || job.completedAt.IsZero() || now.Sub(job.completedAt) <= s.ttl {
			continue
		}
		delete(s.jobs, id)
		if s.running[job.cwdKey] == id {
			delete(s.running, job.cwdKey)
		}
	}
}

func uiMemoryConsolidationCWDKey(deps memoryHandlerDeps, cwd string) string {
	if trimmed := strings.TrimSpace(cwd); trimmed != "" {
		return trimmed
	}
	if deps.Service != nil {
		if projectRoot := strings.TrimSpace(deps.Service.Config().ProjectRoot); projectRoot != "" {
			return projectRoot
		}
	}
	return "default"
}
