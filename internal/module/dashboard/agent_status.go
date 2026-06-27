package dashboard

import (
	"context"
	"strings"
)

// ListAgentStatuses 按状态读取 agent status store。
// store 缺失时返回空切片，保持 dashboard agent 状态卡可选。
func (s *service) ListAgentStatuses(ctx context.Context, status string) ([]AgentStatus, error) {
	return safeList(s.agentStatuses != nil, func() ([]AgentStatus, error) {
		return s.agentStatuses.List(ctx, strings.TrimSpace(status))
	})
}
