package thread

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
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
		bus.ResilientSubscribe(svc.bus, svc.onAgentLaunched, svc.logger),
		bus.ResilientSubscribe(svc.bus, svc.onAgentFailed, svc.logger),
	}
}

func (s *service) onAgentLaunched(ev agentdto.AgentLaunched) {
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
	if agentID == "" || sessionID == "" || !looksLikeUUID(sessionID) {
		return
	}
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

func (s *service) syncAgentLaunchCWD(ctx context.Context, binding *bindingstore.Binding, threadID, nextCWD string) {
	if s == nil || s.bindingStore == nil || binding == nil {
		return
	}
	agentID := strings.TrimSpace(binding.AgentID)
	nextCWD = comparablePromptCWD(nextCWD)
	if agentID == "" || nextCWD == "" {
		return
	}
	prevCWD := strings.TrimSpace(binding.Cwd)
	if comparablePromptCWD(prevCWD) == nextCWD {
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

// maxSessionRecoveryAttempts limits session-level recovery attempts per agent
// to prevent infinite loops (WS dies → recover → WS dies → recover → ...).
const maxSessionRecoveryAttempts = 2

// onAgentFailed handles passive session death. When the codexapp transport-level
// recovery fails (WS reconnect exhausted), the session dispatches AgentFailed
// with Recoverable=true. We escalate to a full session-level recovery:
// evict the zombie session → wait for Codex thread to finish closing →
// backgroundResumeIfNeeded creates a new WS session and resumes via UUID
// from the binding store (DB is the single source of truth for the UUID).
func (s *service) onAgentFailed(ev agentdto.AgentFailed) {
	if !ev.Recoverable {
		return
	}
	agentID := strings.TrimSpace(ev.AgentID)
	threadID := strings.TrimSpace(ev.ThreadID)
	if agentID == "" {
		return
	}
	// Rate-limit session-level recovery to prevent infinite loops.
	count := s.incrSessionRecoveryCount(agentID)
	if count > maxSessionRecoveryAttempts {
		pkglogger.Warn("thread: onAgentFailed session recovery limit reached",
			"agent_id", agentID,
			"thread_id", threadID,
			"attempts", count,
		)
		return
	}
	target := shared.FirstNonEmpty(threadID, agentID)
	pkglogger.Warn("thread: onAgentFailed → session-level recovery",
		"agent_id", agentID,
		"thread_id", target,
		"error", ev.Error,
		"attempt", count,
	)
	// Evict the dead session so Resume creates a fresh one.
	s.evictZombieSession(context.Background(), target)
	// Give Codex a moment to finish closing the thread — it returns
	// "thread is closing; retry after closed" if we resume too fast.
	shared.SafeGo(s.logger, func() {
		time.Sleep(3 * time.Second)
		s.backgroundResumeIfNeeded(context.Background(), target)
	})
}

// incrSessionRecoveryCount atomically increments and returns the
// session-level recovery count for an agent.
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

// resetSessionRecoveryCount clears the counter, allowing recovery again
// (e.g. after a successful user-initiated Unarchive).
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
