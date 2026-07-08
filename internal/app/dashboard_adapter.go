package app

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	"go.uber.org/fx"
)

type dashboardOrchestrationReaderParams struct {
	fx.In

	Reader dashboardOrchestrationReaderPort
}

// newDashboardOrchestrationReader 将 app 本地读端口适配为 dashboard 读接口。
func newDashboardOrchestrationReader(p dashboardOrchestrationReaderParams) dashboard.OrchestrationReader {
	if p.Reader == nil {
		return nil
	}
	return dashboardOrchestrationReader{reader: p.Reader}
}

type dashboardOrchestrationReaderPort interface {
	ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error)
	Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error)
	GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error)
}

type dashboardOrchestrationReaderPortParams struct {
	fx.In

	Service contract.OrchestrationService `optional:"true"`
}

// provideDashboardOrchestrationReaderPort 集中暂存 full service 到 dashboard 读端口的兼容接线。
func provideDashboardOrchestrationReaderPort(p dashboardOrchestrationReaderPortParams) dashboardOrchestrationReaderPort {
	if p.Service == nil {
		return nil
	}
	return p.Service
}

type dashboardOrchestrationReader struct {
	reader dashboardOrchestrationReaderPort
}

// ListAgents 转发 dashboard 需要的 agent 列表读取。
func (r dashboardOrchestrationReader) ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error) {
	return r.reader.ListAgents(ctx)
}

// Snapshot 转发 dashboard 需要的单 agent 快照读取。
func (r dashboardOrchestrationReader) Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error) {
	return r.reader.Snapshot(ctx, agentID)
}

// GetReport 转发 dashboard 需要的 agent 报告读取。
func (r dashboardOrchestrationReader) GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
	return r.reader.GetReport(ctx, agentID)
}
