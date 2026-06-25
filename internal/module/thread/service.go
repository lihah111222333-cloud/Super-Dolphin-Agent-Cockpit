package thread

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idempotency"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

const (
	statusArchived = "archived"
	statusCreated  = "created"
	statusFailed   = "failed"
	statusStopped  = "stopped"
)

var errLocalSessionAlreadyGone = errors.New("thread local session already gone")

// SessionProvider is an alias for contract.SessionProvider.
// Kept as a local type alias for backward compatibility within this package.
type SessionProvider = contract.SessionProvider

// sessionGenerationRemover 用于按 generation 精确移除已停止的 session，避免误删新建 session。
type sessionGenerationRemover interface {
	RemoveSessionGeneration(agentID string, generation uint64)
}

type service struct {
	logger         *slog.Logger
	threadStore    threadstore.Store
	bindingStore   bindingstore.Store
	sharedFiles    sharedfilestore.Store
	sessions       SessionProvider
	starter        SessionStarter
	promptAssembly contract.PromptAssemblyService
	cfg            *contract.Config
	toolRegistry   contract.ToolRegistry
	mcpServers     contract.MCPServerConfigProvider
	turns          contract.TurnThreadCleaner
	orchestration  OrchestrationFacade
	tracing        *platformobs.Service
	bus            *event.Dispatcher

	emitStarted      func(threaddto.Started)
	emitStopped      func(threaddto.Stopped)
	emitUpdated      func(threaddto.Updated)
	emitMessagesPage func(threaddto.MessagesPage)
	emitCompacted    func(threaddto.Compacted)
	emitLaunched     func(threaddto.Launched)

	// pendingLaunchMu serializes SpawnIfNeeded per thread_id so concurrent
	// first-turns of a pending thread fork exactly one CLI process.
	pendingLaunchMu sync.Map // key: threadID(string), value: *sync.Mutex

	// agentIDMu protects process-local agent_id reservations made while
	// thread/start is still launching and has not persisted agent_threads yet.
	agentIDMu           sync.Mutex
	agentIDReservations map[string]struct{}

	launchIntentRegistry idempotency.Registry[StartResult]
	launchIntentByThread sync.Map

	threadAgentsMu sync.RWMutex
	threadAgents   map[string]string

	resumeInFlight, resumeBlocked, sessionRecoveryCount sync.Map

	// promptStore is optional; when nil, thread/start skips injection and the
	// CLI falls back to its bundled system prompt. When wired, it powers the
	// agent_key → prompt_text lookup in resolveRoutedPrompt.
	promptStore promptstore.Store
	// promptCatalog is the runtime read path for routed prompts. It may combine
	// repo-owned builtin prompts with DB-owned user assets; promptStore remains
	// the write path for prompt_versions snapshots.
	promptCatalog promptstore.RuntimePromptCatalog

	// matchWhenEval evaluates a prompt_template's match_when JSONB expression
	// against the current BuildCtx. Injected from prompt.EvaluateMatchWhen at
	// construction time to avoid a direct thread→prompt import. Nil-safe:
	// maybeAutoRouteByMatchWhen skips evaluation when nil.
	matchWhenEval contract.MatchWhenEvaluator

	// enableWhenEval keeps router prompt_versions snapshots aligned with assembler gates.
	enableWhenEval contract.EnableWhenEvaluator

	reconnectDelay time.Duration

	// agentLaunchedWorker is the P22 P2 (thread S4) single owner of the
	// onAgentLaunched -> binding store write + prompt-assembly
	// invalidation slow-path. Always constructed; processAgentLaunched
	// guards on bindingStore so a nil-store service is still safe.
	agentLaunchedWorker *agentLaunchedWorker

	// sessionRecoveryWorker is the P22 P2 (thread S2) single owner of
	// the onAgentFailed -> session-level recovery slow-path (rate-limit,
	// evict zombie, 3s reconnect delay, backgroundResumeIfNeeded). The
	// pre-P22 naked runtimesafe.SafeGo(context.Background(), ...) + 3s
	// time.Sleep moved into processSessionRecovery under a WaitGroup-
	// tracked worker goroutine.
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

// List 列出线程。
func (s *service) List(ctx context.Context) ([]Ref, error) {
	return s.listThreads(ctx, nil)
}

// Get 读取线程。
func (s *service) Get(ctx context.Context, id string) (*Ref, error) {
	thread, err := s.getThread(ctx, id)
	if err != nil {
		return nil, err
	}
	ref := toRef(*thread)
	s.enrichRefIdentity(ctx, &ref)
	return &ref, nil
}

// ListByStatus 按状态列出线程。
func (s *service) ListByStatus(ctx context.Context, status string) ([]Ref, error) {
	want := strings.TrimSpace(status)
	if want == "" {
		return s.List(ctx)
	}
	return s.listThreads(ctx, func(thread threadstore.Thread) bool {
		return strings.EqualFold(strings.TrimSpace(thread.Status), want)
	})
}

// ListByCWD 按工作目录列出线程。
func (s *service) ListByCWD(ctx context.Context, cwdPrefix string) ([]Ref, error) {
	prefix := strings.TrimSpace(cwdPrefix)
	return s.listThreads(ctx, func(thread threadstore.Thread) bool {
		return prefix == "" || strings.HasPrefix(strings.TrimSpace(thread.Cwd), prefix)
	})
}

// SetName 设置名称。
func (s *service) SetName(ctx context.Context, threadID, name string) error {
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	thread.Name = name
	thread.Prompt = name
	thread.ManuallyRenamed = true
	thread.UpdatedAt = time.Now().Unix()
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

// Delete 删除线程。
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
	if err := s.deleteThreadRuntime(ctx, stopState, binding); err != nil {
		return err
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
) (*bindingstore.Binding, bool, error) {
	if s.bindingStore == nil {
		return nil, false, nil
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

// deletePendingLaunchThread 删除待处理启动线程。
func (s *service) deletePendingLaunchThread(
	ctx context.Context,
	threadID string,
	binding *bindingstore.Binding,
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
	binding *bindingstore.Binding,
) error {
	if binding == nil {
		return nil
	}
	return s.stopThreadRuntime(ctx, stopState, "thread_deleted", true)
}

// deleteThreadBinding 从 binding store 删除 agent 关联记录。
func (s *service) deleteThreadBinding(ctx context.Context, binding *bindingstore.Binding) error {
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
	binding *bindingstore.Binding,
) error {
	s.cleanupThreadScratchpad(ctx, threadID, binding)
	s.forgetThreadAgents(stopState.targets...)
	if s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	if err := s.threadStore.DeleteByThreadID(ctx, stopState.stoppedID); err != nil {
		return err
	}
	s.cleanupThreadTurns(ctx, "thread_deleted", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, agentIDFromBinding(binding, stopState.stoppedID), "deleted", "deleted")
	return nil
}

// forgetThreadAgents 批量移除进程内 threadID→agentID 映射缓存。
func (s *service) forgetThreadAgents(threadIDs ...string) {
	for _, threadID := range threadIDs {
		s.forgetThreadAgent(threadID)
	}
}

// listThreads 列出线程。
func (s *service) listThreads(ctx context.Context, filter func(threadstore.Thread) bool) ([]Ref, error) {
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

// getThread 按 threadID 从 store 读取单条记录。
func (s *service) getThread(ctx context.Context, threadID string) (*threadstore.Thread, error) {
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
func (s *service) upsertThread(ctx context.Context, thread threadstore.Thread) error {
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
	return s.threadStore.UpdateStatus(ctx, threadstore.UpdateStatusParams{
		ThreadID:  id,
		Status:    strings.TrimSpace(status),
		UpdatedAt: time.Now().Unix(),
	})
}

// resolveBinding 按 threadID 解析 binding，通过多路径回退查找对应 agent。
func (s *service) resolveBinding(ctx context.Context, threadID string) (*bindingstore.Binding, error) {
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
func (s *service) resolveSession(ctx context.Context, threadID string) (contract.Session, *bindingstore.Binding, error) {
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

// enrichRefIdentity 补充引用身份。
func (s *service) enrichRefIdentity(ctx context.Context, ref *Ref) {
	if s == nil || s.bindingStore == nil || ref == nil {
		return
	}
	binding, err := s.resolveBinding(ctx, ref.ID)
	if err != nil || binding == nil {
		return
	}
	if provider := strings.TrimSpace(binding.Provider); provider != "" {
		ref.Provider = provider
	}
	if providerThreadID := resolvedProviderThreadID(binding); providerThreadID != "" {
		ref.ProviderThreadID = providerThreadID
	}
	if sessionID := resolvedSessionID(binding); sessionID != "" {
		ref.SessionID = sessionID
	}
	if ref.CWD == "" {
		ref.CWD = strings.TrimSpace(binding.Cwd)
	}
}

func resolvedProviderThreadID(binding *bindingstore.Binding) string {
	return recoverableBindingProviderThreadID(binding)
}

func resolvedSessionID(binding *bindingstore.Binding) string {
	if binding == nil {
		return ""
	}
	sessionUUID := strings.TrimSpace(binding.SessionUUID)
	if identifier.LooksLikeUUID(sessionUUID) {
		return sessionUUID
	}
	return strings.TrimSpace(binding.ProviderThreadID)
}

// evictZombieSession removes a dead session (transport closed, context
// canceled) left by Archive so that the next resolve path creates a fresh
// session. RemoveSession triggers session.Close → shutdownSession →
// poolRelease, which reclaims the old CLI process. It also clears the
// resumeInFlight guard to allow backgroundResumeIfNeeded to proceed.
// evictZombieSession 处理evict僵尸会话。
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
	// Clear the stampede guard so backgroundResumeIfNeeded can proceed.
	s.resumeInFlight.Delete(agentID)
}

// backgroundResumeIfNeeded checks whether the thread has a stored binding
// (from a previous session) but no active session, and triggers a background
// Resume so the session is ready by the time the user sends a message.
//
// context.Background() is used because the thread service has no lifecycle
// context and the goroutine is naturally bounded: resumeInFlight prevents
// stampede (at most one in-flight per agent), and Resume itself is a single
// provider round-trip with provider-side timeouts.
func (s *service) backgroundResumeIfNeeded(ctx context.Context, threadID string) {
	agentID, ok := s.backgroundResumeCandidate(ctx, threadID)
	if !ok {
		return
	}
	// Prevent stampede: skip if a resume was already attempted for this agent.
	// The entry is never deleted — a failed resume stays marked so we don't
	// retry in an infinite loop and exhaust the DB connection pool.
	if _, loaded := s.resumeInFlight.LoadOrStore(agentID, struct{}{}); loaded {
		return
	}
	safego.Go(context.Background(), s.logger, "thread.backgroundResume", func(ctx context.Context) {
		if s.logger != nil {
			s.logger.Info("thread: background resume", "thread_id", threadID, "agent_id", agentID)
		}
		if _, err := s.Resume(ctx, ResumeRequest{ThreadID: threadID}); err != nil {
			util.LogIgnoredError(s.logger, "thread: background resume failed", err)
			// Keep resumeInFlight entry to block further retries.
			return
		}
		// Only clear on success so subsequent ReadMessages can detect a live session.
		s.resumeInFlight.Delete(agentID)
	})
}

// backgroundResumeCandidate 处理后台恢复候选项。
func (s *service) backgroundResumeCandidate(ctx context.Context, threadID string) (string, bool) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		return "", false
	}
	agentID := strings.TrimSpace(binding.AgentID)
	if agentID == "" {
		return "", false
	}
	if recoverableBindingProviderThreadID(binding) == "" {
		return "", false
	}
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, threadID, binding); blocked {
		if s.logger != nil {
			s.logger.Info("thread: background resume skipped by lifecycle",
				"thread_id", threadID,
				"agent_id", agentID,
				"reason", reason,
			)
		}
		return "", false
	}
	if s.sessions != nil {
		if sess, _ := s.sessions.GetSession(agentID); sess != nil {
			return "", false
		}
	}
	return agentID, true
}

// closeSessionForAgent 为代理关闭会话。
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

func (s *service) setBindingArchived(ctx context.Context, threadID string, archived bool) error {
	if s.bindingStore == nil {
		return nil
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return err
	}
	return s.bindingStore.SetArchived(ctx, bindingstore.SetArchivedParams{
		AgentID:   strings.TrimSpace(binding.AgentID),
		Archived:  archived,
		UpdatedAt: time.Now().Unix(),
	})
}
