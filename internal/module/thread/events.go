package thread

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
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

func (s *service) onAgentLaunched(ev agentdto.AgentLaunched) {
	if s == nil || s.agentLaunchedWorker == nil || s.bindingStore == nil {
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

func (s *service) processAgentLaunched(ev agentdto.AgentLaunched) {
	if s == nil || s.bindingStore == nil {
		return
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	sessionID := strings.TrimSpace(ev.SessionID)
	ctx := context.Background()
	// Claude system:init may not carry agent_id; resolve from threadID → binding.
	binding, err := s.resolveBindingForEvent(ctx, agentID, threadID)
	if err != nil || binding == nil {
		return
	}
	s.syncAgentLaunchCWD(ctx, binding, threadID, ev.CWD)
	agentID = strings.TrimSpace(binding.AgentID)
	if agentID == "" || sessionID == "" || !identifier.LooksLikeUUID(sessionID) {
		return
	}
	s.recordAgentLaunchSessionUUID(ctx, binding, threadID, agentID, sessionID)
	s.recordAgentLaunchProviderThreadID(ctx, binding, threadID, agentID, sessionID)
}

func (s *service) recordAgentLaunchSessionUUID(ctx context.Context, binding *bindingstore.Binding, threadID, agentID, sessionID string) {
	if strings.TrimSpace(binding.SessionUUID) == sessionID {
		return
	}
	if err := s.bindingStore.UpdateSessionUUID(ctx, bindingstore.UpdateSessionUUIDParams{
		AgentID:     agentID,
		SessionUUID: sessionID,
		UpdatedAt:   time.Now().Unix(),
	}); err != nil {
		s.logger.Warn("thread: update session_uuid from agent event failed", "thread_id", threadID, "agent_id", agentID, "session_uuid", sessionID, "error", err)
		return
	}
	binding.SessionUUID = sessionID
	s.logger.Info("thread: updated session_uuid from agent event", "thread_id", threadID, "agent_id", agentID, "session_uuid", sessionID)
}

func (s *service) recordAgentLaunchProviderThreadID(ctx context.Context, binding *bindingstore.Binding, threadID, agentID, sessionID string) {
	providerThreadID := normalizeProviderThreadID(binding.Provider, sessionID)
	if providerThreadID == "" {
		return
	}
	current := strings.TrimSpace(binding.ProviderThreadID)
	if current == providerThreadID {
		return
	}
	if current != "" && current != agentID && identifier.LooksLikeUUID(current) {
		return
	}
	if !bindingHasProviderHistoryForUUID(binding, providerThreadID) {
		if s.logger != nil {
			s.logger.Info("thread: provider_thread_id from agent event is not recoverable",
				"thread_id", threadID,
				"agent_id", agentID,
				"provider_thread_id", providerThreadID,
				"rollout_path", binding.RolloutPath)
		}
		return
	}
	if err := s.bindingStore.UpdateProviderThreadID(ctx, bindingstore.UpdateProviderThreadIDParams{
		AgentID:          agentID,
		ProviderThreadID: providerThreadID,
		UpdatedAt:        time.Now().Unix(),
	}); err != nil {
		s.logger.Warn("thread: update provider_thread_id from agent event failed", "thread_id", threadID, "agent_id", agentID, "provider_thread_id", providerThreadID, "error", err)
		return
	}
	binding.ProviderThreadID = providerThreadID
	s.logger.Info("thread: updated provider_thread_id from agent event", "thread_id", threadID, "agent_id", agentID, "provider_thread_id", providerThreadID)
}

func (s *service) syncAgentLaunchCWD(ctx context.Context, binding *bindingstore.Binding, threadID, nextCWD string) {
	agentID, nextCWD, ok := normalizedAgentLaunchCWD(s, binding, nextCWD)
	if !ok {
		return
	}
	prevCWD := strings.TrimSpace(binding.Cwd)
	if comparablePromptCWD(prevCWD) == nextCWD {
		return
	}
	if comparablePromptCWD(prevCWD) != "" {
		if s.logger != nil {
			s.logger.Warn("thread: rejected cwd mismatch from agent event", "thread_id", threadID, "agent_id", agentID, "stored_cwd", prevCWD, "event_cwd", nextCWD)
		}
		return
	}
	if err := s.bindingStore.UpdateAgentCwd(ctx, bindingstore.UpdateAgentCwdParams{
		AgentID:   agentID,
		Cwd:       nextCWD,
		UpdatedAt: time.Now().Unix(),
	}); err != nil {
		s.logger.Warn("thread: update cwd from agent event failed", "thread_id", threadID, "agent_id", agentID, "cwd", nextCWD, "error", err)
		return
	}
	binding.Cwd = nextCWD
	if promptWorktreeSwitchRequiresInvalidation(prevCWD, nextCWD, s.cfg) {
		if err := s.invalidatePromptAssembly(ctx, contract.InvalidateWorktree); err != nil {
			s.logger.Warn("thread: prompt invalidate after cwd change failed", "thread_id", threadID, "agent_id", agentID, "cwd", nextCWD, "reason", contract.InvalidateWorktree, "error", err)
		}
	}
	if s.logger != nil {
		s.logger.Info("thread: updated cwd from agent event", "thread_id", threadID, "agent_id", agentID, "cwd", nextCWD)
	}
}

func normalizedAgentLaunchCWD(s *service, binding *bindingstore.Binding, nextCWD string) (string, string, bool) {
	if s == nil || s.bindingStore == nil || binding == nil {
		return "", "", false
	}
	agentID := strings.TrimSpace(binding.AgentID)
	nextCWD = comparablePromptCWD(nextCWD)
	if agentID == "" || nextCWD == "" {
		return "", "", false
	}
	return agentID, nextCWD, true
}

// maxSessionRecoveryAttempts limits session-level recovery attempts per agent
// to prevent infinite loops (WS dies → recover → WS dies → recover → ...).
const maxSessionRecoveryAttempts = 2

// onAgentFailed handles passive session death. When the codexapp
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

func (s *service) resolveBindingForEvent(ctx context.Context, agentID, threadID string) (*bindingstore.Binding, error) {
	if agentID != "" {
		b, err := s.bindingStore.GetByAgentID(ctx, agentID)
		if err == nil && b != nil {
			return b, nil
		}
	}
	if threadID != "" {
		return s.resolveBinding(ctx, threadID)
	}
	return nil, nil
}
