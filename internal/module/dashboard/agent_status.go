package dashboard

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ListAgentStatuses 列出代理statuses。
func (s *service) ListAgentStatuses(ctx context.Context, status string) ([]contract.AgentStatus, error) {
	return safeList(s.agentStatuses != nil, func() ([]contract.AgentStatus, error) {
		return s.agentStatuses.List(ctx, strings.TrimSpace(status))
	})
}
