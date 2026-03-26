package dashboard

import (
	"context"
	"strings"

	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
)

func (s *service) ListAgentStatuses(ctx context.Context, status string) ([]agentstatusstore.AgentStatus, error) {
	if s.agentStatuses == nil {
		return []agentstatusstore.AgentStatus{}, nil
	}
	return s.agentStatuses.List(ctx, strings.TrimSpace(status))
}
