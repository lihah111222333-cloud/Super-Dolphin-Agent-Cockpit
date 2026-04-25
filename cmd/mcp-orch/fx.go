package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/memory"
	orchnotify "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/notify"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	taskdagstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
			newPool,
			newQueries,
			newAgentThreadStore,
			newAgentBindingStore,
			memory.NewConfig,
			memory.NewService,
			func(store storeworkspace.Store, dispatcher *event.Dispatcher) workspace.Service {
				return workspace.NewService(store, dispatcher)
			},
			newNoopSessionCleaner,
			newNoopTurnStarter,
			buildBootstrapConfig,
			bootstrap.New,
			newRegistry,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newHTTPRunner, fx.ResultTags(`group:"runners"`)),
		),
		fx.Invoke(registerPoolLifecycle),
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
		var req struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, err
		}
		result, err := p.CallTool(ctx, req.Name, req.Arguments)
		if err != nil {
			return nil, err
		}
		text, _ := json.Marshal(result)
		return map[string]any{
			"content": []map[string]string{{"type": "text", "text": string(text)}},
		}, nil
	}
	cfg.Capabilities = []string{
		"tools/orchestration", "tools/task", "tools/workspace",
		"tools/prompt", "tools/command", "tools/shared_file", "tools/memory",
	}
	cfg.Subscriptions = []string{"config/agent", "config/thread"}
	cfg.FinalReport = func() *mcp.ReportRequest {
		return &mcp.ReportRequest{Report: mcp.ReportEnvelope{
			Type:       mcp.ReportVariantCompletion,
			Completion: &mcp.CompletionReport{Status: "done", Report: "mcp-orch shutdown"},
		}}
	}
	cfg.OnConfigChanged = func(n mcp.ConfigChangedNotify) {
		log.Printf("mcp-orch config changed: scope=%s version=%d", n.Scope, n.ConfigVersion)
	}
	cfg.OnShutdown = func(mcp.ShutdownRequest) { _ = shutdowner.Shutdown() }
	if hookAfter != nil {
		cfg.Hooks = bootstrap.HookConfig{OnAfter: bootstrap.HookAfterHandler(hookAfter)}
	}
	return cfg
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
				orchestration.ProvideHookAfterHandler,
				orchestration.ProvideRPCFacade,
			),
			fx.Invoke(orchestration.RegisterTurnLifecycle),
			fx.Invoke(orchestration.RegisterApprovalLifecycle),
		),
		fx.Provide(func(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
			return buildLauncher(lc, turnStarter, logger, remoteAddr)
		}),
	}
	if remoteAddr == "" {
		options = append(options, fx.Provide(
			fx.Annotate(orchestration.NewRunnerActor, fx.ResultTags(`group:"runners"`)),
		))
	}
	return options
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
