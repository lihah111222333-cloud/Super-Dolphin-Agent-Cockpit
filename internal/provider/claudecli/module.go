package claudecli

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

// driverFactoryParams collects the fx dependencies for NewDriverFactory.
// SkillLibConfig and FBSDRecorder are optional so test fixtures that do not
// provide them still compile and wire correctly.
type driverFactoryParams struct {
	fx.In

	Logger               *slog.Logger
	Dispatcher           *unified.EventDispatcher
	Reporter             contract.RuntimeReporter
	Reg                  *pidregistry.Registry
	ProxyAddrFn          func() string
	SkillLibConfig       contract.SkillLibraryConfig      `optional:"true"`
	FBSDRecorder         contract.FBSDRecorder            `optional:"true"`
	SetupWorkspaceSkills contract.WorkspaceSkillSetupFunc `optional:"true"`
}

func NewDriverFactory(p driverFactoryParams) contract.DriverFactory {
	// P6: install FBSD recorder hook so Claude tool_use parser can打点
	// when the model issues Read(.claude/skills/<n>/references/...) calls.
	// nil tracker / disabled tracker keeps the hook nil-safe.
	if p.FBSDRecorder != nil {
		SetFBSDRecorder(p.FBSDRecorder.Record)
	}
	return contract.DriverFactory{
		Name: "claude",
		Create: func() contract.Driver {
			return newDriver(p.Logger, p.Dispatcher, p.Reporter, p.Reg, p.ProxyAddrFn, p.SkillLibConfig.CacheDir, contract.WorkspaceSkillSetupFunc(p.SetupWorkspaceSkills))
		},
		NativeTools: []contract.NativeToolDescriptor{
			{ID: "Read", Label: "读文件", Description: "上游 Agent 直接读取工作区文件", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Write", Label: "写文件", Description: "上游 Agent 直接写入新文件", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Edit", Label: "编辑文件", Description: "上游 Agent 直接修改现有文件", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "MultiEdit", Label: "批量编辑", Description: "一次调用内批量修改多个位置", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Bash", Label: "执行命令", Description: "在本地 shell 中执行任意命令", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Grep", Label: "代码搜索", Description: "使用上游内置 grep 在工作区查找", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Glob", Label: "文件匹配", Description: "按 glob 模式列出匹配文件", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "LS", Label: "列目录", Description: "列出目录内容", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "WebFetch", Label: "抓取网页", Description: "按 URL 拉取网页内容", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "WebSearch", Label: "网页搜索", Description: "调用内置网页搜索", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "TodoWrite", Label: "待办记录", Description: "写入上游自带的任务清单", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "NotebookEdit", Label: "Notebook 编辑", Description: "编辑 Jupyter Notebook", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "Task", Label: "派生子 Agent", Description: "派生子 Agent 执行任务", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
			{ID: "ExitPlanMode", Label: "退出计划模式", Description: "离开 Plan Mode 审批界面", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
		},
	}

}

var Module = fx.Module("provider.claudecli",
	fx.Provide(
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
	),
	fx.Invoke(RegisterTranslators),
)
