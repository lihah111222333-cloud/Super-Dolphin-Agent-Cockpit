package main

import (
	"context"
	"log"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	"go.uber.org/fx"
)

// run boots the MCP binary itself. The core process only exposes ctl/* endpoints
// and manifest metadata; external executors decide when and how this binary starts.
func run() error {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func(shutdowner fx.Shutdowner) bootstrap.Config {
				cfg := bootstrap.ReadBootConfig()
				cfg.AgentID = ""
				cfg.Capabilities = []string{"tools/lsp"}
				cfg.FinalReport = func() *mcp.ReportRequest {
					return &mcp.ReportRequest{
						Report: mcp.ReportEnvelope{
							Type: mcp.ReportVariantCompletion,
							Completion: &mcp.CompletionReport{
								Status: "done",
								Report: "mcp-lsp shutdown",
							},
						},
					}
				}
				cfg.OnConfigChanged = func(notify mcp.ConfigChangedNotify) {
					log.Printf("mcp-lsp config changed: scope=%s version=%d selector=%+v payload=%s", notify.Scope, notify.ConfigVersion, notify.Selector, string(notify.Payload))
				}
				cfg.OnShutdown = func(mcp.ShutdownRequest) {
					_ = shutdowner.Shutdown()
				}
				return cfg
			},
			bootstrap.New,
		),
		fx.Invoke(func(lc fx.Lifecycle, client *bootstrap.Client) {
			lc.Append(fx.Hook{
				OnStart: func(ctx context.Context) error { return client.Start(ctx) },
				OnStop:  func(context.Context) error { return client.Close() },
			})
		}),
	)
	if err := app.Err(); err != nil {
		return err
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), fx.DefaultTimeout)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	<-app.Wait()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), fx.DefaultTimeout)
	defer stopCancel()
	return app.Stop(stopCtx)
}
