package claudecli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

// driverFactoryParams 收集 Claude driver factory 的 fx 依赖。
type driverFactoryParams struct {
	fx.In

	Logger       *slog.Logger
	Dispatcher   *unified.EventDispatcher
	Reporter     contract.RuntimeReporter
	Dependency   contract.DependencyConfig `optional:"true"`
	Config       *contract.Config          `optional:"true"`
	Reg          *pidregistry.Registry
	ProxyAddrFn  func() string `name:"proxy_addr_fn"`
	ProxyTokenFn func() string `name:"proxy_token_fn"`
	Mirror       contract.SkillMirrorReconciler
	Recovery     contract.SessionRecoveryReporter `optional:"true"`
	Tracer       *observability.Service           `optional:"true"`
}

// NewDriverFactory 构造 Claude provider 的 DriverFactory。
// NativeTools 在这里声明默认禁用策略，防止 provider-native 工具绕过项目治理入口。
func NewDriverFactory(p driverFactoryParams) contract.DriverFactory {
	return contract.DriverFactory{
		Name: "claude",
		Create: func() contract.Driver {
			return newDriver(p.Logger, p.Dispatcher, p.Reporter, p.Reg, p.ProxyAddrFn, p.ProxyTokenFn, p.Mirror, p.Recovery, p.Tracer)
		},
		NativeTools: []contract.NativeToolDescriptor{
			{ID: "Read", Label: "直接读项目文件", Description: "绕过项目文件工具直接读取工作区文件。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Write", Label: "直接新建文件", Description: "绕过项目文件编辑链路直接创建文件。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Edit", Label: "直接改文件", Description: "绕过项目补丁和审查链路直接修改文件。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "MultiEdit", Label: "批量改文件", Description: "绕过项目补丁和审查链路批量修改文件。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Bash", Label: "直接执行命令", Description: "绕过项目命令治理直接执行本地命令。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "BashOutput", Label: "查看命令输出", Description: "绕过项目命令记录直接读取后台命令输出。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "KillShell", Label: "停止命令", Description: "绕过项目命令治理直接终止后台命令。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Grep", Label: "直接搜代码", Description: "绕过项目 LSP 和搜索工具直接查找代码。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Glob", Label: "直接匹配文件", Description: "绕过项目文件工具直接按规则查找文件。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "LS", Label: "直接列目录", Description: "绕过项目文件工具直接读取目录内容。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Agent", Label: "启动临时子任务", Description: "让 Claude 自己派生临时子任务；本项目已有任务编排。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "AskUserQuestion", Label: "向用户提问", Description: "绕过项目对话流直接向用户发起提问。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "CronCreate", Label: "创建定时任务", Description: "绕过项目定时任务入口直接创建任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "CronDelete", Label: "删除定时任务", Description: "绕过项目定时任务入口直接删除任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "CronList", Label: "查看定时任务", Description: "绕过项目定时任务入口直接查看任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "EnterPlanMode", Label: "进入计划模式", Description: "绕过项目计划视图进入 Claude 自带计划模式。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ExitPlanMode", Label: "退出计划模式", Description: "绕过项目计划视图退出 Claude 自带计划模式。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "EnterWorktree", Label: "进入工作树", Description: "绕过项目工作区管理创建或进入工作树。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ExitWorktree", Label: "退出工作树", Description: "绕过项目工作区管理退出工作树。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Monitor", Label: "查看 Claude 后台事件", Description: "查看 Claude 自带后台任务事件流。", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "WebFetch", Label: "抓取网页", Description: "按 URL 拉取网页内容", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "WebSearch", Label: "网页搜索", Description: "调用内置网页搜索", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TodoWrite", Label: "自行更新待办", Description: "绕过项目计划和任务视图写入 Claude 自带待办清单。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "NotebookEdit", Label: "编辑 Jupyter 笔记本", Description: "编辑 Jupyter Notebook 文件。", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "NotebookRead", Label: "读取 Jupyter 笔记本", Description: "读取 Jupyter Notebook 文件。", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ListMcpResources", Label: "列出外部资源", Description: "绕过项目工具面直接读取 MCP 资源列表。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ReadMcpResource", Label: "读取外部资源", Description: "绕过项目工具面直接读取 MCP 资源内容。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "PushNotification", Label: "推送通知", Description: "绕过项目通知入口直接发送通知。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "RemoteTrigger", Label: "远程触发任务", Description: "绕过项目任务入口直接触发远程任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ScheduleWakeup", Label: "计划唤醒", Description: "绕过项目定时任务入口直接安排唤醒。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "SendUserFile", Label: "发送文件给用户", Description: "绕过项目消息流直接发送文件给用户。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "SendUserMessage", Label: "发送消息给用户", Description: "绕过项目消息流直接发送消息给用户。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "SendMessage", Label: "发送远程消息", Description: "绕过项目消息流直接发送团队或远程控制消息。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Skill", Label: "调用技能", Description: "允许 Claude 使用 provider-native 技能发现与调用。", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Task", Label: "启动后台任务", Description: "让 Claude 自己启动后台任务；本项目已有任务编排。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TaskCreate", Label: "创建后台任务", Description: "绕过项目任务编排直接创建后台任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TaskGet", Label: "读取后台任务", Description: "绕过项目任务视图直接读取后台任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TaskList", Label: "列出后台任务", Description: "绕过项目任务视图直接列出后台任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TaskOutput", Label: "读取任务输出", Description: "绕过项目任务视图直接读取后台任务输出。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TaskStop", Label: "停止后台任务", Description: "绕过项目任务治理直接停止后台任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TaskUpdate", Label: "更新后台任务", Description: "绕过项目任务视图直接更新后台任务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TeamCreate", Label: "创建 Claude 团队空间", Description: "创建 Claude 内置团队空间。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TeamDelete", Label: "删除 Claude 团队空间", Description: "删除 Claude 内置团队空间。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ToolSearch", Label: "自行发现工具", Description: "绕过项目工具清单自行发现可用工具。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "WaitForMcpServers", Label: "等待外部工具服务", Description: "绕过项目工具服务生命周期直接等待 MCP 服务。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Advisor", Label: "Claude 内置审查", Description: "调用 Claude 自带审查能力。", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ShareOnboardingGuide", Label: "分享 Claude 入门指南", Description: "分享 Claude 自带入门指南。", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
		},
	}

}

func provideDriverFactory(p driverFactoryParams) (contract.DriverFactory, error) {
	reporter, err := newModeAwareRuntimeReporter(p.Reporter, p.Dependency, p.Config, p.Logger, "claude")
	if err != nil {
		return contract.DriverFactory{}, err
	}
	p.Reporter = reporter
	return NewDriverFactory(p), nil
}

type runtimeReportDeferredStatus struct {
	AgentID    string
	Provider   string
	Port       int
	Dependency string
	Profile    contract.DependencyProfile
	Status     string
}

type modeAwareRuntimeReporter struct {
	base     contract.RuntimeReporter
	logger   *slog.Logger
	profile  contract.DependencyProfile
	provider string

	mu       sync.Mutex
	deferred map[string]runtimeReportDeferredStatus
}

func newModeAwareRuntimeReporter(base contract.RuntimeReporter, dependency contract.DependencyConfig, cfg *contract.Config, logger *slog.Logger, provider string) (*modeAwareRuntimeReporter, error) {
	if base == nil {
		return nil, fmt.Errorf("%s runtime reporter is required", strings.TrimSpace(provider))
	}
	profile, err := dependencyProfileFromFactoryParams(dependency, cfg, provider)
	if err != nil {
		return nil, err
	}
	return &modeAwareRuntimeReporter{
		base:     base,
		logger:   logger,
		profile:  profile,
		provider: strings.TrimSpace(provider),
		deferred: map[string]runtimeReportDeferredStatus{},
	}, nil
}

func dependencyProfileFromFactoryParams(dependency contract.DependencyConfig, cfg *contract.Config, provider string) (contract.DependencyProfile, error) {
	profile := dependency.Profile
	if strings.TrimSpace(string(profile)) == "" && cfg != nil {
		profile = cfg.Dependency.Profile
	}
	switch profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileProduction, contract.DependencyProfileTest:
		return profile, nil
	case "":
		return "", fmt.Errorf("%s dependency profile is required", strings.TrimSpace(provider))
	default:
		return "", fmt.Errorf("%s dependency profile %q is not supported", strings.TrimSpace(provider), profile)
	}
}

