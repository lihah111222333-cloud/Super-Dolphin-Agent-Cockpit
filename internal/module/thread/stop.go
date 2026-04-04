package thread

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

type threadStopState struct {
	agentID   string
	stoppedID string
	targets   []string
	binding   *bindingstore.Binding
}

func (s *service) Stop(ctx context.Context, threadID string) error {
	ctx = shared.NonNilContext(ctx)
	stopState, err := s.resolveThreadStopState(ctx, threadID)
	if err != nil {
		return err
	}
	if err := s.stopThreadRuntime(ctx, stopState, "thread_stopped", false); err != nil {
		return err
	}
	if err := s.updateThreadStatus(ctx, stopState.stoppedID, statusStopped); err != nil {
		return err
	}
	if err := s.cleanupStoppedBinding(ctx, stopState.binding); err != nil {
		return err
	}
	for _, id := range stopState.targets {
		s.forgetThreadAgent(id)
	}
	s.cleanupThreadTurns(ctx, "thread_stopped", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, stopState.agentID, statusStopped, "stopped")
	return nil
}

func (s *service) resolveThreadStopState(ctx context.Context, threadID string) (threadStopState, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return threadStopState{}, err
	}
	return newThreadStopState(binding, threadID), nil
}

func newThreadStopState(binding *bindingstore.Binding, threadID string) threadStopState {
	return threadStopState{
		agentID:   strings.TrimSpace(bindingAgentID(binding)),
		stoppedID: stoppedThreadID(binding, threadID),
		targets:   stopThreadTargets(binding, threadID),
		binding:   binding,
	}
}

func (s *service) stopThreadRuntime(
	ctx context.Context,
	stopState threadStopState,
	source string,
	allowMissingAgent bool,
) error {
	s.interruptStoppingThread(ctx, stopState.agentID, source)
	if err := s.closeSessionForAgent(ctx, stopState.agentID); err != nil {
		return err
	}
	return s.stopManagedAgent(ctx, stopState.agentID, allowMissingAgent)
}

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

func bindingAgentID(binding *bindingstore.Binding) string {
	if binding == nil {
		return ""
	}
	return binding.AgentID
}

func (s *service) stopManagedAgent(ctx context.Context, agentID string, allowMissingAgent bool) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	if s.orchestration == nil {
		if s.sessions != nil {
			s.sessions.RemoveSession(agentID)
		}
		return nil
	}
	err := s.orchestration.StopAgent(ctx, agentID)
	if allowMissingAgent && errors.Is(err, contract.ErrAgentNotFound) {
		return nil
	}
	return err
}

func (s *service) cleanupThreadTurns(ctx context.Context, reason string, threadIDs ...string) {
	if s.turns == nil {
		return
	}
	for _, threadID := range uniqueThreadIDs(threadIDs...) {
		shared.LogIgnoredError(s.logger, "cleanup thread turns failed", s.turns.CleanupThread(ctx, threadID, reason))
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
	return shared.FirstNonEmpty(
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
