package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// httpBinaryName 是 peer discovery 文件中的二进制名。
const httpBinaryName = "mcp-orch"

// errOrchHTTPSessionTokenRequired 表示 peer HTTP 模式缺少 bearer token。
var errOrchHTTPSessionTokenRequired = errors.New("mcp-orch http: GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN required in peer mode")

// httpRunner 在 peer 模式下启动 HTTP MCP 端点，供多个 Claude CLI agent 共享同一个 mcp-orch 进程。
type httpRunner struct {
	tools       common.ToolProvider
	bearerToken string
}

// newHTTPRunner 在 peer 模式下创建 HTTP runner；非 peer 模式返回阻塞空 runner。
func newHTTPRunner(registry tools.Registry) platformrunner.Runner {
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		// Non-peer mode: return a runner that blocks until context done.
		return blockRunner{}
	}
	return &httpRunner{
		tools:       registryToolProvider{registry: registry},
		bearerToken: bootstrap.SessionTokenFromEnv(),
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
	srv := common.NewHTTPServer(httpBinaryName, "dev", r.tools, common.WithBearerToken(r.bearerToken))
	addr, err := srv.Start(ctx, "127.0.0.1:0")
	if err != nil {
		pkglogger.Warn("mcp-orch http: start failed", "error", err)
		return err
	}

	// Write discovery file so BuildManifest() can find this endpoint.
	if err := discovery.WritePeerDiscovery(httpBinaryName, addr); err != nil {
		pkglogger.Warn("mcp-orch http: discovery write failed", "error", err)
		// Non-fatal: Claude will fall back to stdio.
	}

	pkglogger.Info("mcp-orch http: listening",
		"addr", addr, "binary", httpBinaryName)

	<-ctx.Done()

	// Cleanup discovery file on shutdown.
	_ = discovery.CleanupPeerDiscovery(httpBinaryName)
	stopCtx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Stop(stopCtx)
}
