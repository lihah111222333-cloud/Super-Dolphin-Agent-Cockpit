package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/fxadapter"
	orchnotify "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/notify"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/wakeupreclaim"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	taskdagstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// run 启动 mcp-orch 进程，完成依赖装配、RPC 监听和退出清理。
// 该二进制只暴露编排侧工具、manifest 和控制端点，具体启动时机由外部 runtime 决定。
func run() error {
	remoteAddr := strings.TrimSpace(os.Getenv("GO_AGENT_CTL_RPC_ADDR"))
	app := fx.New(
		fx.NopLogger,
		platformconfig.Module,
		platformdb.Module,
		platformbus.Module,
		orchnotify.Module,
		promptstore.Module,
		commandcardstore.Module,
		sharedfilestore.Module,
		taskdagstore.Module,
		storeworkspace.Module,
		fx.Options(buildOrchestrationOptions(remoteAddr)...),
		fx.Provide(
			newLogger,
			newQueries,
			newAgentThreadStore,
			newAgentBindingStore,
			func(store storeworkspace.Store, dispatcher *event.Dispatcher) workspace.Service {
				return workspace.NewService(store, dispatcher)
			},
			newNoopSessionCleaner,
			newNoopTurnStarter,
			newModelRegistry,
			newBuiltinPromptRegistry,
			buildBootstrapConfig,
			bootstrap.New,
			newRegistry,
			newStdioServer,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newHTTPRunner, fx.ResultTags(`group:"runners"`)),
		),
		fx.Invoke(bindRuntime),
	)
	if err := app.Err(); err != nil {
		return err
	}
	startCtx, startCancel := platformconfig.WithRPCRequestTimeout(context.Background())
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	<-app.Wait()
	stopCtx, stopCancel := platformconfig.WithRPCRequestTimeout(context.Background())
	defer stopCancel()
	return app.Stop(stopCtx)
}

// buildBootstrapConfig 组装 mcp-orch 的启动配置，把工具注册和 scope 注入接到 bootstrap。
func buildBootstrapConfig(shutdowner fx.Shutdowner, hookAfter contract.BootstrapHookAfterHandler, registry tools.Registry) bootstrap.Config {
	cfg := bootstrap.ReadBootConfig()
	cfg.AgentID = ""
	// 注册 tools/list 和 tools/call，让 toolbridge 能以带 scope 的方式调用 mcp-orch。
	// toolbridge 只负责转发和注入 scope，实际工具逻辑仍走同一份 registry。
	p := registryToolProvider{registry: registry}
	cfg.OnToolsList = func(ctx context.Context) (any, error) {
		tools, err := p.ListTools(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tools": tools}, nil
	}
	cfg.OnToolsCall = func(ctx context.Context, params json.RawMessage) (any, error) {
		return handleScopedToolsCall(ctx, p, mcp.ClientKindOrch, params)
	}
	cfg.Capabilities = []string{
		"tools/orchestration", "tools/task", "tools/workspace",
		"tools/prompt", "tools/command", "tools/shared_file", "tools/video",
	}
	cfg.Subscriptions = []string{"config/agent", "config/thread"}
	cfg.FinalReport = func() *mcp.ReportRequest {
		return &mcp.ReportRequest{Report: mcp.ReportEnvelope{
			Type:       mcp.ReportVariantCompletion,
			Completion: &mcp.CompletionReport{Status: "done", Report: "mcp-orch shutdown"},
		}}
	}
	cfg.OnConfigChanged = func(n mcp.ConfigChangedNotify) {
		pkglogger.Get().Info("mcp-orch config changed", "scope", n.Scope, "version", n.ConfigVersion)
	}
	cfg.OnShutdown = func(mcp.ShutdownRequest) {
		platformshared.LogIgnoredError(pkglogger.Get(), "mcp-orch: OnShutdown", shutdowner.Shutdown())
	}
	if hookAfter != nil {
		cfg.Hooks = bootstrap.HookConfig{OnAfter: bootstrap.HookAfterHandler(hookAfter)}
	}
	return cfg
}

func handleScopedToolsCall(ctx context.Context, p registryToolProvider, family string, params json.RawMessage) (any, error) {
	return handleScopedToolsCallWithCaller(ctx, family, params, p.CallTool)
}

// bootstrap tools/call 在这里把 _agentId/_cwd 放进 ctx。
// 下游工具要信 ctx 里的 scope，不要信模型传来的同名字段。
func handleScopedToolsCallWithCaller(
	ctx context.Context,
	family string,
	params json.RawMessage,
	call func(context.Context, string, json.RawMessage) (any, error),
) (any, error) {
	req, err := common.DecodeToolCallParams(params)
	if err != nil {
		return nil, err
	}
	scope := req.Scope(family)
	ctx = common.WithToolScope(ctx, scope)
	result, err := call(ctx, req.Name, req.Arguments)
	if err != nil {
		result = common.NewToolErrorEnvelope(req.Name, err)
	}
	return wrapScopedToolResult(result)
}

// error envelope 也作为正常工具结果返回。
// 这样模型能看到 isError=true，而不是收到一层 JSON-RPC 失败。
func wrapScopedToolResult(result any) (any, error) {
	text, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": string(text)}},
		"structuredContent": json.RawMessage(text),
		"isError":           structuredToolResultIsError(text),
	}, nil
}

