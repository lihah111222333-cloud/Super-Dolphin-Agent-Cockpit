package thread

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idempotency"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/identifier"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	statusArchived = "archived"
	statusCreated  = "created"
	statusFailed   = "failed"
	statusStopped  = "stopped"
)

const maxThreadListLimit = 200

var errLocalSessionAlreadyGone = errors.New("thread local session already gone")

// ListPageRequest 是 thread 列表 keyset 分页请求。
type ListPageRequest struct {
	Limit           int
	CursorCreatedAt int64
	CursorThreadID  string
}

// ListPageResult 是 thread 列表 keyset 分页响应。
type ListPageResult struct {
	Threads             []Ref  `json:"threads"`
	HasMore             bool   `json:"has_more"`
	NextCursorCreatedAt int64  `json:"next_cursor_created_at,omitempty"`
	NextCursorThreadID  string `json:"next_cursor_thread_id,omitempty"`
}

// SessionProvider 复用跨模块会话查询接口，保留本包旧构造函数的类型签名兼容。
type SessionProvider = contract.SessionProvider

// sessionGenerationRemover 用于按 generation 精确移除已停止的 session，避免误删新建 session。
type sessionGenerationRemover interface {
	RemoveSessionGeneration(agentID string, generation uint64)
}

type service struct {
	logger                  *slog.Logger
	threadStore             ThreadStore
	archiveStateStore       ArchiveStateStore
	bindingStore            BindingStore
	sessions                SessionProvider
	starter                 SessionStarter
	promptAssembly          contract.PromptAssemblyService
	cfg                     *contract.Config
	toolRegistry            contract.ToolRegistry
	mcpServers              contract.MCPServerConfigProvider
	turns                   contract.TurnThreadCleaner
	orchestration           OrchestrationFacade
	sessionGenerationBinder SessionGenerationBinder
	scratchpadCleanup       func(string) error
	tracing                 *platformobs.Service
	bus                     *event.Dispatcher

	emitStarted      func(threaddto.Started)
	emitStopped      func(threaddto.Stopped)
	emitUpdated      func(threaddto.Updated)
	emitMessagesPage func(threaddto.MessagesPage)
	emitCompacted    func(threaddto.Compacted)
	emitLaunched     func(threaddto.Launched)

	// pendingLaunchMu 按 thread_id 串行化 SpawnIfNeeded，避免 pending 线程的并发首轮请求 fork 多个 CLI。
	pendingLaunchMu sync.Map // key: threadID(string), value: *sync.Mutex

	// agentIDMu 保护进程内 agent_id 预留窗口；thread/start 持久化 agent_threads 前靠它拦截重复启动。
	agentIDMu           sync.Mutex
	agentIDReservations map[string]struct{}

	launchIntentRegistry idempotency.Registry[StartResult]
	launchIntentByThread sync.Map

	threadAgentsMu sync.RWMutex
	threadAgents   map[string]string

	resumeInFlight, resumeBlocked, sessionRecoveryCount sync.Map

	// promptCatalog 是运行时读路径，会合并内置模板和数据库模板。
	promptCatalog PromptCatalog

	// matchWhenEval 用当前 BuildCtx 评估 prompt_template.match_when。
	// 由构造层注入以避免 thread 直接依赖 prompt 包；为 nil 时自动路由会跳过表达式评估。
	matchWhenEval contract.MatchWhenEvaluator

	// enableWhenEval 复用 assembler 的 enable_when 规则，保证路由 snapshot 与最终注入结果一致。
	enableWhenEval contract.EnableWhenEvaluator

	reconnectDelay time.Duration

	// agentLaunchedWorker 串行承接 onAgentLaunched 后的 binding 写入和 prompt
	// 失效慢路径。worker 始终构造，真正写库前再检查 bindingStore，保证精简装配仍可运行。
	agentLaunchedWorker *agentLaunchedWorker

	// sessionRecoveryWorker 承接 onAgentFailed 后的会话恢复慢路径。
	// 它统一负责重试计数、清理僵尸 session、等待 provider 关闭窗口和后台恢复，
	// Stop 时可取消等待并收束正在运行的恢复 goroutine。
	sessionRecoveryWorker *sessionRecoveryWorker
}

