package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"go.uber.org/fx"
)

type bootstrapRunner struct {
	cfg    bootstrap.Config
	client *bootstrap.Client
}

type runtimeParams struct {
	fx.In

	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

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
					slog.Info("mcp-lsp config changed",
						"binary_name", cfg.BinaryName,
						"instance_id", cfg.InstanceID,
						"scope", notify.Scope,
						"config_version", notify.ConfigVersion,
						"selector", notify.Selector,
						"payload", string(notify.Payload),
					)
				}
				cfg.OnShutdown = func(mcp.ShutdownRequest) {
					_ = shutdowner.Shutdown()
				}
				return cfg
			},
			bootstrap.New,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
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

func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client}
}

func (r bootstrapRunner) Run(ctx context.Context) error {
	if strings.TrimSpace(r.cfg.RPCAddr) == "" {
		<-ctx.Done()
		return nil
	}
	if err := r.client.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return r.client.Close()
}

func bindRuntime(lc fx.Lifecycle, params runtimeParams) {
	logger := slog.Default()
	var cancel context.CancelFunc
	done := make(chan error, 1)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			go func() {
				err := platformrunner.RunGroup(runCtx, params.Runners, platformrunner.GroupOptions{
					EnableSignals: true,
				})
				done <- err
				close(done)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("mcp-lsp runtime exited", "error", err)
				}
				_ = params.Shutdowner.Shutdown()
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
