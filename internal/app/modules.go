package app

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/runtimeadapter"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app/storeadapter"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/appupdate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/cron"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/dashboard"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/datasource"
	datasourcev2 "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/datasource_v2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/feedback"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/insight"
	mcpserver "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/mcp_server"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/notify"
	moduleobservability "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/personalization"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/thread"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/turn"
	turnobservation "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/turn/observation"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/workflowtemplate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/cachekeepalive"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/hooks"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	platformobservability "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/statemachine"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/e2efixture"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store"
)

const promptIntentE2EFixtureHarnessEnv = "PROMPT_INTENT_E2E_FIXTURE_HARNESS"

// Module 组装桌面和后台共享的核心应用面。
// 它只暴露给外部 MCP 进程使用的控制面，不在桌面进程内启动或监管 MCP 子进程。
var Module = fx.Options(
	fx.Provide(NewLogger),
	fx.Provide(pidregistry.New),
	fx.Provide(
		provideDashboardOrchestrationReaderPort,
		provideDashboardOrchestrationReportReaderPort,
		provideUIStateAgentLister,
		newDashboardOrchestrationReader,
		newDashboardOrchestrationReportReader,
	),
	config.Module,
	db.Module,
	bus.Module,
	rpc.Module,
	hooks.Module,
	cachekeepalive.Module,
	mcpcontrol.Module,
	platformrunner.Module,
	statemachine.Module,
	platformobservability.Module,
	fx.Provide(provideBusTraceRecorder, provideRPCTraceRecorder),
	store.Module,
	storeadapter.Module,
	dashboard.Module,
	datasource.Module,
	datasourcev2.Module,
	feedback.Module,
	mcpserver.Module,
	memory.Module,
	prompt.Module,
	personalization.Module,
	moduleobservability.Module,
	appupdate.Module,
	skill.Module,
	fx.Invoke(initProviderHooks),
	thread.Module,
	turn.Module,
	turnobservation.Module,
	// 定时任务、通知和洞察模块均采用 fx optional 依赖。
	// 缺少上游依赖时保持 no-op，不阻塞桌面主体装配图闭合。
	cron.Module,
	notify.Module,
	insight.Module,
	uistate.Module,
	workflowtemplate.Module,
	unified.Module,
	promptIntentE2EFixtureModule(),
	// claudecli.Module,
	codexapp.Module,
	toolbridge.Module, // 始终加载 provider 工具桥，供内置工具和 peer 调用共用。
	runtimeadapter.Module,
	sharedFileAdapterModule(),

	// DAG 编排由独立 mcp-orch MCP server 承担。
	// 桌面进程不能再嵌入 orchestration module，否则本地 launcher 会把桌面二进制当子进程拉起并导致 agent 失败。
	fx.Provide(
		AsRPCRunner,
		newToolbridgeHandlerRef,
		fx.Annotate(newMCPOrchDAGRuntime, fx.As(new(contract.DAGRuntime))),
		fx.Annotate(
			newMCPOrchOrchestrationFacade,
			fx.As(new(thread.OrchestrationFacade)),
			fx.As(new(thread.SessionGenerationBinder)),
		),
		provideRuntimeUpdater,
		newRuntimeReporter,
		thread.NewSessionLifecyclePort,
		thread.NewSessionStatusPort,
		newSessionPorts,
	),
	fx.Invoke(bindToolbridgeHandlerRef),
)

// promptIntentE2EFixtureModule 仅在显式配置 fixture path 时加载 e2e provider。
func promptIntentE2EFixtureModule() fx.Option {
	if strings.TrimSpace(os.Getenv(e2efixture.FixturePathEnv)) == "" {
		return fx.Options()
	}
	if strings.TrimSpace(os.Getenv(promptIntentE2EFixtureHarnessEnv)) != "1" {
		return fx.Error(fmt.Errorf("%s requires %s=1; refusing to register e2e fixture provider in production graph",
			e2efixture.FixturePathEnv, promptIntentE2EFixtureHarnessEnv))
	}
	if strings.TrimSpace(os.Getenv("DREAM_PROVIDER_ORDER")) == "" {
		_ = os.Setenv("DREAM_PROVIDER_ORDER", e2efixture.ProviderName)
	}
	return e2efixture.Module
}

// AsRPCRunner 将 RPC server 注册为 platform runner。
func AsRPCRunner(server *rpc.Server) RunnerResult {
	return RunnerResult{Runner: server}
}

// initProviderHooks 把 module 层能力接入 provider/shared 钩子。
// provider 层通过这些钩子使用工具结果捕获和技能块裁剪，避免直接导入 module 包。
func initProviderHooks() {
	providershared.SetCaptureToolResultHook(func(meta providershared.ToolResultMeta, raw string) providershared.ToolResultRecord {
		result := turn.CaptureToolResult(turn.ToolResultMeta{
			ThreadID:  meta.ThreadID,
			TurnID:    meta.TurnID,
			CallID:    meta.CallID,
			ToolName:  meta.ToolName,
			Timestamp: meta.Timestamp,
		}, raw)
		return providershared.ToolResultRecord{
			Preview:       result.Preview,
			PersistedPath: result.PersistedPath,
			PersistFailed: result.PersistFailed,
			PersistError:  result.PersistError,
			Truncated:     result.Truncated,
			OriginalSize:  result.OriginalSize,
		}
	})
	providershared.SetResetToolResultScopeHook(turn.ResetToolResultScope)
	providershared.SetTrimSkillBlocksHook(skill.TrimInjectedSkillBlocks)
}