var _ Service = (*service)(nil)

// invalidatePromptAssembly 触发 prompt assembly 缓存失效，nil 时安全跳过。
func (s *service) invalidatePromptAssembly(ctx context.Context, reason contract.InvalidateReason) error {
	if s == nil || s.promptAssembly == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	return s.promptAssembly.Invalidate(ctx, reason)
}

// List 返回最多 200 条线程引用，保留旧 no-arg RPC 的兼容硬上限。
func (s *service) List(ctx context.Context) ([]Ref, error) {
	page, err := s.ListPage(ctx, ListPageRequest{Limit: maxThreadListLimit})
	if err != nil {
		return nil, err
	}
	return page.Threads, nil
}

// ListPage 返回按 created_at/thread_id keyset 分页的线程引用。
func (s *service) ListPage(ctx context.Context, req ListPageRequest) (ListPageResult, error) {
	params, err := normalizeThreadListPage(req)
	if err != nil {
		return ListPageResult{}, err
	}
	if s.threadStore == nil {
		return ListPageResult{}, errors.New("thread store is not configured")
	}
	store, ok := s.threadStore.(ThreadPageReader)
	if !ok {
		return ListPageResult{}, errors.New("thread store page query is not configured")
	}
	page, err := store.ListPage(ctx, params)
	if err != nil {
		return ListPageResult{}, err
	}
	return toListPageResult(page), nil
}

// ListLoadedPage 返回已加载线程的有限列表页，状态过滤必须在 SQL 层完成。
func (s *service) ListLoadedPage(ctx context.Context, req ListPageRequest) (ListPageResult, error) {
	params, err := normalizeThreadListPage(req)
	if err != nil {
		return ListPageResult{}, err
	}
	if s.threadStore == nil {
		return ListPageResult{}, errors.New("thread store is not configured")
	}
	store, ok := s.threadStore.(LoadedThreadPageReader)
	if !ok {
		return ListPageResult{}, errors.New("thread loaded page query is not configured")
	}
	page, err := store.ListLoadedPage(ctx, params)
	if err != nil {
		return ListPageResult{}, err
	}
	return toListPageResult(page), nil
}

// CountActive 通过 store 聚合查询统计活跃 agent 数量。
func (s *service) CountActive(ctx context.Context) (int64, error) {
	if s.threadStore == nil {
		return 0, errors.New("thread store is not configured")
	}
	counter, ok := s.threadStore.(ActiveThreadCounter)
	if !ok {
		return 0, errors.New("thread active count query is not configured")
	}
	return counter.CountActive(ctx)
}

// Get 按 thread id 读取单个线程，并补齐 binding 中的 provider 身份。
func (s *service) Get(ctx context.Context, id string) (*Ref, error) {
	thread, err := s.getThread(ctx, id)
	if err != nil {
		return nil, err
	}
	ref := toRef(*thread)
	if err := s.enrichRefIdentity(ctx, &ref); err != nil {
		return nil, err
	}
	return &ref, nil
}

// ListByStatus 按持久化状态过滤线程；空状态表示返回全部。
func (s *service) ListByStatus(ctx context.Context, status string) ([]Ref, error) {
	want := strings.TrimSpace(status)
	if want == "" {
		return s.List(ctx)
	}
	if strings.EqualFold(want, statusCreated) {
		page, err := s.ListLoadedPage(ctx, ListPageRequest{Limit: maxThreadListLimit})
		if err != nil {
			return nil, err
		}
		return page.Threads, nil
	}
	return s.listThreads(ctx, func(thread threadStoreRecord) bool {
		return strings.EqualFold(strings.TrimSpace(thread.Status), want)
	})
}

// ListByCWD 按 CWD 前缀过滤线程，供 UI 缩小当前项目线程列表。
func (s *service) ListByCWD(ctx context.Context, cwdPrefix string) ([]Ref, error) {
	prefix := strings.TrimSpace(cwdPrefix)
	return s.listThreads(ctx, func(thread threadStoreRecord) bool {
		return prefix == "" || strings.HasPrefix(strings.TrimSpace(thread.Cwd), prefix)
	})
}

