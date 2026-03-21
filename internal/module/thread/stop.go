package thread

import (
	"context"
	"strings"

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
	s.interruptStoppingThread(ctx, agentID)
	if err := s.stopManagedAgent(ctx, agentID); err != nil {
		return err
	}
	s.cleanupThreadTurns(ctx, "thread_stopped", targets...)
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
