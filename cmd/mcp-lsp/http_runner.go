package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const httpLSPBinaryName = "mcp-lsp"

var errLSPHTTPSessionTokenRequired = errors.New("mcp-lsp http: GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN required in peer mode")

// httpRunner starts an HTTP MCP endpoint in peer mode so that
// multiple Claude CLI agents can share a single mcp-lsp process.
type httpRunner struct {
	tools       common.ToolProvider
	bearerToken string
}

func newHTTPRunner(handlers ToolHandlers) platformrunner.Runner {
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		return lspBlockRunner{}
	}
	return &httpRunner{
		tools:       registryToolProvider{defs: toolDefinitions(handlers)},
		bearerToken: bootstrap.SessionTokenFromEnv(),
	}
}

// lspBlockRunner is a no-op runner that blocks until its context is cancelled.
type lspBlockRunner struct{}

// Run 启动LSP后台流程。
func (lspBlockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Run 启动LSP后台流程。
func (r *httpRunner) Run(ctx context.Context) error {
	if strings.TrimSpace(r.bearerToken) == "" {
		return errLSPHTTPSessionTokenRequired
	}
	//lint:ignore SA1019 peer HTTP transport is retained for legacy Claude CLI sharing mode.
	srv := common.NewHTTPServer(httpLSPBinaryName, binaryVersion, r.tools, common.WithBearerToken(r.bearerToken))
	//lint:ignore SA1019 peer HTTP transport is retained for legacy Claude CLI sharing mode.
	addr, err := srv.Start(ctx, "127.0.0.1:0")
	if err != nil {
		pkglogger.Warn("mcp-lsp http: start failed", "error", err)
		return err
	}

	if err := discovery.WritePeerDiscovery(httpLSPBinaryName, addr); err != nil {
		pkglogger.Warn("mcp-lsp http: discovery write failed", "error", err)
	}

	pkglogger.Info("mcp-lsp http: listening",
		"addr", addr, "binary", httpLSPBinaryName)

	<-ctx.Done()

	_ = discovery.CleanupPeerDiscovery(httpLSPBinaryName)
	stopCtx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	//lint:ignore SA1019 peer HTTP transport is retained for legacy Claude CLI sharing mode.
	return srv.Stop(stopCtx)
}
