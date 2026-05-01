package main

import (
	"context"
	"os"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const httpBinaryName = "mcp-orch"

// httpRunner starts an HTTP MCP endpoint in peer mode so that
// multiple Claude CLI agents can share a single mcp-orch process.
type httpRunner struct {
	tools common.ToolProvider
}

func newHTTPRunner(registry tools.Registry) platformrunner.Runner {
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		// Non-peer mode: return a runner that blocks until context done.
		return blockRunner{}
	}
	return &httpRunner{tools: registryToolProvider{registry: registry}}
}

// blockRunner is a no-op runner that blocks until its context is cancelled.
type blockRunner struct{}

func (blockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (r *httpRunner) Run(ctx context.Context) error {
	srv := common.NewHTTPServer(httpBinaryName, "dev", r.tools)
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
