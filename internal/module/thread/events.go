package thread

import (
	"context"
	"strings"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/identifier"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func (s *service) bindDispatcher(dispatcher *event.Dispatcher) {
	if s == nil || dispatcher == nil {
		return
	}
	s.bus = dispatcher
}

func registerThreadSubscriptions(svc *service) []context.CancelFunc {
	if svc == nil {
		return nil
	}
	return []context.CancelFunc{
		contract.ResilientSubscribe(svc.bus, svc.onAgentLaunched, svc.logger),
		contract.ResilientSubscribe(svc.bus, svc.onAgentFailed, svc.logger),
	}
}

func (s *service) startBusWorkers() {
	if s == nil {
		return
	}
	if s.agentLaunchedWorker != nil {
		s.agentLaunchedWorker.Start()
	}
	if s.sessionRecoveryWorker != nil {
		s.sessionRecoveryWorker.Start()
	}
}

func (s *service) stopBusWorkers(ctx context.Context) {
	if s == nil {
		return
	}
	if s.agentLaunchedWorker != nil {
		s.drainBusWorker(ctx, "agent launched worker", s.agentLaunchedWorker.Stop)
	}
	if s.sessionRecoveryWorker != nil {
		s.drainBusWorker(ctx, "session recovery worker", s.sessionRecoveryWorker.Stop)
	}
}

func (s *service) drainBusWorker(ctx context.Context, name string, stop func(context.Context) error) {
	if err := stop(ctx); err != nil && s.logger != nil {
		s.logger.Warn("thread: "+name+" drain failed", "error", err)
	}
}

// onAgentLaunched 将 agent 启动事件投递到串行 worker。
// 回调不直接写 binding，避免 bus 线程承担慢 I/O，也让 shutdown 可等待未完成的写入。
func (s *service) onAgentLaunched(ev agentdto.AgentLaunched) {
	if s == nil || s.agentLaunchedWorker == nil || s.threadBindingStorePort() == nil {
		return
	}
	agentID := strings.TrimSpace(ev.AgentID)
	threadID := strings.TrimSpace(ev.ThreadID)
	key := agentID
	if key == "" {
		key = threadID
	}
	if key == "" {
		return
	}
	s.agentLaunchedWorker.Enqueue(key, ev)
}

