package dashboard

import (
	"context"
	"strings"

	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
)

// ListAgentStatuses 列出代理statuses。
func (s *service) ListAgentStatuses(ctx context.Context, status string) ([]agentstatusstore.AgentStatus, error) {
	return safeList(s.agentStatuses != nil, func() ([]agentstatusstore.AgentStatus, error) {
		return s.agentStatuses.List(ctx, strings.TrimSpace(status))
	})
}