// SetName 更新本地线程名，并在 provider 支持时同步远端会话标题。
// 本地持久化先完成；远端同步失败会返回错误，避免 UI 误以为两端已一致。
func (s *service) SetName(ctx context.Context, threadID, name string) error {
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	thread.Name = name
	thread.Prompt = name
	thread.ManuallyRenamed = true
	thread.UpdatedAt = time.Now().UnixMilli()
	if err := s.upsertThread(ctx, *thread); err != nil {
		return err
	}

	if err := s.syncProviderThreadName(ctx, threadID, thread.AgentID, name); err != nil {
		return err
	}

	if s.emitUpdated != nil {
		s.emitUpdated(threaddto.Updated{
			EventHeader: shareddto.EventHeader{Timestamp: time.Now()},
			ThreadID:    threadID,
			Name:        name,
		})
	}
	return nil
}

// Delete 删除线程的本地状态、binding、scratchpad 和关联 runtime。
// pending_launch 线程没有真实 provider 进程，走单独路径清理并发布停止事件。
func (s *service) Delete(ctx context.Context, threadID string) error {
	ctx = util.NonNilContext(ctx)
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return err
	}
	binding, handled, err := s.resolveDeleteBinding(ctx, id)
	if handled || err != nil {
		return err
	}
	if handled, err := s.deletePendingLaunchThread(ctx, id, binding); handled || err != nil {
		return err
	}
	stopState := newThreadStopState(binding, id)
	releaseResume, err := s.deleteThreadRuntime(ctx, stopState, binding)
	if err != nil {
		return err
	}
	if releaseResume != nil {
		defer releaseResume()
	}
	if err := s.deleteThreadBinding(ctx, binding); err != nil {
		return err
	}
	return s.deleteThreadState(ctx, id, stopState, binding)
}

// resolveDeleteBinding 解析删除操作所需的 binding；处理 pending_launch 线程的特殊路径。
func (s *service) resolveDeleteBinding(
	ctx context.Context,
	threadID string,
) (*bindingStoreRecord, bool, error) {
	if s.bindingStore == nil {
		if handled, pendingErr := s.deletePendingLaunchThread(ctx, threadID, nil); handled || pendingErr != nil {
			return nil, handled, pendingErr
		}
		return nil, false, errors.New("thread: binding store is not configured")
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err == nil {
		return binding, false, nil
	}
	if handled, pendingErr := s.deletePendingLaunchThread(ctx, threadID, nil); handled || pendingErr != nil {
		return nil, handled, pendingErr
	}
	binding, err = s.resolveBinding(ctx, threadID)
	return binding, false, err
}

// deletePendingLaunchThread 删除尚未 fork provider 的 pending_launch 线程。
// 它在 per-thread 锁内完成 store 删除和 launch intent 关闭，避免首轮输入并发拉起已删除线程。
func (s *service) deletePendingLaunchThread(
	ctx context.Context,
	threadID string,
	binding *bindingStoreRecord,
) (bool, error) {
	if binding != nil {
		return false, nil
	}
	if s.threadStore == nil {
		return false, nil
	}
	id := strings.TrimSpace(threadID)
	if id == "" {
		return false, nil
	}
	mu := s.acquirePendingLaunchLock(id)
	mu.Lock()
	defer mu.Unlock()
	pendingLaunch, err := s.isThreadPendingLaunch(ctx, id)
	if err != nil {
		return false, err
	}
	if !pendingLaunch {
		return false, nil
	}
	if err := s.threadStore.DeleteByThreadID(ctx, id); err != nil {
		return true, err
	}
	s.CompleteLaunchIntent(ctx, id)
	s.publishThreadStopped(id, "", "deleted", "deleted_pending_launch")
	return true, nil
}

// deleteThreadRuntime 停止线程关联的 provider session 和 orchestration agent。
func (s *service) deleteThreadRuntime(
	ctx context.Context,
	stopState threadStopState,
	binding *bindingStoreRecord,
) (func(), error) {
	if binding == nil {
		return nil, nil
	}
	return s.stopThreadRuntime(ctx, stopState, "thread_deleted", true)
}

// deleteThreadBinding 从 binding store 删除 agent 关联记录。
func (s *service) deleteThreadBinding(ctx context.Context, binding *bindingStoreRecord) error {
	if s.bindingStore == nil || binding == nil {
		return nil
	}
	return s.bindingStore.DeleteByAgentID(ctx, strings.TrimSpace(binding.AgentID))
}

// deleteThreadState 删除 thread store 记录、清理 scratchpad 并发布停止事件。
func (s *service) deleteThreadState(
	ctx context.Context,
	threadID string,
	stopState threadStopState,
	binding *bindingStoreRecord,
) error {
	cleanupErr := s.cleanupThreadScratchpad(ctx, threadID, binding)
	s.forgetThreadAgents(stopState.targets...)
	if s.threadStore == nil {
		return joinScratchpadPartialCleanupError("delete", errors.New("thread store is not configured"), cleanupErr)
	}
	if err := s.threadStore.DeleteByThreadID(ctx, stopState.stoppedID); err != nil {
		return joinScratchpadPartialCleanupError("delete", err, cleanupErr)
	}
	turnCleanupErr := s.cleanupThreadTurns(ctx, "thread_deleted", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, agentIDFromBinding(binding, stopState.stoppedID), "deleted", "deleted")
	return newLifecyclePartialCleanupError("delete", errors.Join(cleanupErr, turnCleanupErr))
}

// forgetThreadAgents 批量移除进程内 threadID→agentID 映射缓存。
func (s *service) forgetThreadAgents(threadIDs ...string) {
	for _, threadID := range threadIDs {
		s.forgetThreadAgent(threadID)
	}
}

// listThreads 从 thread store 读取列表并应用可选过滤器。
// 过滤只影响返回结果，不改变 store 状态；store 未装配时直接报错。
func (s *service) listThreads(ctx context.Context, filter func(threadStoreRecord) bool) ([]Ref, error) {
	if s.threadStore == nil {
		return nil, errors.New("thread store is not configured")
	}
	threads, err := s.threadStore.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Ref, 0, len(threads))
	for _, thread := range threads {
		if filter != nil && !filter(thread) {
			continue
		}
		result = append(result, toRef(thread))
	}
	return result, nil
}

