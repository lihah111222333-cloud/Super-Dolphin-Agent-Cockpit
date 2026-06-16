package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"go.uber.org/fx"
)

type bootstrapRunner struct {
	cfg        bootstrap.Config
	client     *bootstrap.Client
	stdioReady <-chan struct{} // closed when stdio server is ready
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
			buildBootstrapConfig,
			bootstrap.New,
			newStdioServer,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
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

// buildBootstrapConfig 构建启动配置。
func buildBootstrapConfig(shutdowner fx.Shutdowner) (bootstrap.Config, error) {
	cfg := bootstrap.ReadBootConfig()
	cfg.AgentID = ""
	var err error
	cfg.BootSnapshot, err = stripBootSnapshotCapabilities(cfg.BootSnapshot)
	if err != nil {
		return bootstrap.Config{}, err
	}
	// Do not advertise tools/ida until a real IDA provider registers
	// concrete schemas and handlers. An empty provider is a placeholder,
	// not a tool capability.
	cfg.FinalReport = func() *mcp.ReportRequest {
		return &mcp.ReportRequest{
			Report: mcp.ReportEnvelope{
				Type: mcp.ReportVariantCompletion,
				Completion: &mcp.CompletionReport{
					Status: "done",
					Report: "mcp-ida shutdown",
				},
			},
		}
	}
	cfg.OnConfigChanged = func(notify mcp.ConfigChangedNotify) {
		pkglogger.Info("mcp-ida config changed",
			"binary_name", cfg.BinaryName,
			"instance_id", cfg.InstanceID,
			"scope", notify.Scope,
			"config_version", notify.ConfigVersion,
			"selector", notify.Selector,
			"payload", string(notify.Payload),
		)
	}
	cfg.OnShutdown = func(mcp.ShutdownRequest) {
		platformshared.LogIgnoredError(pkglogger.Get(), "mcp-ida: OnShutdown", shutdowner.Shutdown())
	}
	return cfg, nil
}

func stripBootSnapshotCapabilities(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("mcp-ida: parse bootstrap snapshot: %w", err)
	}
	if _, ok := snapshot["capabilities"]; !ok {
		return append(json.RawMessage(nil), raw...), nil
	}
	delete(snapshot, "capabilities")
	sanitized, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("mcp-ida: sanitize bootstrap snapshot: %w", err)
	}
	return sanitized, nil
}

func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client, server *common.Server) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client, stdioReady: server.Ready()}
}

// Run 启动IDA后台流程。
func (r bootstrapRunner) Run(ctx context.Context) error {
	r.client.InstallLogRelay()
	// Dual-channel startup ordering: wait for the local stdio MCP server
	// to be ready before connecting to the control-plane jrpc2. This
	// guarantees the tool-execution surface is available when the
	// control plane starts dispatching requests.
	if r.stdioReady != nil {
		select {
		case <-r.stdioReady:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if strings.TrimSpace(r.cfg.RPCAddr) == "" {
		return errors.New("mcp-ida: GO_AGENT_CTL_RPC_ADDR is required")
	}
	if err := r.client.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	return r.client.Close()
}

// emptyToolProvider implements common.ToolProvider with an empty tool list.
// mcp-ida currently exposes no local tools via MCP; IDA tool definitions
// will be added here once migrated from V2 (see docs/plans/迁移/audit-mcp-ida-tools.md).
type emptyToolProvider struct{}

// ListTools 返回当前 peer 暴露的工具列表。
func (emptyToolProvider) ListTools(context.Context) ([]mcp.MCPTool, error) {
	return []mcp.MCPTool{}, nil
}

// CallTool 调用当前 peer 暴露的工具。
func (emptyToolProvider) CallTool(_ context.Context, name string, _ json.RawMessage) (any, error) {
	return nil, errors.New("unknown tool: " + name)
}

func newStdioServer() *common.Server {
	stdout := mcpStdout.Load()
	if stdout == nil {
		stdout = os.Stdout
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer("mcp-ida", "dev", transport, emptyToolProvider{})
}

// newStdioRunner adapts the stdio *common.Server as a Runner for the run group.
func newStdioRunner(server *common.Server) platformrunner.Runner {
	return server
}

// bindRuntime 绑定运行时。
func bindRuntime(lc fx.Lifecycle, params runtimeParams) {
	log := pkglogger.Get()
	var (
		cancel       context.CancelFunc
		shutdownOnce sync.Once
	)
	done := make(chan error, 1)
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			platformshared.LogIgnoredError(log, "mcp-ida shutdown error", params.Shutdowner.Shutdown())
		})
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// Sidecar lifecycle: context.Background() is intentional here.
			// Unlike the main app (internal/app/runner.go) which derives runCtx
			// from an owner-supplied RootCtxProvider, sidecars run as independent
			// child processes. Their lifetime is governed by:
			//   1. Parent process kill / OnShutdown callback -> fx.Shutdowner.Shutdown()
			//   2. RunGroup self-exit -> requestShutdown()
			// Both paths converge on OnStop, which calls cancel() and waits for
			// the run group to drain via the done channel.
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			runtimesafe.SafeGo(runCtx, log, "mcp-ida.runtime.runGroup", func(context.Context) {
				err := platformrunner.RunGroup(runCtx, params.Runners, platformrunner.GroupOptions{
					EnableSignals: false,
				})
				done <- err
				close(done)
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Error("mcp-ida runtime exited", "error", err)
				}
				requestShutdown()
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			select {
			case err := <-done:
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
