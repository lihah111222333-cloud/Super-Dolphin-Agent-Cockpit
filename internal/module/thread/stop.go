package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// threadStopState 是停止线程时一次性计算出的运行时和持久化目标。
// Stop 后续步骤复用这份快照，避免停止过程中 binding/thread 状态变化导致清理目标前后不一致。
type threadStopState struct {
	agentID   string                    // 运行时 agent id
	stoppedID string                    // 写回 stopped 状态的 thread id
	targets   []string                  // 需要清理 turn/thread-agent 缓存的全部 thread id
	binding   *threadBindingStoreRecord // 当前 thread 对应的 provider binding
}

// threadRuntimeStopOutcome 保存运行时收束成功后、持久化完成前必须携带的状态。
// releaseResume 维持恢复阻断，interruptErr 延迟到 durable finalization 完成后再返回。
type threadRuntimeStopOutcome struct {
	releaseResume func()
	interruptErr  error
}

// errResumeLifecycleBlocked 标记恢复请求被停止/归档生命周期阻断。
var errResumeLifecycleBlocked = errors.New("thread resume blocked by lifecycle state")

// blockResumeForAgent 在停止运行时期间阻断同一 agent 的恢复。
// 标记存在于进程内 sync.Map，停止路径结束或清理完成后必须配对解除。
func (s *service) blockResumeForAgent(agentID string) {
	if s == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	s.resumeBlocked.Store(agentID, struct{}{})
}

// unblockResumeForAgent 解除 agent 的恢复阻断标记。
// 允许空 service 或空 id 静默返回，便于 defer 清理路径无条件调用。
func (s *service) unblockResumeForAgent(agentID string) {
	if s == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	s.resumeBlocked.Delete(agentID)
}

// blockResumeForAgentUntilDurable 持有恢复阻断，直到调用方完成持久化状态写入。
// 返回的 release 可安全多次调用，供 Stop/Archive/Delete 在成功和错误路径统一释放。
func (s *service) blockResumeForAgentUntilDurable(agentID string) func() {
	s.blockResumeForAgent(agentID)
	released := false
	return func() {
		if released {
			return
		}
		released = true
		s.unblockResumeForAgent(agentID)
	}
}

// unblockResumeForThread 根据 thread/binding 反查 agent 后解除恢复阻断。
// binding 不可用时退回 threadID 作为 key，覆盖停止早期还没写入 binding 的场景。
func (s *service) unblockResumeForThread(ctx context.Context, threadID string) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		s.unblockResumeForAgent(threadID)
		return
	}
	s.unblockResumeForAgent(binding.AgentID)
}

// resetSessionRecoveryForThread 清理线程对应 agent 的恢复重试计数。
// binding 缺失时使用 threadID 兜住启动失败或历史数据不完整的路径。
func (s *service) resetSessionRecoveryForThread(ctx context.Context, threadID string) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		s.resetSessionRecoveryCount(threadID)
		return
	}
	s.resetSessionRecoveryCount(binding.AgentID)
}

// resumeLifecycleBlockReason 判断恢复请求是否被停止/归档状态阻断。
// 它先看进程内停止标记，再看持久化状态，确保 stop 和 resume 并发时不会重新拉起旧 session。
func (s *service) resumeLifecycleBlockReason(
	ctx context.Context,
	threadID string,
	binding *threadBindingStoreRecord,
) (string, bool) {
	binding = s.resolveResumeLifecycleBinding(ctx, threadID, binding)
	if reason, blocked := s.resumeAgentLifecycleBlock(threadID, binding); blocked {
		return reason, true
	}
	if thread, ok := s.resumeLifecycleThreadRecord(ctx, threadID, binding); ok {
		return resumeLifecycleThreadBlock(thread)
	}
	return "", false
}

// resolveResumeLifecycleBinding 为恢复生命周期检查补齐 binding。
// 调用方已经提供 binding 或 store 不可用时直接复用现状，避免在错误路径里扩大查询面。
func (s *service) resolveResumeLifecycleBinding(
	ctx context.Context,
	threadID string,
	binding *threadBindingStoreRecord,
) *threadBindingStoreRecord {
	if s == nil || binding != nil || s.bindingStore == nil {
		return binding
	}
	resolved, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil
	}
	return resolved
}