func normalizeThreadListPage(req ListPageRequest) (contract.ThreadListPageParams, error) {
	if req.Limit <= 0 {
		return contract.ThreadListPageParams{}, errors.New("thread list limit is required")
	}
	if req.Limit > maxThreadListLimit {
		return contract.ThreadListPageParams{}, fmt.Errorf("thread list limit exceeds maximum: %d > %d", req.Limit, maxThreadListLimit)
	}
	cursorThreadID := strings.TrimSpace(req.CursorThreadID)
	if req.CursorCreatedAt != 0 && cursorThreadID == "" {
		return contract.ThreadListPageParams{}, errors.New("thread list cursor_thread_id is required when cursor_created_at is set")
	}
	return contract.ThreadListPageParams{
		Limit:           req.Limit,
		CursorCreatedAt: req.CursorCreatedAt,
		CursorThreadID:  cursorThreadID,
	}, nil
}

func toListPageResult(page contract.ThreadListPage) ListPageResult {
	refs := make([]Ref, 0, len(page.Threads))
	for _, thread := range page.Threads {
		refs = append(refs, refFromThreadListRecord(thread))
	}
	return ListPageResult{
		Threads:             refs,
		HasMore:             page.HasMore,
		NextCursorCreatedAt: page.NextCursorCreatedAt,
		NextCursorThreadID:  page.NextCursorThreadID,
	}
}

func refFromThreadListRecord(thread contract.ThreadListRecord) Ref {
	return Ref{
		ID:        thread.ThreadID,
		Name:      thread.Name,
		AgentID:   thread.AgentID,
		Status:    thread.Status,
		CreatedAt: thread.CreatedAt,
		UpdatedAt: thread.UpdatedAt,
		CWD:       thread.Cwd,
		Model:     thread.Model,
		Port:      int(thread.Port),
	}
}

// getThread 按 threadID 从 store 读取单条记录。
func (s *service) getThread(ctx context.Context, threadID string) (*threadStoreRecord, error) {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return nil, err
	}
	if s.threadStore == nil {
		return nil, errors.New("thread store is not configured")
	}
	return s.threadStore.GetByThreadID(ctx, id)
}

