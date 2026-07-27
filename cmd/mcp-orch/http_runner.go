package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/discovery"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// httpBinaryName 是 peer discovery 文件中的二进制名。
const httpBinaryName = "mcp-orch"

// errOrchHTTPSessionTokenRequired 表示 peer HTTP 模式缺少 bearer token。
var errOrchHTTPSessionTokenRequired = errors.New("mcp-orch http: GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN required in peer mode")

// httpRunner 在 peer 模式下启动 HTTP MCP 端点，供多个 Claude CLI agent 共享同一个 mcp-orch 进程。
type httpRunner struct {
	bearerToken          string
	startServer          func(context.Context) (string, func(context.Context) error, error)
	writePeerDiscovery   func(string, string) error
	cleanupPeerDiscovery func(string) error
}

// newHTTPRunner 在 peer 模式下创建 HTTP runner；非 peer 模式返回阻塞空 runner。
func newHTTPRunner(registry tools.Registry) platformrunner.Runner {
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		// Non-peer mode: return a runner that blocks until context done.
		return blockRunner{}
	}
	toolProvider := registryToolProvider{registry: registry}
	bearerToken := bootstrap.SessionTokenFromEnv()
	return &httpRunner{
		bearerToken: bearerToken,
		startServer: func(ctx context.Context) (string, func(context.Context) error, error) {
			srv := common.NewHTTPServer(
				httpBinaryName,
				"dev",
				toolProvider,
				common.WithBearerToken(bearerToken),
				common.WithHTTPToolErrorClassifier(tools.ToolErrorClassifier),
			)
			addr, err := srv.Start(ctx, "127.0.0.1:0")
			return addr, srv.Stop, err
		},
		writePeerDiscovery:   discovery.WritePeerDiscovery,
		cleanupPeerDiscovery: discovery.CleanupPeerDiscovery,
	}
}

// blockRunner 是非 peer 模式的占位 runner，只等待上下文取消。
type blockRunner struct{}

// Run 阻塞到上下文取消，保持 RunGroup 生命周期形状一致。
func (blockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Run 启动 peer HTTP MCP 端点，并在退出时清理 discovery 文件。
func (r *httpRunner) Run(ctx context.Context) error {
	if strings.TrimSpace(r.bearerToken) == "" {
		return errOrchHTTPSessionTokenRequired
	}
	addr, stopServer, err := r.startServer(ctx)
	if err != nil {
		pkglogger.Warn("mcp-orch http: start failed", "error", err)
		return err
	}

	// Write discovery file so BuildManifest() can find this endpoint.
	if discoveryErr := r.writePeerDiscovery(httpBinaryName, addr); discoveryErr != nil {
		pkglogger.Warn("mcp-orch http: discovery write failed", "error", discoveryErr)
		stopCtx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(discoveryErr, stopServer(stopCtx))
	}

	pkglogger.Info("mcp-orch http: listening",
		"addr", addr, "binary", httpBinaryName)

	<-ctx.Done()

	// Cleanup discovery file on shutdown.
	cleanupErr := r.cleanupPeerDiscovery(httpBinaryName)
	stopCtx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return errors.Join(cleanupErr, stopServer(stopCtx))
}