// resumeAgentLifecycleBlock 检查指定 agent 是否处于进程内停止阻断状态。
// 停止流程先写入该标记，后台恢复看到后必须跳过，避免刚停止的 session 被重新拉起。
func (s *service) resumeAgentLifecycleBlock(threadID string, binding *threadBindingStoreRecord) (string, bool) {
	if s == nil {
		return "", false
	}
	agentID := strings.TrimSpace(bindingAgentID(binding))
	if agentID == "" {
		agentID = strings.TrimSpace(threadID)
	}
	if _, blocked := s.resumeBlocked.Load(agentID); blocked {
		return "agent_stopping", true
	}
	if binding != nil && binding.Archived {
		return "binding_archived", true
	}
	return "", false
}

// resumeLifecycleThreadBlock 把持久化线程状态转换为恢复阻断原因。
// fork creating/failed 不能被普通 resume 消费；非 fork failed 保留既有可恢复语义。
func resumeLifecycleThreadBlock(thread threadConfigRecord) (string, bool) {
	switch strings.TrimSpace(thread.Status) {
	case statusArchived:
		return "thread_archived", true
	case statusStopped:
		return "thread_stopped", true
	case statusForkCreating:
		return "fork_creating", true
	case statusFailed:
		if retainedForkThread(thread) {
			return "fork_failed", true
		}
		return "", false
	default:
		return "", false
	}
}

func retainedForkThread(thread threadConfigRecord) bool {
	return strings.TrimSpace(thread.OwnerThreadID) != ""
}

// resumeLifecycleThreadRecord 推导恢复请求应遵守的持久化线程记录。
// 它按请求 threadID、binding.CodexThreadID、binding.AgentID 查找，命中后由调用方基于完整记录判断能否恢复。
func (s *service) resumeLifecycleThreadRecord(
	ctx context.Context,
	threadID string,
	binding *threadBindingStoreRecord,
) (threadConfigRecord, bool) {
	store := s.threadConfigStorePort()
	if store == nil {
		return threadConfigRecord{}, false
	}
	for _, id := range resumeLifecycleThreadIDs(threadID, binding) {
		thread, err := store.GetByThreadID(ctx, id)
		if err != nil || thread == nil {
			continue
		}
		return *thread, true
	}
	return threadConfigRecord{}, false
}

// resumeLifecycleThreadIDs 生成恢复生命周期检查需要查询的候选 thread id。
// binding 里的 CodexThreadID 和 AgentID 都可能承载旧会话索引，必须一起检查。
func resumeLifecycleThreadIDs(threadID string, binding *threadBindingStoreRecord) []string {
	candidates := []string{strings.TrimSpace(threadID)}
	if binding != nil {
		candidates = append(candidates,
			strings.TrimSpace(binding.CodexThreadID),
			strings.TrimSpace(binding.AgentID),
		)
	}
	return uniqueResumeLifecycleIDs(candidates)
}