// upsertThread 将线程记录写入 store，线程 store 未配置时报错。
func (s *service) upsertThread(ctx context.Context, thread threadStoreRecord) error {
	if s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	return s.threadStore.Upsert(ctx, newThreadUpsertParams(thread))
}

// updateThreadStatus 更新指定线程的 status 字段。
func (s *service) updateThreadStatus(ctx context.Context, threadID, status string) error {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return err
	}
	if s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	return s.threadStore.UpdateStatus(ctx, threadStoreStatusUpdate{
		ThreadID:  id,
		Status:    strings.TrimSpace(status),
		UpdatedAt: time.Now().UnixMilli(),
	})
}

// resolveBinding 按 threadID 解析 binding，通过多路径回退查找对应 agent。
func (s *service) resolveBinding(ctx context.Context, threadID string) (*bindingStoreRecord, error) {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return nil, err
	}
	if s.bindingStore == nil {
		return nil, errors.New("binding store is not configured")
	}
	return s.resolveBindingChain(ctx, id)
}

// resolveSession 按 threadID 解析 binding 并从 session provider 获取 session。
func (s *service) resolveSession(ctx context.Context, threadID string) (contract.Session, *bindingStoreRecord, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if s.sessions == nil {
		return nil, binding, errors.New("session provider is not configured")
	}
	session, err := s.sessions.GetSession(strings.TrimSpace(binding.AgentID))
	if err != nil {
		return nil, binding, err
	}
	return session, binding, nil
}

// enrichRefIdentity 从 binding 补齐线程引用的 provider/session 身份。
// binding 缺失不会让读取失败，因为列表页仍需要展示 thread store 中的存量记录。
func (s *service) enrichRefIdentity(ctx context.Context, ref *Ref) error {
	if s == nil || s.bindingStore == nil || ref == nil {
		return nil
	}
	binding, err := s.resolveBinding(ctx, ref.ID)
	if err != nil {
		return err
	}
	if binding == nil {
		return nil
	}
	return applyBindingIdentity(ref, binding)
}

// applyBindingIdentity 把单条 binding 的已验证身份回填到线程引用。
func applyBindingIdentity(ref *Ref, binding *bindingStoreRecord) error {
	if provider := strings.TrimSpace(binding.Provider); provider != "" {
		ref.Provider = provider
	}
	providerThreadID, err := resolvedProviderThreadID(binding)
	if err != nil {
		return err
	}
	if providerThreadID != "" {
		ref.ProviderThreadID = providerThreadID
	}
	if sessionID := resolvedSessionID(binding); sessionID != "" {
		ref.SessionID = sessionID
	}
	if ref.CWD == "" {
		ref.CWD = strings.TrimSpace(binding.Cwd)
	}
	return nil
}

// resolvedProviderThreadID 返回 binding 的统一恢复 identity。
func resolvedProviderThreadID(binding *bindingStoreRecord) (string, error) {
	return recoverableBindingProviderThreadID(binding)
}

func resolvedSessionID(binding *bindingStoreRecord) string {
	if binding == nil {
		return ""
	}
	sessionUUID := strings.TrimSpace(binding.SessionUUID)
	if identifier.LooksLikeUUID(sessionUUID) {
		return sessionUUID
	}
	return strings.TrimSpace(binding.ProviderThreadID)
}

// evictZombieSession 清理已断开但仍留在 session provider 中的旧会话。
// RemoveSession 会触发 provider 侧关闭和进程回收；若线程未被停止或归档，再释放
// resumeInFlight 标记，让后续后台恢复可以重新建立会话。
func (s *service) evictZombieSession(ctx context.Context, threadID string) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		return
	}
	agentID := strings.TrimSpace(binding.AgentID)
	if agentID == "" {
		return
	}
	if s.sessions != nil {
		pkglogger.Warn("thread: evictZombieSession → closing old session + reclaiming CLI process",
			"agent_id", agentID,
			"thread_id", threadID,
		)
		s.sessions.RemoveSession(agentID)
	}
	if _, blocked := s.resumeLifecycleBlockReason(ctx, threadID, binding); blocked {
		return
	}
	// 清除并发恢复标记，允许后续后台恢复重新检查当前 binding 和生命周期状态。
	s.resumeInFlight.Delete(agentID)
}

