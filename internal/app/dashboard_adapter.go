package app

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	"go.uber.org/fx"
)

type dashboardOrchestrationReaderParams struct {
	fx.In

	Reader dashboardOrchestrationSnapshotReaderPort
}

// newDashboardOrchestrationReader 将 app 本地读端口适配为 dashboard 读接口。
func newDashboardOrchestrationReader(p dashboardOrchestrationReaderParams) dashboard.OrchestrationReader {
	if p.Reader == nil {
		return nil
	}
	return dashboardOrchestrationReader{reader: p.Reader}
}

type dashboardOrchestrationReportReaderParams struct {
	fx.In

	Reader dashboardOrchestrationReportReaderPort
}

// newDashboardOrchestrationReportReader 将 app 本地报告端口适配为 dashboard 报告接口。
func newDashboardOrchestrationReportReader(p dashboardOrchestrationReportReaderParams) dashboard.OrchestrationReportReader {
	if p.Reader == nil {
		return nil
	}
	return dashboardOrchestrationReportReader{reader: p.Reader}
}

type dashboardOrchestrationSnapshotReaderPort interface {
	ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error)
	Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error)
}

type dashboardOrchestrationReportReaderPort interface {
	GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error)
}

type dashboardOrchestrationReaderPortParams struct {
	fx.In

	Lifecycle contract.AgentLifecyclePort `optional:"true"`
}

// provideDashboardOrchestrationReaderPort 暴露 dashboard 需要的 agent 生命周期读端口。
func provideDashboardOrchestrationReaderPort(p dashboardOrchestrationReaderPortParams) dashboardOrchestrationSnapshotReaderPort {
	if p.Lifecycle == nil {
		return nil
	}
	return dashboardOrchestrationReaderPortAdapter{
		lifecycle: p.Lifecycle,
	}
}

type dashboardOrchestrationReportReaderPortParams struct {
	fx.In

	Reports contract.AgentReportPort `optional:"true"`
}

// provideDashboardOrchestrationReportReaderPort 暴露 dashboard 需要的 report 读取端口。
func provideDashboardOrchestrationReportReaderPort(p dashboardOrchestrationReportReaderPortParams) dashboardOrchestrationReportReaderPort {
	if p.Reports == nil {
		return nil
	}
	return dashboardOrchestrationReportReaderPortAdapter{reports: p.Reports}
}

type uiStateAgentListerParams struct {
	fx.In

	Reader dashboardOrchestrationSnapshotReaderPort
}

// provideUIStateAgentLister 将 app 本地读端口收窄为 uistate 首屏 agent 列表端口。
func provideUIStateAgentLister(p uiStateAgentListerParams) uistate.AgentLister {
	if p.Reader == nil {
		return nil
	}
	return p.Reader
}

type dashboardOrchestrationReader struct {
	reader dashboardOrchestrationSnapshotReaderPort
}

type dashboardOrchestrationReportReader struct {
	reader dashboardOrchestrationReportReaderPort
}

type dashboardOrchestrationReaderPortAdapter struct {
	lifecycle contract.AgentLifecyclePort
}

type dashboardOrchestrationReportReaderPortAdapter struct {
	reports contract.AgentReportPort
}

// ListAgents 从 agent 生命周期端口读取列表快照。
func (a dashboardOrchestrationReaderPortAdapter) ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error) {
	return a.lifecycle.ListAgents(ctx)
}

// Snapshot 从 agent 生命周期端口读取单 agent 快照。
func (a dashboardOrchestrationReaderPortAdapter) Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error) {
	return a.lifecycle.Snapshot(ctx, agentID)
}

// GetReport 从 agent report 端口读取报告。
func (a dashboardOrchestrationReportReaderPortAdapter) GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
	return a.reports.GetReport(ctx, agentID)
}

// ListAgents 转发 UI 读模型需要的 agent 列表读取。
func (r dashboardOrchestrationReader) ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error) {
	return r.reader.ListAgents(ctx)
}

// Snapshot 转发 dashboard 需要的单 agent 快照读取。
func (r dashboardOrchestrationReader) Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error) {
	return r.reader.Snapshot(ctx, agentID)
}

// GetReport 转发 dashboard 需要的 agent 报告读取。
func (r dashboardOrchestrationReportReader) GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
	return r.reader.GetReport(ctx, agentID)
}
