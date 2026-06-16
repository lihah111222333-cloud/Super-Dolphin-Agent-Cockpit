package app

import (
	"context"
	"os"
	"strings"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/appupdate"
	"github.com/anthropic-ai/super-agent-v3/internal/module/cron"
	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/datasource"
	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	"github.com/anthropic-ai/super-agent-v3/internal/module/feedback"
	"github.com/anthropic-ai/super-agent-v3/internal/module/insight"
	mcpserver "github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	"github.com/anthropic-ai/super-agent-v3/internal/module/notify"
	moduleobservability "github.com/anthropic-ai/super-agent-v3/internal/module/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/module/personalization"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	turnobservation "github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/cachekeepalive"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/hooks"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	platformobservability "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/e2efixture"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/runtimeconfig"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/store"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

// Module wires the core app surface. It intentionally exposes the ctl control plane
// for externally started MCP binaries, but does not launch or supervise MCP processes.
var Module = fx.Options(
	fx.Provide(NewLogger),
	fx.Provide(pidregistry.New),
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
	// P21 模块：P1b cron 计划任务、P2 多平台通知、P3 session insights。
	// 三者均采用 fx optional 依赖，缺少上游依赖时自动降级为 noop，不阻塞整体装配图闭合。
	cron.Module,
	notify.Module,
	insight.Module,
	uistate.Module,
	unified.Module,
	promptIntentE2EFixtureModule(),
	// claudecli.Module,
	codexapp.Module,
	toolbridge.Module, // P15 新增：始终加载
	toolbridgeAdaptersModule(),
	toolbridgeCodexBindingModule(),
	sharedFileAdapterModule(),

	// orchestration is handled entirely by the standalone mcp-orch MCP server;
	// the desktop app must NOT embed its own orchestration module, otherwise
	// localLauncher re-spawns the desktop binary as a subprocess which exits
	// immediately and causes agent state to go to "failed".
	fx.Provide(
		AsRPCRunner,
		fx.Annotate(newMCPOrchDAGRuntime, fx.As(new(contract.DAGRuntime))),
		newThreadOrchestrationFacade,
		newRuntimeReporter,
		provideNativeToolDescriptors,
		provideDisabledBuiltinToolsFn,
	),
)

func promptIntentE2EFixtureModule() fx.Option {
	if strings.TrimSpace(os.Getenv(e2efixture.FixturePathEnv)) == "" {
		return fx.Options()
	}
	if strings.TrimSpace(os.Getenv("DREAM_PROVIDER_ORDER")) == "" {
		_ = os.Setenv("DREAM_PROVIDER_ORDER", e2efixture.ProviderName)
	}
	return e2efixture.Module
}

func provideNativeToolDescriptors(registry *unified.Registry) []contract.NativeToolDescriptor {
	if registry == nil {
		return nil
	}
	return registry.NativeTools()
}

// provideDisabledBuiltinToolsFn bridges uistate soft-filter resolution into
// prompt.DisabledBuiltinToolsFn without creating a prompt→uistate import cycle.
func provideDisabledBuiltinToolsFn(prefs uipreference.Store, tools []contract.NativeToolDescriptor) prompt.DisabledBuiltinToolsFn {
	index := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, t := range tools {
		index[t.ID] = t
	}
	return func(ctx context.Context, cwd, provider string) []string {
		return uistate.ResolveSoftFilteredBuiltinTools(ctx, prefs, cwd, tools, index, provider)
	}
}

// AsRPCRunner 把应用装配处理为RPCrunner。
func AsRPCRunner(server *rpc.Server) RunnerResult {
	return RunnerResult{Runner: server}
}

// initProviderHooks wires module-layer functions into provider/shared hooks
// so the provider layer can use CaptureToolResult, ResetToolResultScope, and
// TrimInjectedSkillBlocks without importing module packages directly.
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
			Truncated:     result.Truncated,
			OriginalSize:  result.OriginalSize,
		}
	})
	providershared.SetResetToolResultScopeHook(turn.ResetToolResultScope)
	providershared.SetTrimSkillBlocksHook(skill.TrimInjectedSkillBlocks)
}
