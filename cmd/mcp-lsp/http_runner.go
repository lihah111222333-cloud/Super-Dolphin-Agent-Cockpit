package main

import (
	"context"
	"os"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const httpLSPBinaryName = "mcp-lsp"

// httpRunner starts an HTTP MCP endpoint in peer mode so that
// multiple Claude CLI agents can share a single mcp-lsp process.
type httpRunner struct {
	tools common.ToolProvider
}

func newHTTPRunner(handlers ToolHandlers) platformrunner.Runner {
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		return lspBlockRunner{}
	}
	return &httpRunner{tools: registryToolProvider{defs: toolDefinitions(handlers)}}
}

// lspBlockRunner is a no-op runner that blocks until its context is cancelled.
type lspBlockRunner struct{}

func (lspBlockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (r *httpRunner) Run(ctx context.Context) error {
	srv := common.NewHTTPServer(httpLSPBinaryName, binaryVersion, r.tools)
	addr, err := srv.Start(ctx, "127.0.0.1:0")
	if err != nil {
		pkglogger.Warn("mcp-lsp http: start failed", "error", err)
		return err
	}

	if err := common.WritePeerDiscovery(httpLSPBinaryName, addr); err != nil {
		pkglogger.Warn("mcp-lsp http: discovery write failed", "error", err)
	}

	pkglogger.Info("mcp-lsp http: listening",
		"addr", addr, "binary", httpLSPBinaryName)

	<-ctx.Done()

	_ = common.CleanupPeerDiscovery(httpLSPBinaryName)
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Stop(stopCtx)
}