// uniqueResumeLifecycleIDs 去重恢复生命周期候选 id。
// 保留输入顺序，使调用方优先检查用户请求的 threadID，再检查 binding 派生 id。
func uniqueResumeLifecycleIDs(candidates []string) []string {
	out := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, id := range candidates {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// resumeLifecycleError 包装恢复生命周期阻断错误。
// 外层可通过 errors.Is 识别 errResumeLifecycleBlocked，同时保留具体阻断原因。
func resumeLifecycleError(threadID, reason string) error {
	return fmt.Errorf("%w: %s for %q", errResumeLifecycleBlocked, reason, strings.TrimSpace(threadID))
}

// Stop 按用户请求收束线程关联的 provider session、活跃 turn 和托管 agent。
// pending_launch 线程走轻量删除路径；普通线程会先构造稳定 stopState，确保并发 resume 不会复活旧 session。
func (s *service) Stop(ctx context.Context, threadID string) error {
	ctx = util.NonNilContext(ctx)
	stopState, err := s.resolveThreadStopState(ctx, threadID)
	if err != nil {
		if handled, pendingErr := s.stopPendingLaunchThread(ctx, threadID); handled || pendingErr != nil {
			return pendingErr
		}
		stopState, err = s.resolveThreadStopState(ctx, threadID)
		if err != nil {
			return err
		}
	}
	runtimeStop, err := s.stopThreadRuntimeUntilDurable(ctx, stopState, "thread_stopped", false)
	if err != nil {
		return err
	}
	defer runtimeStop.releaseResume()
	if err := s.updateThreadStatus(ctx, stopState.stoppedID, statusStopped); err != nil {
		return errors.Join(runtimeStop.interruptErr, err)
	}
	cleanupErr := s.cleanupThreadScratchpad(ctx, stopState.stoppedID, stopState.binding)
	for _, id := range stopState.targets {
		s.forgetThreadAgent(id)
	}
	turnCleanupErr := s.cleanupThreadTurns(ctx, "thread_stopped", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, stopState.agentID, statusStopped, "stopped")
	return newLifecyclePartialCleanupError(
		"stop",
		errors.Join(runtimeStop.interruptErr, cleanupErr, turnCleanupErr),
	)
}

// stopPendingLaunchThread 停止待处理启动线程。
func (s *service) stopPendingLaunchThread(ctx context.Context, threadID string) (bool, error) {
	store := s.threadConfigStorePort()
	if store == nil {
		return false, nil
	}
	trimmed := strings.TrimSpace(threadID)
	if trimmed == "" {
		return false, nil
	}
	mu := s.acquirePendingLaunchLock(trimmed)
	mu.Lock()
	defer mu.Unlock()
	row, err := store.GetByThreadID(ctx, trimmed)
	if err != nil {
		return false, err
	}
	if row == nil || !row.PendingLaunch {
		return false, nil
	}
	if err := s.updateThreadStatus(ctx, trimmed, statusStopped); err != nil {
		return true, err
	}
	s.CompleteLaunchIntent(ctx, trimmed)
	s.publishThreadStopped(trimmed, "", statusStopped, "stopped_pending_launch")
	return true, nil
}

// resolveThreadStopState 读取 binding 并构造停止所需的稳定目标快照。
// 这里不做状态写入；调用方拿到快照后再按固定顺序停止 runtime、更新 thread 和清理 turn。
func (s *service) resolveThreadStopState(ctx context.Context, threadID string) (threadStopState, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return threadStopState{}, err
	}
	return newThreadStopState(binding, threadID), nil
}

// newThreadStopState 从 binding 和请求 thread id 推导停止目标。
// stoppedID 用于状态写回，targets 用于缓存/turn 清理，二者不能混用。
func newThreadStopState(binding *threadBindingStoreRecord, threadID string) threadStopState {
	return threadStopState{
		agentID:   strings.TrimSpace(bindingAgentID(binding)),
		stoppedID: stoppedThreadID(binding, threadID),
		targets:   stopThreadTargets(binding, threadID),
		binding:   binding,
	}
}

// stopThreadRuntime 停止线程运行时。
func (s *service) stopThreadRuntime(
	ctx context.Context,
	stopState threadStopState,
	source string,
	allowMissingAgent bool,
) (func(), error) {
	result, err := s.stopThreadRuntimeUntilDurable(ctx, stopState, source, allowMissingAgent)
	if err != nil {
		return nil, err
	}
	if result.interruptErr != nil {
		result.releaseResume()
		return nil, result.interruptErr
	}
	return result.releaseResume, nil
}

// stopThreadRuntimeUntilDurable 收束运行时并维持恢复阻断，供调用方完成 durable finalization。
// interrupt 失败不代表 teardown 失败，因此单独携带，只有 session/agent 收束失败才立即返回。
func (s *service) stopThreadRuntimeUntilDurable(
	ctx context.Context,
	stopState threadStopState,
	source string,
	allowMissingAgent bool,
) (threadRuntimeStopOutcome, error) {
	pkglogger.Info("thread: stopThreadRuntime ENTERED",
		"agent_id", stopState.agentID,
		"stopped_id", stopState.stoppedID,
		"source", source,
		"allow_missing_agent", allowMissingAgent,
		"caller", archiveCallerStack(),
	)
	releaseResume := s.blockResumeForAgentUntilDurable(stopState.agentID)
	interruptErr := s.interruptStoppingThread(ctx, stopState.agentID, source)
	localSessionGone := false
	if err := s.closeSessionForAgent(ctx, stopState.agentID); err != nil {
		if !errors.Is(err, errLocalSessionAlreadyGone) {
			releaseResume()
			pkglogger.Warn("thread: stopThreadRuntime closeSession FAILED",
				"agent_id", stopState.agentID,
				"error", err,
			)
			return threadRuntimeStopOutcome{}, errors.Join(interruptErr, err)
		}
		localSessionGone = true
	}
	err := s.stopManagedAgent(ctx, stopState.agentID, allowMissingAgent || localSessionGone)
	if err != nil {
		releaseResume()
		pkglogger.Warn("thread: stopThreadRuntime DONE with error",
			"agent_id", stopState.agentID,
			"error", err,
		)
		return threadRuntimeStopOutcome{}, errors.Join(interruptErr, err)
	}
	return threadRuntimeStopOutcome{
		releaseResume: releaseResume,
		interruptErr:  interruptErr,
	}, nil
}

// interruptStoppingThread 在停止 provider 前尝试中断活跃 turn。
// 中断失败会在运行时收束完成后返回，避免将不完整的停止报告为成功。
func (s *service) interruptStoppingThread(ctx context.Context, agentID, source string) error {
	if s.turns == nil {
		return nil
	}
	if s.sessions == nil {
		return nil
	}
	session, err := s.sessions.GetSession(strings.TrimSpace(agentID))
	switch {
	case errors.Is(err, contract.ErrSessionNotFound):
		session = nil
	case err != nil:
		return fmt.Errorf("lookup stopping session: %w", err)
	}
	if session == nil {
		return nil
	}
	if err := s.turns.InterruptActiveTurn(ctx, session, source); err != nil {
		if s.logger != nil {
			s.logger.Warn("thread stop: interrupt active turn failed", "agent_id", agentID, "error", err)
		}
		return err
	}
	return nil
}

func bindingAgentID(binding *threadBindingStoreRecord) string {
	if binding == nil {
		return ""
	}
	return binding.AgentID
}

// stopManagedAgent 停止 orchestration 侧托管 agent。
// allowMissingAgent 为真时容忍 agent 已被外部清理；其它错误会返回给 Stop 触发恢复阻断清理。
func (s *service) stopManagedAgent(ctx context.Context, agentID string, allowMissingAgent bool) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	if s.orchestration == nil {
		pkglogger.Info("thread: stopManagedAgent no orchestration",
			"agent_id", agentID,
		)
		return nil
	}
	err := s.orchestration.StopAgent(ctx, agentID)
	if allowMissingAgent && errors.Is(err, contract.ErrAgentNotFound) {
		return nil
	}
	if err != nil {
		pkglogger.Warn("thread: stopManagedAgent StopAgent FAILED",
			"agent_id", agentID,
			"error", err,
		)
	}
	return err
}

func (s *service) cleanupThreadTurns(ctx context.Context, reason string, threadIDs ...string) error {
	if s.turns == nil {
		return nil
	}
	var cleanupErrors []error
	for _, threadID := range uniqueThreadIDs(threadIDs...) {
		if err := s.turns.CleanupThread(ctx, threadID, reason); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cleanup thread turns %q: %w", threadID, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func stopThreadTargets(binding *threadBindingStoreRecord, threadID string) []string {
	if binding == nil {
		return uniqueThreadIDs(threadID)
	}
	return uniqueThreadIDs(
		threadID,
		binding.ProviderThreadID,
		binding.CodexThreadID,
		binding.AgentID,
	)
}

func stoppedThreadID(binding *threadBindingStoreRecord, threadID string) string {
	if binding == nil {
		return strings.TrimSpace(threadID)
	}
	return util.FirstNonEmpty(
		binding.CodexThreadID,
		threadID,
		binding.ProviderThreadID,
		binding.AgentID,
	)
}

func uniqueThreadIDs(values ...string) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ids = append(ids, value)
	}
	return ids
}
