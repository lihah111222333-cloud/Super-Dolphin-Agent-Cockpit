package orchestration

import (
	"context"
	"errors"
	"strings"
)

type generationAwareSessionCleaner interface {
	RemoveSessionGeneration(agentID string, generation uint64)
}

func (s *service) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(strings.TrimSpace(agentID))
	if err != nil {
		return err
	}
	if generation == 0 {
		return errors.New("session generation is required")
	}
	agent.sessionGeneration = generation
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	return nil
}

func (s *service) removeSession(agent *agentRuntime) {
	if s.sessionCleaner == nil || agent == nil {
		return
	}
	if cleaner, ok := s.sessionCleaner.(generationAwareSessionCleaner); ok {
		if agent.sessionGeneration != 0 {
			cleaner.RemoveSessionGeneration(agent.id, agent.sessionGeneration)
			agent.sessionGeneration = 0
		}
		return
	}
	s.sessionCleaner.RemoveSession(agent.id)
	agent.sessionGeneration = 0
}