func structuredToolResultIsError(raw []byte) bool {
	var probe struct {
		Success *bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Success != nil && !*probe.Success
}

// buildOrchestrationOptions 组装编排模块的 fx 依赖，让 DAG、cron 和 runtime 能一起启动。
func buildOrchestrationOptions(remoteAddr string) []fx.Option {
	// orchestration 子包不导出 fx.Module；根装配层在这里显式串起 provider 和 lifecycle hook。
	// 这让依赖方向保持在入口层，避免业务子包反向拥有应用装配边界。
	options := []fx.Option{
		fx.Module("orchestration",
			fx.Provide(
				fx.Annotate(
					orchestration.ProvideService,
					fx.As(fx.Self()),
					fx.As(new(contract.AgentLifecyclePort)),
					fx.As(new(contract.AgentRuntimePort)),
					fx.As(new(contract.AgentReportPort)),
					fx.As(new(contract.TurnSubmissionPort)),
					fx.As(new(contract.DAGCreateRuntime)),
					fx.As(new(contract.DAGRuntime)),
					fx.As(new(contract.DAGDeleteRuntime)),
					fx.As(new(contract.DAGNodeStatusRuntime)),
					fx.As(new(contract.DAGNodeDispatchRuntime)),
					fx.As(new(orchestration.ScheduledDAGStartService)),
					fx.As(new(orchestration.WakeupLauncher)),
					fx.As(new(orchestration.HookConsumerRuntime)),
					fx.As(new(orchestration.HookReportPort)),
					fx.As(new(orchestration.AgentLaunchSnapshotter)),
					fx.As(new(orchestration.StopAgentService)),
					fx.As(new(orchestration.RunnerLifecyclePort)),
					fx.As(new(orchestration.RunnerRuntimePort)),
					fx.As(new(orchestration.TurnLifecyclePort)),
					fx.As(new(orchestration.ApprovalLifecyclePort)),
				),
				orchestration.ProvideHookAfterHandler,
				orchestration.ProvideRPCFacade,
				provideSQLDAGScheduleStore,
				provideSQLiteRuntimeLocker,
				provideAgentThreadLookup,
				// 为 DAG turn.completed subscriber 提供窄端口，避免订阅器依赖完整 store/service。
				orchestration.ProvideDAGSubscriberNodeFlowStore,
			),
			fx.Invoke(orchestration.RegisterTurnLifecycle),
			fx.Invoke(orchestration.RegisterApprovalLifecycle),
			// DAG turn.completed 订阅只推进 DAG 节点状态；agent runtime 推进仍由 RegisterTurnLifecycle 负责。
			// 两条订阅路径职责分离，避免同一事件里互相覆盖运行态。
			fx.Invoke(orchestration.RegisterDAGTurnCompletedSubscriber),
			fx.Provide(fx.Annotate(orchestration.ProvideWakeupDispatcherRunner, fx.ResultTags(`group:"runners"`))),
			fx.Provide(fx.Annotate(wakeupreclaim.ProvideWakeupReclaimerRunner, fx.ResultTags(`group:"runners"`))),
			fx.Provide(fx.Annotate(provideScheduledDAGCronRunner, fx.ResultTags(`group:"runners"`))),
		),
		fx.Provide(func(lc fx.Lifecycle, turnStarter contract.OrchestrationTurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
			return buildLauncher(lc, turnStarter, logger, remoteAddr)
		}),
		fx.Provide(
			newAutomationCommandGetter,
			nodeexec.NewShellCommandRunner,
			// AutomationCommandRunner 接口适配：ShellCommandRunner 是 *T 类型，
			// ProvideAutomationExecutor 要 AutomationCommandRunner 接口，fx 不会自动推断。
			func(r *nodeexec.ShellCommandRunner) nodeexec.AutomationCommandRunner { return r },
			// AgentExecutor 和 NodeExecutorRouter 作为 fx 单例接入 dispatcher。
			// agentLifecycleController 先把 service 生命周期能力复制到窄口，再交给 DAG agent launcher。
			orchestration.ProvideAgentLifecycleController,
			orchestration.NewServiceAgentLauncher,
			fxadapter.NewStoreNodeSpawnRecorder,
			orchestration.ProvideNodeLifecycleHooks,
			orchestration.ProvideAutomationExecutor,
			// sharedfile 端口 adapter 把 store/sharedfile.Store 收窄成 nodeexec 读写端口。
			// NodeExecutorRouter 预填 RunContext 时依赖它处理 inputs.from_sharedfiles 与 outputs.to_sharedfile。
			orchestration.NewStoreSharedFileReader,
			orchestration.NewStoreSharedFileWriter,
			// 通过 ProvideAgentExecutor 注入 recorder option；直接提供 nodeexec.NewAgentExecutor 会丢失可变参数。
			orchestration.ProvideAgentExecutor,
			orchestration.NewNodeExecutorRouter,
		),
		// WakeupDispatcher 先作为具体类型提供，再用 invoke 装上 nodeRouter。
		// Runner provider 返回的是接口，无法通过 fx.Decorate 拿回具体 dispatcher。
		fx.Provide(orchestration.ProvideWakeupDispatcher),
		fx.Invoke(orchestration.WireWakeupDispatcherRouter),
		fx.Invoke(orchestration.WireWakeupDispatcherRetryAlertSink),
	}
	if remoteAddr == "" {
		options = append(options, fx.Provide(
			fx.Annotate(orchestration.NewRunnerActor, fx.ResultTags(`group:"runners"`)),
		))
	}
	return options
}

func provideAgentThreadLookup(store orchestration.AgentThreadStore) orchestration.AgentThreadLookup {
	return store
}

type automationCommandGetter struct {
	handler tools.ToolHandler
}

func newAutomationCommandGetter(store commandcardstore.Store) nodeexec.AutomationCommandGetter {
	return automationCommandGetter{handler: tools.HandleCommandGet(store)}
}

// GetCommandCard 返回桌面端展示 mcp-orch 命令所需的卡片信息。
func (g automationCommandGetter) GetCommandCard(ctx context.Context, cardKey string) (nodeexec.AutomationCommandCard, error) {
	if g.handler == nil {
		return nodeexec.AutomationCommandCard{}, errors.New("command_get client is not configured")
	}
	input, err := json.Marshal(map[string]string{"card_key": cardKey})
	if err != nil {
		return nodeexec.AutomationCommandCard{}, fmt.Errorf("marshal command_get input: %w", err)
	}
	result, err := g.handler(ctx, input)
	if err != nil {
		return nodeexec.AutomationCommandCard{}, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nodeexec.AutomationCommandCard{}, fmt.Errorf("marshal command_get result: %w", err)
	}
	var card nodeexec.AutomationCommandCard
	if err := json.Unmarshal(payload, &card); err != nil {
		return nodeexec.AutomationCommandCard{}, fmt.Errorf("parse command_get result: %w", err)
	}
	return card, nil
}

func buildLauncher(lc fx.Lifecycle, turnStarter contract.OrchestrationTurnStarter, logger *slog.Logger, remoteAddr string) orchestration.AgentLauncher {
	if remoteAddr == "" {
		return orchestration.NewLocalLauncher(turnStarter, logger)
	}
	launcher := orchestration.NewRemoteLauncher(remoteAddr)
	if closer, ok := launcher.(interface{ Close() error }); ok {
		lc.Append(fx.Hook{OnStop: func(context.Context) error { return closer.Close() }})
	}
	return launcher
}
