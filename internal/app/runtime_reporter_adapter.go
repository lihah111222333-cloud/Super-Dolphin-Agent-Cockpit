package app

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

type runtimeReporterParams struct {
	fx.In

	Service contract.OrchestrationService `optional:"true"`
	Logger  *pkglogger.Logger
}

// newRuntimeReporter provides a contract.RuntimeReporter for the desktop app.
// When the full orchestration service is available, runtime reports are forwarded
// to it (UpdateRuntime). Otherwise a no-op implementation is used so that the
// provider driver factories (claudecli, codexapp) can still be constructed.
func newRuntimeReporter(p runtimeReporterParams) contract.RuntimeReporter {
	if p.Service != nil {
		return orchestrationRuntimeReporter{svc: p.Service}
	}
	return noopRuntimeReporter{logger: p.Logger}
}

type orchestrationRuntimeReporter struct {
	svc contract.OrchestrationService
}

// ReportRuntime 报告运行时。
func (r orchestrationRuntimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	return r.svc.UpdateRuntime(ctx, report)
}

type noopRuntimeReporter struct {
	logger *pkglogger.Logger
}

// ReportRuntime 报告运行时。
func (r noopRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	if r.logger != nil {
		r.logger.Debug("runtime report (noop)", "agent_id", report.AgentID, "provider", report.Provider)
	}
	return nil
}