// processAgentLaunched 根据 provider 启动事件补写 binding 中的 session 身份。
// 事件可能缺 agent_id，因此先用 threadID 反查 binding；只有 UUID 可恢复且历史文件存在时才写 provider_thread_id。
func (s *service) processAgentLaunched(ev agentdto.AgentLaunched) error {
	if s.threadBindingStorePort() == nil {
		return nil
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	sessionID := strings.TrimSpace(ev.SessionID)
	ctx := context.Background()
	// Claude system:init 可能不带 agent_id，因此用 threadID 反查 binding 作为权威身份。
	binding, err := s.resolveBindingForEvent(ctx, agentID, threadID)
	if err != nil {
		return err
	}
	if binding == nil {
		return nil
	}
	if err := s.syncAgentLaunchCWD(ctx, binding, threadID, ev.CWD); err != nil {
		return err
	}
	agentID = strings.TrimSpace(binding.AgentID)
	if agentID == "" || sessionID == "" || !identifier.LooksLikeUUID(sessionID) {
		return nil
	}
	if err := s.recordAgentLaunchSessionUUID(ctx, binding, threadID, agentID, sessionID); err != nil {
		return err
	}
	return s.recordAgentLaunchProviderThreadID(ctx, binding, threadID, agentID, sessionID)
}

func (s *service) recordAgentLaunchSessionUUID(ctx context.Context, binding *threadBindingRecord, threadID, agentID, sessionID string) error {
	if strings.TrimSpace(binding.SessionUUID) == sessionID {
		return nil
	}
	store := s.threadBindingStorePort()
	if store == nil {
		return nil
	}
	if err := store.UpdateSessionUUID(ctx, threadBindingSessionUUIDUpdate{
		AgentID:     agentID,
		SessionUUID: sessionID,
		UpdatedAt:   time.Now().UnixMilli(),
	}); err != nil {
		return err
	}
	binding.SessionUUID = sessionID
	s.logger.Info("thread: updated session_uuid from agent event", "thread_id", threadID, "agent_id", agentID, "session_uuid", sessionID)
	return nil
}

// recordAgentLaunchProviderThreadID 从启动事件记录可恢复的 provider thread id。
// 已存在的真实 UUID 不会被覆盖；无法定位 provider 历史文件时只记日志，避免写入不可恢复身份。
func (s *service) recordAgentLaunchProviderThreadID(ctx context.Context, binding *threadBindingRecord, threadID, agentID, sessionID string) error {
	providerThreadID := normalizeProviderThreadID(binding.Provider, sessionID)
	if providerThreadID == "" {
		return nil
	}
	current := strings.TrimSpace(binding.ProviderThreadID)
	if current == providerThreadID {
		return nil
	}
	if current != "" && current != agentID && identifier.LooksLikeUUID(current) {
		return nil
	}
	if !bindingRecordHasProviderHistoryForUUID(binding, providerThreadID) {
		if s.logger != nil {
			fields := []any{
				"thread_id", threadID,
				"agent_id", agentID,
				"provider_thread_id", providerThreadID,
			}
			fields = append(fields, platformshared.SafePathLogFields("rollout_path", binding.RolloutPath)...)
			s.logger.Info("thread: provider_thread_id from agent event is not recoverable", fields...)
		}
		return nil
	}
	store := s.threadBindingStorePort()
	if store == nil {
		return nil
	}
	if err := store.UpdateProviderThreadID(ctx, threadBindingProviderThreadIDUpdate{
		AgentID:          agentID,
		ProviderThreadID: providerThreadID,
		UpdatedAt:        time.Now().UnixMilli(),
	}); err != nil {
		return err
	}
	binding.ProviderThreadID = providerThreadID
	s.logger.Info("thread: updated provider_thread_id from agent event", "thread_id", threadID, "agent_id", agentID, "provider_thread_id", providerThreadID)
	return nil
}

// syncAgentLaunchCWD 将启动事件里的 CWD 回写到 binding。
// 只有原 binding 还没有可信 CWD 时才写入；若新旧目录冲突，拒绝事件值以保护后续 prompt 可见性判断。
func (s *service) syncAgentLaunchCWD(ctx context.Context, binding *threadBindingRecord, threadID, nextCWD string) error {
	agentID, nextCWD, ok := normalizedAgentLaunchCWD(s, binding, nextCWD)
	if !ok {
		return nil
	}
	prevCWD := strings.TrimSpace(binding.Cwd)
	if comparablePromptCWD(prevCWD) == nextCWD {
		return nil
	}
	if comparablePromptCWD(prevCWD) != "" {
		if s.logger != nil {
			fields := []any{"thread_id", threadID, "agent_id", agentID}
			fields = append(fields, platformshared.SafePathLogFields("stored_cwd", prevCWD)...)
			fields = append(fields, platformshared.SafePathLogFields("event_cwd", nextCWD)...)
			s.logger.Warn("thread: rejected cwd mismatch from agent event", fields...)
		}
		return nil
	}
	store := s.threadBindingStorePort()
	if store == nil {
		return nil
	}
	if promptWorktreeSwitchRequiresInvalidation(prevCWD, nextCWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateWorktree); err != nil {
			return err
		}
	}
	if err := store.UpdateAgentCwd(ctx, threadBindingCWDUpdate{
		AgentID:   agentID,
		Cwd:       nextCWD,
		UpdatedAt: time.Now().UnixMilli(),
	}); err != nil {
		return err
	}
	binding.Cwd = nextCWD
	if s.logger != nil {
		fields := []any{"thread_id", threadID, "agent_id", agentID}
		fields = append(fields, platformshared.SafePathLogFields("cwd", nextCWD)...)
		s.logger.Info("thread: updated cwd from agent event", fields...)
	}
	return nil
}

// normalizedAgentLaunchCWD 校验启动事件具备可写 CWD 的最小条件。
// 返回的目录已按 prompt 比较规则规范化，调用方可直接用于冲突判断和持久化。
func normalizedAgentLaunchCWD(s *service, binding *threadBindingRecord, nextCWD string) (string, string, bool) {
	if s == nil || s.threadBindingStorePort() == nil || binding == nil {
		return "", "", false
	}
	agentID := strings.TrimSpace(binding.AgentID)
	nextCWD = comparablePromptCWD(nextCWD)
	if agentID == "" || nextCWD == "" {
		return "", "", false
	}
	return agentID, nextCWD, true
}

