// Package main 是 mcp-ida sidecar 进程的入口，通过 MCP stdio 协议暴露 IDA 能力。
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
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"go.uber.org/fx"
)

// bootstrapRunner 持有启动配置和控制面客户端，等待 stdio server 就绪后再连接 RPC。
type bootstrapRunner struct {
	cfg        bootstrap.Config
	client     *bootstrap.Client
	stdioReady <-chan struct{} // closed when stdio server is ready
}

// runtimeParams 聚合 fx 注入的 runner 列表和 shutdowner，供 bindRuntime 使用。
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

// buildBootstrapConfig 构建启动配置，清除 capabilities 字段并注册生命周期回调。
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

// stripBootSnapshotCapabilities 从启动快照中删除 capabilities 字段，避免 IDA peer 误报工具能力。
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

// newBootstrapRunner 构建 bootstrapRunner，等待 stdio server ready 信号后连接控制面。
func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client, server *common.Server) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client, stdioReady: server.Ready()}
}

// Run 启动 IDA 后台流程，等待 stdio server 就绪后连接控制面 RPC，直到 ctx 取消。
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

// ListTools 返回当前 peer 暴露的工具列表（当前为空）。
func (emptyToolProvider) ListTools(context.Context) ([]mcp.MCPTool, error) {
	return []mcp.MCPTool{}, nil
}

// CallTool 调用当前 peer 暴露的工具，当前无工具时始终返回 unknown tool 错误。
func (emptyToolProvider) CallTool(_ context.Context, name string, _ json.RawMessage) (any, error) {
	return nil, errors.New("unknown tool: " + name)
}

// newStdioServer 创建 stdio 传输层的 MCP server，使用受保护的 stdout 作为写端。
func newStdioServer() *common.Server {
	stdout := mcpStdout.Load()
	if stdout == nil {
		stdout = os.Stdout
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer("mcp-ida", "dev", transport, emptyToolProvider{})
}

// newStdioRunner 将 stdio *common.Server 适配为 Runner 加入运行组。
func newStdioRunner(server *common.Server) platformrunner.Runner {
	return server
}

// bindRuntime 将运行组生命周期绑定到 fx，OnStart 启动 goroutine 运行所有 runner，
// OnStop 取消 ctx 并等待运行组退出，超时时返回 ctx 错误。
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
