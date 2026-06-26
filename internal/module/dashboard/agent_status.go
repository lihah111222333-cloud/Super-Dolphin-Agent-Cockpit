package dashboard

import (
	"context"
	"strings"

	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
)

// ListAgentStatuses 按状态读取 agent status store。
// store 缺失时返回空切片，保持 dashboard agent 状态卡可选。
func (s *service) ListAgentStatuses(ctx context.Context, status string) ([]agentstatusstore.AgentStatus, error) {
	return safeList(s.agentStatuses != nil, func() ([]agentstatusstore.AgentStatus, error) {
		return s.agentStatuses.List(ctx, strings.TrimSpace(status))
	})
}
