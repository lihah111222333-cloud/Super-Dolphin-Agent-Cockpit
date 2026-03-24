package thread

import (
	"context"
	"strings"
	"time"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

func (s *service) Stop(ctx context.Context, threadID string) error {
	ctx = normalizeThreadContext(ctx)
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return err
	}
	agentID := strings.TrimSpace(binding.AgentID)
	targets := stopThreadTargets(binding, threadID)
	stoppedID := stoppedThreadID(binding, threadID)
	s.interruptStoppingThread(ctx, agentID)
	if err := s.closeSessionIfActive(ctx, stoppedID); err != nil {
		return err
	}
	if err := s.stopManagedAgent(ctx, agentID); err != nil {
		return err
	}
	if err := s.updateThreadStatus(ctx, stoppedID, statusStopped); err != nil {
		return err
	}
	if err := s.cleanupStoppedBinding(ctx, binding); err != nil {
		return err
	}
	for _, id := range uniqueThreadIDs(targets...) {
		s.forgetThreadAgent(id)
	}
	s.cleanupThreadTurns(ctx, "thread_stopped", targets...)
	s.publishThreadStopped(stoppedID, agentID, statusStopped, "stopped")
	return nil
}

func (s *service) interruptStoppingThread(ctx context.Context, agentID string) {
	if s.turns == nil {
		return
	}
	session, err := s.lookupSession(agentID)
	if err != nil || session == nil {
		return
	}
	if err := s.turns.InterruptActiveTurn(ctx, session, "thread_stopped"); err != nil && s.logger != nil {
		s.logger.Warn("thread stop: interrupt active turn failed", "agent_id", agentID, "error", err)
	}
}

func (s *service) stopManagedAgent(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if s.orchestration == nil {
		if s.sessions != nil && agentID != "" {
			s.sessions.RemoveSession(agentID)
		}
		return nil
	}
	return s.orchestration.StopAgent(ctx, agentID)
}

func (s *service) cleanupThreadTurns(ctx context.Context, reason string, threadIDs ...string) {
	if s.turns == nil {
		return
	}
	for _, threadID := range uniqueThreadIDs(threadIDs...) {
		_ = s.turns.CleanupThread(ctx, threadID, reason)
	}
}

func stopThreadTargets(binding *bindingstore.Binding, threadID string) []string {
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

func stoppedThreadID(binding *bindingstore.Binding, threadID string) string {
	if binding == nil {
		return strings.TrimSpace(threadID)
	}
	return firstNonEmpty(
		binding.CodexThreadID,
		threadID,
		binding.ProviderThreadID,
		binding.AgentID,
	)
}

func (s *service) cleanupStoppedBinding(ctx context.Context, binding *bindingstore.Binding) error {
	if s.bindingStore == nil || binding == nil {
		return nil
	}
	agentID := strings.TrimSpace(binding.AgentID)
	if agentID == "" {
		return nil
	}
	return s.bindingStore.UpdateSessionUUID(ctx, bindingstore.UpdateSessionUUIDParams{
		AgentID:     agentID,
		SessionUUID: "",
		UpdatedAt:   time.Now().Unix(),
	})
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
