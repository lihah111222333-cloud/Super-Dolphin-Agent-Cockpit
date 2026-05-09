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
		contract.ResilientSubscribe(svc.bus, svc.onTurnCompleted, svc.logger),
	}
}

// startBusWorkers starts every worker that owns a bus-callback slow-path
// for the thread service. P22 P2 (thread S3+) wires the workers into the
// subscription lifecycle hook so callbacks can enqueue from OnStart's
// first tick without racing a yet-to-be-started runWorker goroutine.
func (s *service) startBusWorkers() {
	if s == nil {
		return
	}
	if s.taskHandoffWorker != nil {
		s.taskHandoffWorker.Start()
	}
	if s.agentLaunchedWorker != nil {
		s.agentLaunchedWorker.Start()
	}
	if s.sessionRecoveryWorker != nil {
		s.sessionRecoveryWorker.Start()
	}
}

// stopBusWorkers drains every bus-callback worker bounded by ctx. The
// subscription cancel must have already fired so no new Enqueues arrive;
// any pending entries are processed before the worker goroutine exits.
func (s *service) stopBusWorkers(ctx context.Context) {
	if s == nil {
		return
	}
	if s.taskHandoffWorker != nil {
		s.drainBusWorker(ctx, "task handoff worker", s.taskHandoffWorker.Stop)
	}
	if s.agentLaunchedWorker != nil {
		s.drainBusWorker(ctx, "agent launched worker", s.agentLaunchedWorker.Stop)
	}
	if s.sessionRecoveryWorker != nil {
		s.drainBusWorker(ctx, "session recovery worker", s.sessionRecoveryWorker.Stop)
	}
}

// drainBusWorker invokes stop(ctx) and warn-logs the error on the
// service logger. Extracted so stopBusWorkers stays flat enough to pass
// the archtest CC-size guard as new workers land (thread S2/S3/S4+).
func (s *service) drainBusWorker(ctx context.Context, name string, stop func(context.Context) error) {
	if err := stop(ctx); err != nil && s.logger != nil {
		s.logger.Warn("thread: "+name+" drain failed", "error", err)
	}
}

// onAgentLaunched is the bus callback for agentdto.AgentLaunched. P22 P2
// thread S4: the callback is cheap Enqueue only. The
// agentLaunchedWorker owns the slow-path (resolveBindingForEvent,
// syncAgentLaunchCWD, bindingStore.UpdateSessionUUID, prompt-assembly
// Invalidate). The coalesce key is agentID when present, falling back
// to threadID so Claude's system:init event — which omits agent_id on
// first turn — still dedupes correctly.
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

// processAgentLaunched carries the pre-P22 inline body of
// onAgentLaunched: resolve the binding (by agentID or threadID),
// sync the launch CWD (possibly invalidating the prompt-assembly cache
// on worktree switch), then persist the session UUID when it changed.
// Invoked exclusively by agentLaunchedWorker.drainPending after the bus
// callback has enqueued the event.
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
	agentID, nextCWD, ok := normalizedAgentLaunchCWD(s, binding, nextCWD)
	if !ok {
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
// transport-level recovery fails (WS reconnect exhausted), the session
// dispatches AgentFailed with Recoverable=true. P22 P2 thread S2: the
// callback only computes the coalesce target and Enqueues into the
// sessionRecoveryWorker, which owns the slow-path (rate-limit, evict
// zombie, ctx-aware 3s reconnect delay, backgroundResumeIfNeeded). The
// pre-P22 naked runtimesafe.SafeGo(context.Background(), ...) +
// time.Sleep is gone; Stop drains pending + waits for every recovery
// goroutine to observe its ctx cancellation.
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
	// Match processSessionRecovery's target computation so coalesce
	// key == the identifier the worker will hand to evict / resume.
	target := util.FirstNonEmpty(threadID, agentID)
	s.sessionRecoveryWorker.Enqueue(target, ev)
}

// processSessionRecovery carries the pre-P22 onAgentFailed body (minus
// the outer SafeGo + time.Sleep, which the worker now owns): rate-limit
// the agent, evict the zombie session, wait for Codex to close the
// thread (ctx-aware so Stop short-circuits), then call
// backgroundResumeIfNeeded. Invoked exclusively by
// sessionRecoveryWorker.drainPending on a tracked goroutine.
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
	pkglogger.Warn("thread: onAgentFailed → session-level recovery",
		"agent_id", agentID,
		"thread_id", target,
		"error", ev.Error,
		"attempt", count,
	)
	// Evict the dead session so Resume creates a fresh one.
	s.evictZombieSession(ctx, target)
	// Give Codex a moment to finish closing the thread — it returns
	// "thread is closing; retry after closed" if we resume too fast. The
	// sleep is ctx-aware so sessionRecoveryWorker.Stop short-circuits
	// instead of blocking shutdown for the full 3 seconds.
	select {
	case <-time.After(sessionRecoveryReconnectDelay):
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
