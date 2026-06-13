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

// run boots the MCP binary itself. The core process only exposes ctl/* endpoints
// and manifest metadata; external executors decide when and how this binary starts.
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

func buildBootstrapConfig(shutdowner fx.Shutdowner, hookAfter contract.BootstrapHookAfterHandler, registry tools.Registry) bootstrap.Config {
	cfg := bootstrap.ReadBootConfig()
	cfg.AgentID = ""
	// P15: register tools/list and tools/call so toolbridge can call this peer.
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

func buildOrchestrationOptions(remoteAddr string) []fx.Option {
	// P22 P4 S4c1: the orchestration subpackage no longer exports
	// `var Module`; root assembly wires its providers + lifecycle hooks
	// explicitly here. The archtest TestOrchestrationNoModuleExport
	// locks this in place (see
	// internal/archtest/orchestration_no_module_export_guard_test.go).
	options := []fx.Option{
		fx.Module("orchestration",
			fx.Provide(
				orchestration.ProvideService,
				orchestration.ProvideServiceInterface,
				orchestration.ProvideScheduledDAGStartService,
				orchestration.ProvideHookAfterHandler,
				orchestration.ProvideRPCFacade,
				provideSQLDAGScheduleStore,
				provideSQLiteRuntimeLocker,
				// ADR-017 v1.2 §2.9：DAG turn.completed subscriber 的窄端口 provider。
				orchestration.ProvideDAGSubscriberNodeFlowStore,
				orchestration.ProvideDAGSubscriberStopAgentService,
				orchestration.ProvideDAGSubscriberAgentThreadLookup,
			),
			fx.Invoke(orchestration.RegisterTurnLifecycle),
			fx.Invoke(orchestration.RegisterApprovalLifecycle),
			// ADR-017 v1.2 §2.1：第三路 TurnCompleted 订阅 —— 推进 DAG 节点状态机。
			// 与 RegisterTurnLifecycle（推 agent runtime）并列不重叠，
			// 三路并发安全（v1.2 reviewer A 实证）。
			fx.Invoke(orchestration.RegisterDAGTurnCompletedSubscriber),
			fx.Provide(fx.Annotate(orchestration.ProvideWakeupDispatcherRunner, fx.ResultTags(`group:"runners"`))),
			fx.Provide(fx.Annotate(wakeupreclaim.ProvideWakeupReclaimerRunner, fx.ResultTags(`group:"runners"`))),
			fx.Provide(fx.Annotate(provideScheduledDAGCronRunner, fx.ResultTags(`group:"runners"`))),
		),
		fx.Provide(func(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
			return buildLauncher(lc, turnStarter, logger, remoteAddr)
		}),
		fx.Provide(
			newAutomationCommandGetter,
			nodeexec.NewShellCommandRunner,
			// AutomationCommandRunner 接口适配：ShellCommandRunner 是 *T 类型，
			// ProvideAutomationExecutor 要 AutomationCommandRunner 接口，fx 不会自动推断。
			func(r *nodeexec.ShellCommandRunner) nodeexec.AutomationCommandRunner { return r },
			// dispatcher-wiring batch §1：AgentExecutor / NodeExecutorRouter
			// fx singletons + serviceAgentLauncher adapter。这些 provider 让 W1/W2 以来
			// “孤儿”的 AgentExecutor / AutomationExecutor 代码正式被危口调到。
			orchestration.NewServiceAgentLauncher,
			fxadapter.NewStoreNodeSpawnRecorder,
			orchestration.ProvideNodeLifecycleHooks,
			orchestration.ProvideAutomationExecutor,
			// dispatcher-wiring closure：sharedfile 端口 adapter —— store/sharedfile.Store
			// 适配成 nodeexec.SharedFileReader / SharedFileWriter，供 NodeExecutorRouter 预填
			// RunContext。是 W2 端口收敛后 dispatcher 路径能走 dogfood-grade DAG
			// (cfg.Inputs.from_sharedfiles / outputs.to_sharedfile) 的必要 wiring。
			orchestration.NewStoreSharedFileReader,
			orchestration.NewStoreSharedFileWriter,
			// round-3 merge fix: 走 ProvideAgentExecutor 包 WithRecorder option，
			// 而不是直接 fx-resolve nodeexec NewAgentExecutor —— 后者 W2 端口收敛后
			// 变 variadic Option 形态，fx 直 Provide 只会拿 launcher 丢 recorder。
			orchestration.ProvideAgentExecutor,
			orchestration.NewNodeExecutorRouter,
		),
		// dispatcher-wiring batch §1：在 NewWakeupDispatcher 返 dispatcher 后装上
		// nodeRouter。必须采用 fx.Invoke 而非 fx.Decorate：ProvideWakeupDispatcherRunner
		// 返 Runner 接口而非其位 *WakeupDispatcher，无法被 decorate 拿到原始类型。
		// 单独提供一个 *WakeupDispatcher provider，供 Runner / router-wire invoke 复用。
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

type automationCommandGetter struct {
	handler tools.ToolHandler
}

func newAutomationCommandGetter(store commandcardstore.Store) nodeexec.AutomationCommandGetter {
	return automationCommandGetter{handler: tools.HandleCommandGet(store)}
}

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

func buildLauncher(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger, remoteAddr string) orchestration.AgentLauncher {
	if remoteAddr == "" {
		return orchestration.NewLocalLauncher(turnStarter, logger)
	}
	launcher := orchestration.NewRemoteLauncher(remoteAddr)
	if closer, ok := launcher.(interface{ Close() error }); ok {
		lc.Append(fx.Hook{OnStop: func(context.Context) error { return closer.Close() }})
	}
	return launcher
}
