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

const httpBinaryName = "mcp-orch"

var errOrchHTTPSessionTokenRequired = errors.New("mcp-orch http: GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN required in peer mode")

// httpRunner starts an HTTP MCP endpoint in peer mode so that
// multiple Claude CLI agents can share a single mcp-orch process.
type httpRunner struct {
	tools       common.ToolProvider
	bearerToken string
}

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

// blockRunner is a no-op runner that blocks until its context is cancelled.
type blockRunner struct{}

// Run 启动编排后台流程。
func (blockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Run 启动编排后台流程。
func (r *httpRunner) Run(ctx context.Context) error {
	if strings.TrimSpace(r.bearerToken) == "" {
		return errOrchHTTPSessionTokenRequired
	}
	//lint:ignore SA1019 peer HTTP transport is retained for legacy Claude CLI sharing mode.
	srv := common.NewHTTPServer(httpBinaryName, "dev", r.tools, common.WithBearerToken(r.bearerToken))
	//lint:ignore SA1019 peer HTTP transport is retained for legacy Claude CLI sharing mode.
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
	//lint:ignore SA1019 peer HTTP transport is retained for legacy Claude CLI sharing mode.
	return srv.Stop(stopCtx)
}