// ReportRuntime 在 provider 启动/恢复路径上执行 mode-aware runtime 上报。
// generic error 始终阻断；只有 desktop/test 明确 deferred 的 orchestration 依赖可记录后继续。
func (r *modeAwareRuntimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	if r == nil || r.base == nil {
		return fmt.Errorf("%s runtime reporter is required", r.providerName())
	}
	err := r.base.ReportRuntime(ctx, report)
	if err == nil {
		return nil
	}
	if !errors.Is(err, contract.ErrDependencyDeferred) || !r.allowDeferred() {
		return fmt.Errorf("%s runtime report failed: %w", r.providerName(), err)
	}
	return r.acceptDeferredRuntimeReport(report)
}

func (r *modeAwareRuntimeReporter) allowDeferred() bool {
	return r.profile == contract.DependencyProfileDesktopHost || r.profile == contract.DependencyProfileTest
}

func (r *modeAwareRuntimeReporter) recordDeferred(report contract.RuntimeReport) {
	status := runtimeReportDeferredStatus{
		AgentID:    strings.TrimSpace(report.AgentID),
		Provider:   strings.TrimSpace(report.Provider),
		Port:       report.Port,
		Dependency: "runtime_reporter.orchestration_service",
		Profile:    r.profile,
		Status:     "deferred",
	}
	r.mu.Lock()
	r.deferred[status.AgentID] = status
	r.mu.Unlock()
	if r.logger != nil {
		r.logger.Info("runtime report deferred", "agent_id", status.AgentID, "provider", status.Provider, "dependency", status.Dependency, "profile", status.Profile, "status", status.Status)
	}
}

func (r *modeAwareRuntimeReporter) acceptDeferredRuntimeReport(report contract.RuntimeReport) error {
	r.recordDeferred(report)
	return nil
}

func (r *modeAwareRuntimeReporter) runtimeReportDeferredForTest(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, ok := r.deferred[strings.TrimSpace(agentID)]
	return ok && status.Dependency == "runtime_reporter.orchestration_service" && status.Profile == r.profile && status.Status == "deferred"
}

func (r *modeAwareRuntimeReporter) providerName() string {
	if r == nil || strings.TrimSpace(r.provider) == "" {
		return "provider"
	}
	return r.provider
}

var Module = fx.Module("provider.claudecli",
	fx.Provide(
		fx.Annotate(provideDriverFactory, fx.ResultTags(`group:"drivers"`)),
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
	),
	fx.Invoke(RegisterTranslators),
)
