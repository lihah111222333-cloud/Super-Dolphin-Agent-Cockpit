package thread

import (
	"context"
	"errors"
	"strings"
)

type sessionGenerationProvider interface {
	SessionGeneration(agentID string) uint64
}

func (s *service) bindSessionGeneration(ctx context.Context, agentID string) error {
	if s.orchestration == nil || s.sessions == nil {
		return nil
	}
	provider, ok := s.sessions.(sessionGenerationProvider)
	if !ok {
		return nil
	}
	generation := provider.SessionGeneration(agentID)
	if generation == 0 {
		return errors.New("session generation is not available")
	}
	return s.orchestration.BindSessionGeneration(ctx, strings.TrimSpace(agentID), generation)
}
