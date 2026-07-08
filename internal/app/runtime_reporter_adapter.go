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

	Updater    runtimeUpdater
	Logger     *slog.Logger              `optional:"true"`
	Dependency contract.DependencyConfig `optional:"true"`
	Config     *contract.Config          `optional:"true"`
}

type runtimeUpdater interface {
	UpdateRuntime(ctx context.Context, report contract.RuntimeReport) error
}

type runtimeUpdaterParams struct {
	fx.In

	Service contract.OrchestrationService `optional:"true"`
}

// provideRuntimeUpdater 集中暂存 full service 到 runtime 更新端口的兼容接线。
func provideRuntimeUpdater(p runtimeUpdaterParams) runtimeUpdater {
	if p.Service == nil {
		return nil
	}
	return p.Service
}

// newRuntimeReporter 为桌面进程提供 runtime report 写入入口。
// orchestration service 可用时写入 UpdateRuntime；未接线时按 dependency profile 决定 fail-fast 或 deferred。
func newRuntimeReporter(p runtimeReporterParams) (contract.RuntimeReporter, error) {
	if p.Updater != nil {
		return orchestrationRuntimeReporter{updater: p.Updater}, nil
	}
	profile, err := appDependencyProfile(p.Dependency, p.Config)
	if err != nil {
		return nil, err
	}
	policy := newDependencyContract(profile)
	if err := policy.Require("runtime_reporter.orchestration_service", p.Updater); err != nil {
		return nil, err
	}
	return desktopExternalRuntimeReporter{logger: p.Logger, profile: profile}, nil
}

// orchestrationRuntimeReporter 将 runtime report 转发到 orchestration service。
type orchestrationRuntimeReporter struct {
	updater runtimeUpdater
}

// ReportRuntime 写入 orchestration runtime report。
func (r orchestrationRuntimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	return r.updater.UpdateRuntime(ctx, report)
}

// desktopExternalRuntimeReporter 明确表示 runtime report 由外部 orchestration 承接。
type desktopExternalRuntimeReporter struct {
	logger  *slog.Logger
	profile contract.DependencyProfile
}

// ReportRuntime 返回 typed deferred，调用方必须按 dependency profile 处理。
func (r desktopExternalRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	if r.logger != nil {
		r.logger.Debug("runtime report deferred to external orchestration", "agent_id", report.AgentID, "provider", report.Provider)
	}
	return dependencyDeferred("runtime_reporter.orchestration_service", r.profile)
}
