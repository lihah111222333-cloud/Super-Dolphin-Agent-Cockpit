package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/app/storeadapter"
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
	"github.com/anthropic-ai/super-agent-v3/internal/module/workflowtemplate"
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
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/store"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
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
		provideSkillMutationAuditStore,
		provideSkillToolPersistence,
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
	fx.Provide(provideBusTraceRecorder, provideRPCTraceRecorder, provideMCPControlSystemLogSink),
	store.Module,
	threadStoreAdaptersModule(),
	storeadapter.Module,
	businessStoreAdaptersModule(),
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
	toolbridgeAdaptersModule(),
	toolbridgeCodexBindingModule(),
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
		provideCacheKeepaliveBindingLookup,
		provideCacheKeepaliveThreadLookup,
		provideNativeToolDescriptors,
		provideDisabledBuiltinToolsFn,
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

type cacheKeepaliveBindingLookupAdapter struct {
	store bindingstore.Store
}

// provideCacheKeepaliveBindingLookup 把 binding store 裁剪成 keepalive 只读端口。
func provideCacheKeepaliveBindingLookup(store bindingstore.Store) contract.CacheKeepaliveBindingLookup {
	if store == nil {
		return nil
	}
	return cacheKeepaliveBindingLookupAdapter{store: store}
}

// GetCacheKeepaliveBindingByAgentID 只暴露 keepalive 判断 live binding 需要的字段。
func (a cacheKeepaliveBindingLookupAdapter) GetCacheKeepaliveBindingByAgentID(ctx context.Context, agentID string) (*contract.CacheKeepaliveBinding, error) {
	binding, err := a.store.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return nil, err
	}
	return &contract.CacheKeepaliveBinding{
		AgentID:  binding.AgentID,
		Archived: binding.Archived,
	}, nil
}

type cacheKeepaliveThreadLookupAdapter struct {
	store threadstore.Store
}

// provideCacheKeepaliveThreadLookup 把 thread store 裁剪成 keepalive 启动事件回查端口。
func provideCacheKeepaliveThreadLookup(store threadstore.Store) contract.CacheKeepaliveThreadLookup {
	if store == nil {
		return nil
	}
	return cacheKeepaliveThreadLookupAdapter{store: store}
}

// GetCacheKeepaliveThreadByID 只返回 keepalive 反查 agentID 需要的线程身份字段。
func (a cacheKeepaliveThreadLookupAdapter) GetCacheKeepaliveThreadByID(ctx context.Context, threadID string) (*contract.CacheKeepaliveThreadRef, error) {
	thread, err := a.store.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return nil, err
	}
	return &contract.CacheKeepaliveThreadRef{
		ThreadID: thread.ThreadID,
		AgentID:  thread.AgentID,
	}, nil
}

// provideNativeToolDescriptors 从 unified registry 暴露原生工具描述。
func provideNativeToolDescriptors(registry *unified.Registry) []contract.NativeToolDescriptor {
	if registry == nil {
		return nil
	}
	return registry.NativeTools()
}

// provideDisabledBuiltinToolsFn 将 UI 偏好的内置工具软过滤接入 prompt。
// 这里做成函数桥接，避免 prompt 包直接依赖 uistate 包形成反向导入。
func provideDisabledBuiltinToolsFn(prefs uipreference.Store, tools []contract.NativeToolDescriptor) prompt.DisabledBuiltinToolsFn {
	index := make(map[string]contract.NativeToolDescriptor, len(tools))
	for _, t := range tools {
		index[t.ID] = t
	}
	return func(ctx context.Context, cwd, provider string) ([]string, error) {
		return uistate.ResolveExplicitSoftFilteredBuiltinTools(ctx, prefs, cwd, tools, index, provider)
	}
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
