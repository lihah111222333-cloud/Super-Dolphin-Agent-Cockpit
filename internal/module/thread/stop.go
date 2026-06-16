package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

type threadStopState struct {
	agentID   string
	stoppedID string
	targets   []string
	binding   *contract.Binding
}

var errResumeLifecycleBlocked = errors.New("thread resume blocked by lifecycle state")

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

func (s *service) unblockResumeForThread(ctx context.Context, threadID string) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		s.unblockResumeForAgent(threadID)
		return
	}
	s.unblockResumeForAgent(binding.AgentID)
}

func (s *service) resetSessionRecoveryForThread(ctx context.Context, threadID string) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		s.resetSessionRecoveryCount(threadID)
		return
	}
	s.resetSessionRecoveryCount(binding.AgentID)
}

func (s *service) resumeLifecycleBlockReason(
	ctx context.Context,
	threadID string,
	binding *contract.Binding,
) (string, bool) {
	binding = s.resolveResumeLifecycleBinding(ctx, threadID, binding)
	if reason, blocked := s.resumeAgentLifecycleBlock(threadID, binding); blocked {
		return reason, true
	}
	if status, ok := s.resumeLifecycleThreadStatus(ctx, threadID, binding); ok {
		return resumeLifecycleStatusBlock(status)
	}
	return "", false
}

func (s *service) resolveResumeLifecycleBinding(
	ctx context.Context,
	threadID string,
	binding *contract.Binding,
) *contract.Binding {
	if s == nil || binding != nil || s.bindingStore == nil {
		return binding
	}
	resolved, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return unresolvedStopBinding()
	}
	return resolved
}

func unresolvedStopBinding() *contract.Binding {
	return nil
}

// resumeAgentLifecycleBlock 处理恢复代理生命周期block。
func (s *service) resumeAgentLifecycleBlock(threadID string, binding *contract.Binding) (string, bool) {
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

func resumeLifecycleStatusBlock(status string) (string, bool) {
	switch strings.TrimSpace(status) {
	case statusArchived:
		return "thread_archived", true
	case statusStopped:
		return "thread_stopped", true
	default:
		return "", false
	}
}

// resumeLifecycleThreadStatus 处理恢复生命周期线程状态。
func (s *service) resumeLifecycleThreadStatus(
	ctx context.Context,
	threadID string,
	binding *contract.Binding,
) (string, bool) {
	if s == nil || s.threadStore == nil {
		return "", false
	}
	for _, id := range resumeLifecycleThreadIDs(threadID, binding) {
		thread, err := s.threadStore.GetByThreadID(ctx, id)
		if err != nil || thread == nil {
			continue
		}
		return strings.TrimSpace(thread.Status), true
	}
	return "", false
}

func resumeLifecycleThreadIDs(threadID string, binding *contract.Binding) []string {
	candidates := []string{strings.TrimSpace(threadID)}
	if binding != nil {
		candidates = append(candidates,
			strings.TrimSpace(binding.CodexThreadID),
			strings.TrimSpace(binding.AgentID),
		)
	}
	return uniqueResumeLifecycleIDs(candidates)
}

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

func resumeLifecycleError(threadID, reason string) error {
	return fmt.Errorf("%w: %s for %q", errResumeLifecycleBlocked, reason, strings.TrimSpace(threadID))
}

// Stop 停止线程流程。
func (s *service) Stop(ctx context.Context, threadID string) error {
	ctx = kernel.NonNilContext(ctx)
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
	if err := s.stopThreadRuntime(ctx, stopState, "thread_stopped", false); err != nil {
		return err
	}
	if err := s.updateThreadStatus(ctx, stopState.stoppedID, statusStopped); err != nil {
		return err
	}
	s.cleanupThreadScratchpad(ctx, stopState.stoppedID, stopState.binding)
	for _, id := range stopState.targets {
		s.forgetThreadAgent(id)
	}
	s.cleanupThreadTurns(ctx, "thread_stopped", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, stopState.agentID, statusStopped, "stopped")
	return nil
}

// stopPendingLaunchThread 停止待处理启动线程。
func (s *service) stopPendingLaunchThread(ctx context.Context, threadID string) (bool, error) {
	if s.threadStore == nil {
		return false, nil
	}
	trimmed := strings.TrimSpace(threadID)
	if trimmed == "" {
		return false, nil
	}
	mu := s.acquirePendingLaunchLock(trimmed)
	mu.Lock()
	defer mu.Unlock()
	row, err := s.threadStore.GetByThreadID(ctx, trimmed)
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

func (s *service) resolveThreadStopState(ctx context.Context, threadID string) (threadStopState, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return threadStopState{}, err
	}
	return newThreadStopState(binding, threadID), nil
}

func newThreadStopState(binding *contract.Binding, threadID string) threadStopState {
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
) error {
	pkglogger.Info("thread: stopThreadRuntime ENTERED",
		"agent_id", stopState.agentID,
		"stopped_id", stopState.stoppedID,
		"source", source,
		"allow_missing_agent", allowMissingAgent,
		"caller", archiveCallerStack(),
	)
	s.blockResumeForAgent(stopState.agentID)
	s.interruptStoppingThread(ctx, stopState.agentID, source)
	localSessionGone := false
	if err := s.closeSessionForAgent(ctx, stopState.agentID); err != nil {
		if !errors.Is(err, errLocalSessionAlreadyGone) {
			s.unblockResumeForAgent(stopState.agentID)
			pkglogger.Warn("thread: stopThreadRuntime closeSession FAILED",
				"agent_id", stopState.agentID,
				"error", err,
			)
			return err
		}
		localSessionGone = true
	}
	err := s.stopManagedAgent(ctx, stopState.agentID, allowMissingAgent || localSessionGone)
	if err != nil {
		s.unblockResumeForAgent(stopState.agentID)
		pkglogger.Warn("thread: stopThreadRuntime DONE with error",
			"agent_id", stopState.agentID,
			"error", err,
		)
	} else {
		s.unblockResumeForAgent(stopState.agentID)
	}
	return err
}

// interruptStoppingThread 处理interruptstopping线程。
func (s *service) interruptStoppingThread(ctx context.Context, agentID, source string) {
	if s.turns == nil {
		return
	}
	session, err := s.lookupSession(agentID)
	if err != nil || session == nil {
		return
	}
	if err := s.turns.InterruptActiveTurn(ctx, session, source); err != nil && s.logger != nil {
		s.logger.Warn("thread stop: interrupt active turn failed", "agent_id", agentID, "error", err)
	}
}

func bindingAgentID(binding *contract.Binding) string {
	if binding == nil {
		return ""
	}
	return binding.AgentID
}

// stopManagedAgent 停止managed代理。
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

func (s *service) cleanupThreadTurns(ctx context.Context, reason string, threadIDs ...string) {
	if s.turns == nil {
		return
	}
	for _, threadID := range uniqueThreadIDs(threadIDs...) {
		kernel.LogIgnoredError(s.logger, "cleanup thread turns failed", s.turns.CleanupThread(ctx, threadID, reason))
	}
}

func stopThreadTargets(binding *contract.Binding, threadID string) []string {
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

func stoppedThreadID(binding *contract.Binding, threadID string) string {
	if binding == nil {
		return strings.TrimSpace(threadID)
	}
	return kernel.FirstNonEmpty(
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
