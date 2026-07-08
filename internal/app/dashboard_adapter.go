package app

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	"go.uber.org/fx"
)

type dashboardOrchestrationReaderParams struct {
	fx.In

	Service contract.OrchestrationService `optional:"true"`
}

// newDashboardOrchestrationReader 将完整 orchestration service 收窄为 dashboard 读端口。
func newDashboardOrchestrationReader(p dashboardOrchestrationReaderParams) dashboard.OrchestrationReader {
	if p.Service == nil {
		return nil
	}
	return dashboardOrchestrationReader{service: p.Service}
}

type dashboardOrchestrationReader struct {
	service contract.OrchestrationService
}

// ListAgents 转发 dashboard 需要的 agent 列表读取。
func (r dashboardOrchestrationReader) ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error) {
	return r.service.ListAgents(ctx)
}

// Snapshot 转发 dashboard 需要的单 agent 快照读取。
func (r dashboardOrchestrationReader) Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error) {
	return r.service.Snapshot(ctx, agentID)
}

// GetReport 转发 dashboard 需要的 agent 报告读取。
func (r dashboardOrchestrationReader) GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
	return r.service.GetReport(ctx, agentID)
}
