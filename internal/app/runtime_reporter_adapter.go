package app

import (
	"context"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

// runtimeReporterParams 收集 RuntimeReporter 的可选依赖。
type runtimeReporterParams struct {
	fx.In

	Service contract.OrchestrationService `optional:"true"`
	Logger  *slog.Logger
}

// newRuntimeReporter 为桌面进程提供 runtime report 写入入口。
// orchestration service 可用时写入 UpdateRuntime；未接线时返回 no-op，保证 provider driver 仍能构造。
func newRuntimeReporter(p runtimeReporterParams) contract.RuntimeReporter {
	if p.Service != nil {
		return orchestrationRuntimeReporter{svc: p.Service}
	}
	return noopRuntimeReporter{logger: p.Logger}
}

// orchestrationRuntimeReporter 将 runtime report 转发到 orchestration service。
type orchestrationRuntimeReporter struct {
	svc contract.OrchestrationService
}

// ReportRuntime 写入 orchestration runtime report。
func (r orchestrationRuntimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	return r.svc.UpdateRuntime(ctx, report)
}

// noopRuntimeReporter 在没有 orchestration service 时保留 provider 构造能力。
type noopRuntimeReporter struct {
	logger *slog.Logger
}

// ReportRuntime 只记录 debug 日志，不写持久化状态。
func (r noopRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	if r.logger != nil {
		r.logger.Debug("runtime report (noop)", "agent_id", report.AgentID, "provider", report.Provider)
	}
	return nil
}