// backgroundResumeIfNeeded 在已有持久化 binding 但当前无活跃 session 时触发后台 Resume。
// 这样用户打开历史线程后，首条消息到达前 provider session 有机会提前恢复。
//
// 这里使用 context.Background()，因为 service 没有独立生命周期 context。
// goroutine 由 resumeInFlight 限制为每个 agent 最多一次，Resume 本身仍受 provider 超时控制。
func (s *service) backgroundResumeIfNeeded(ctx context.Context, threadID string) {
	agentID, ok, err := s.backgroundResumeCandidate(ctx, threadID)
	if err != nil {
		util.LogIgnoredError(s.logger, "thread: background resume eligibility failed", err)
		return
	}
	if !ok {
		return
	}
	// 防止恢复风暴：同一 agent 已尝试恢复时直接跳过。
	// 失败后保留标记，避免 ReadMessages 高频触发无限重试并耗尽数据库连接。
	if _, loaded := s.resumeInFlight.LoadOrStore(agentID, struct{}{}); loaded {
		return
	}
	safego.Go(context.Background(), s.logger, "thread.backgroundResume", func(ctx context.Context) {
		if s.logger != nil {
			s.logger.Info("thread: background resume", "thread_id", threadID, "agent_id", agentID)
		}
		if _, err := s.Resume(ctx, ResumeRequest{ThreadID: threadID}); err != nil {
			util.LogIgnoredError(s.logger, "thread: background resume failed", err)
			// 失败时保留 resumeInFlight，阻断后续自动重试。
			return
		}
		// 成功后清除标记，后续 ReadMessages 可重新检查活跃 session。
		s.resumeInFlight.Delete(agentID)
	})
}

// backgroundResumeCandidate 判断线程是否需要后台恢复。
// 只有存在可恢复 provider 历史、未被停止/归档阻断且当前没有活跃 session 时才返回 agent id。
func (s *service) backgroundResumeCandidate(ctx context.Context, threadID string) (string, bool, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		return "", false, err
	}
	agentID := strings.TrimSpace(binding.AgentID)
	if agentID == "" {
		return "", false, nil
	}
	providerThreadID, err := recoverableBindingProviderThreadID(binding)
	if err != nil {
		return "", false, err
	}
	if providerThreadID == "" {
		return "", false, nil
	}
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, threadID, binding); blocked {
		if s.logger != nil {
			s.logger.Info("thread: background resume skipped by lifecycle",
				"thread_id", threadID,
				"agent_id", agentID,
				"reason", reason,
			)
		}
		return "", false, nil
	}
	if s.sessions != nil {
		if sess, _ := s.sessions.GetSession(agentID); sess != nil {
			return "", false, nil
		}
	}
	return agentID, true, nil
}

// closeSessionForAgent 关闭并移除指定 agent 的本地 session。
// 记录 generation 后再关闭，避免 stop 过程中误删同一 agent 后续新建的 session。
func (s *service) closeSessionForAgent(ctx context.Context, agentID string) error {
	if s.sessions == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	var generation uint64
	if provider, ok := s.sessions.(sessionGenerationProvider); ok {
		generation = provider.SessionGeneration(agentID)
	}
	session, err := s.sessions.GetSession(agentID)
	if err != nil {
		if errors.Is(err, contract.ErrSessionNotFound) {
			s.removeStoppedSession(agentID, generation)
			return errLocalSessionAlreadyGone
		}
		return err
	}
	if session == nil {
		s.removeStoppedSession(agentID, generation)
		return errLocalSessionAlreadyGone
	}
	err = session.Close(ctx)
	s.removeStoppedSession(agentID, generation)
	return err
}

func (s *service) removeStoppedSession(agentID string, generation uint64) {
	if s.sessions == nil {
		return
	}
	if generation != 0 {
		if remover, ok := s.sessions.(sessionGenerationRemover); ok {
			remover.RemoveSessionGeneration(agentID, generation)
			return
		}
	}
	s.sessions.RemoveSession(agentID)
}
