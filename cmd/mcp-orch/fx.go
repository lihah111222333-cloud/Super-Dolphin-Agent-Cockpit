package main

import (
	"context"
	"log"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	taskdagstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	workspace "github.com/anthropic-ai/super-agent-v3/internal/module/workspace"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	internalStore "github.com/anthropic-ai/super-agent-v3/internal/store"
	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
	"go.uber.org/fx"
)

// run boots the MCP binary itself. The core process only exposes ctl/* endpoints
// and manifest metadata; external executors decide when and how this binary starts.
func run() error {
	app := fx.New(
		fx.NopLogger,
		platformconfig.Module,
		platformbus.Module,
		internalStore.Module,
		promptstore.Module,
		commandcardstore.Module,
		sharedfilestore.Module,
		taskdagstore.Module,
		orchestration.Module,
		fx.Provide(
			newLogger,
			newPool,
			newQueries,
			func(store storeworkspace.Store, emitters *platformbus.WorkspaceEmitters) workspace.Service {
				return workspace.NewService(store, emitters)
			},
			newNoopSessionCleaner,
			newNoopTurnStarter,
			func(shutdowner fx.Shutdowner) bootstrap.Config {
				cfg := bootstrap.ReadBootConfig()
				cfg.AgentID = ""
				cfg.Capabilities = []string{
					"tools/orchestration",
					"tools/task",
					"tools/workspace",
					"tools/prompt",
					"tools/command",
					"tools/shared_file",
				}
				cfg.Subscriptions = []string{"config/agent", "config/thread"}
				cfg.FinalReport = func() *mcp.ReportRequest {
					return &mcp.ReportRequest{
						Report: mcp.ReportEnvelope{
							Type: mcp.ReportVariantCompletion,
							Completion: &mcp.CompletionReport{
								Status: "done",
								Report: "mcp-orch shutdown",
							},
						},
					}
				}
				cfg.OnConfigChanged = func(notify mcp.ConfigChangedNotify) {
					log.Printf("mcp-orch config changed: scope=%s version=%d selector=%+v payload=%s", notify.Scope, notify.ConfigVersion, notify.Selector, string(notify.Payload))
				}
				cfg.OnShutdown = func(mcp.ShutdownRequest) {
					_ = shutdowner.Shutdown()
				}
				return cfg
			},
			bootstrap.New,
			newRegistry,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
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