// maxSessionRecoveryAttempts 限制单个 agent 的 session 级恢复次数。
// 被动断线反复出现时停止自动拉起，避免恢复循环占满 provider 和数据库资源。
const maxSessionRecoveryAttempts = 2

// onAgentFailed 将可恢复的被动断线事件交给恢复 worker。
// 非 recoverable 事件不自动重连，避免用户主动停止或归档后的 session 被重新拉起。
func (s *service) onAgentFailed(ev agentdto.AgentFailed) {
	if s == nil || s.sessionRecoveryWorker == nil {
		return
	}
	if !ev.Recoverable {
		return
	}
	agentID := strings.TrimSpace(ev.AgentID)
	threadID := strings.TrimSpace(ev.ThreadID)
	if agentID == "" {
		return
	}
	target := util.FirstNonEmpty(threadID, agentID)
	s.sessionRecoveryWorker.Enqueue(target, ev)
}

// processSessionRecovery 执行单个 agent 的会话恢复流程。
// 它会检查生命周期阻断、限制恢复次数、驱逐旧 session，并在可取消等待窗口后触发后台 Resume。
func (s *service) processSessionRecovery(ctx context.Context, ev agentdto.AgentFailed) {
	if s == nil {
		return
	}
	if !ev.Recoverable {
		return
	}
	agentID := strings.TrimSpace(ev.AgentID)
	threadID := strings.TrimSpace(ev.ThreadID)
	if agentID == "" {
		return
	}
	target := util.FirstNonEmpty(threadID, agentID)
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, target, nil); blocked {
		pkglogger.Info("thread: session recovery skipped by lifecycle",
			"agent_id", agentID,
			"thread_id", target,
			"reason", reason,
		)
		return
	}
	count := s.incrSessionRecoveryCount(agentID)
	if count > maxSessionRecoveryAttempts {
		pkglogger.Warn("thread: onAgentFailed session recovery limit reached",
			"agent_id", agentID,
			"thread_id", threadID,
			"attempts", count,
		)
		return
	}
	pkglogger.Warn("thread: onAgentFailed → session-level recovery",
		"agent_id", agentID,
		"thread_id", target,
		"error", ev.Error,
		"attempt", count,
	)
	s.evictZombieSession(ctx, target)
	select {
	case <-time.After(s.reconnectDelay):
	case <-ctx.Done():
		return
	}
	if reason, blocked := s.resumeLifecycleBlockReason(ctx, target, nil); blocked {
		pkglogger.Info("thread: session recovery resume skipped by lifecycle",
			"agent_id", agentID,
			"thread_id", target,
			"reason", reason,
		)
		return
	}
	s.backgroundResumeIfNeeded(ctx, target)
}

func (s *service) incrSessionRecoveryCount(agentID string) int {
	for {
		val, loaded := s.sessionRecoveryCount.LoadOrStore(agentID, 1)
		if !loaded {
			return 1
		}
		old := val.(int)
		if s.sessionRecoveryCount.CompareAndSwap(agentID, old, old+1) {
			return old + 1
		}
	}
}

func (s *service) resetSessionRecoveryCount(agentID string) {
	s.sessionRecoveryCount.Delete(strings.TrimSpace(agentID))
}

// resolveBindingForEvent 为 provider lifecycle event 找到权威 binding；agentID 优先，缺失时才按 threadID 解析。
func (s *service) resolveBindingForEvent(ctx context.Context, agentID, threadID string) (*threadBindingRecord, error) {
	store := s.threadBindingStorePort()
	if store == nil {
		return nil, nil
	}
	if agentID != "" {
		b, err := store.GetByAgentID(ctx, agentID)
		if err == nil && b != nil {
			return b, nil
		}
		if err != nil && !contract.IsNotFound(err) {
			return nil, err
		}
	}
	if threadID != "" {
		return s.resolveThreadBindingRecord(ctx, threadID)
	}
	return nil, nil
}
