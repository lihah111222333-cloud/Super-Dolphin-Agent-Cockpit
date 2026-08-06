// Package main 是 mcp-ida sidecar 进程的入口，通过 MCP stdio 协议暴露 IDA 能力。
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	"go.uber.org/fx"
)

// bootstrapRunner 持有启动配置和控制面客户端，等待 stdio server 就绪后再连接 RPC。
type bootstrapRunner struct {
	cfg        bootstrap.Config
	client     *bootstrap.Client
	stdioReady <-chan struct{} // stdio server ready 后关闭，保证控制面连接前工具通道可用。
}

// runtimeParams 聚合 fx 注入的 runner 列表和 shutdowner，供 bindRuntime 使用。
type runtimeParams struct {
	fx.In

	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

// run 组装并启动 mcp-ida sidecar 自身的 fx 应用。
// 该进程只暴露控制面握手和空工具面，具体 IDA 能力由后续 provider 接入时显式注册。
func run(stdout *os.File) error {
	if stdout == nil {
		return errors.New("mcp-ida: MCP stdout owner is required")
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			platformmetrics.NewBootstrapMetrics,
			buildBootstrapConfig,
			bootstrap.New,
			newStdioServer,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
		),
		fx.Supply(stdout),
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
func buildBootstrapConfig(shutdowner fx.Shutdowner, metrics *platformmetrics.BootstrapMetrics) (bootstrap.Config, error) {
	cfg := bootstrap.ReadBootConfig()
	cfg.AgentID = ""
	cfg.Metrics = metrics
	var err error
	cfg.BootSnapshot, err = stripBootSnapshotCapabilities(cfg.BootSnapshot)
	if err != nil {
		return bootstrap.Config{}, err
	}
	// 空 provider 只是协议占位；未注册真实 schema/handler 前不能上报 tools/ida 能力。
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
		logConfigChanged(notify)
	}
	cfg.OnShutdown = func(mcp.ShutdownRequest) {
		platformshared.LogIgnoredError(pkglogger.Get(), "mcp-ida: OnShutdown", shutdowner.Shutdown())
	}
	return cfg, nil
}

// logConfigChanged 记录配置变更的排障元信息，不记录原始 payload，避免控制面配置内容泄漏。
func logConfigChanged(notify mcp.ConfigChangedNotify) {
	payloadHash := sha256.Sum256(notify.Payload)
	pkglogger.Info("mcp-ida config changed",
		"scope", notify.Scope,
		"config_version", notify.ConfigVersion,
		"selector", notify.Selector,
		"payload_size", len(notify.Payload),
		"payload_hash", fmt.Sprintf("%x", payloadHash),
	)
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
	// 双通道启动顺序：先等本地 stdio MCP server 就绪，再连接控制面 jrpc2。
	// 这样控制面开始派发请求时，工具执行通道已经存在。
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

// emptyToolProvider 维持 mcp-ida 的 MCP 协议形状，但不声明任何本地工具。
// 调用方只能获得空列表；若误调用工具会 fail-fast 返回 unknown tool。
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
func newStdioServer(stdout *os.File) (*common.Server, error) {
	if stdout == nil {
		return nil, errors.New("mcp-ida: MCP stdout owner is required")
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer("mcp-ida", "dev", transport, emptyToolProvider{}), nil
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
			// sidecar 是独立子进程，不能继承主应用 RootCtxProvider。
			// 父进程关闭、控制面 OnShutdown 或 RunGroup 自退出最终都会进入 OnStop，统一 cancel 并等待 done。
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
