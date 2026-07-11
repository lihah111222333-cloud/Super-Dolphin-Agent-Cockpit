// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/discovery"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const httpLSPBinaryName = "mcp-lsp"

var errLSPHTTPSessionTokenRequired = errors.New("mcp-lsp http: GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN required in peer mode")

// httpRunner 在 peer 模式下启动 HTTP MCP 端点，允许多个 Claude CLI agent 共享同一 mcp-lsp 进程。
type httpRunner struct {
	tools       common.ToolProvider
	bearerToken string
}

// newHTTPRunner 根据 peer 配置选择 HTTP MCP runner 或空阻塞 runner。
// 非 peer 模式仍要阻塞进程生命周期，避免 sidecar 初始化后立即退出。
func newHTTPRunner(handlers ToolHandlers) platformrunner.Runner {
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		return lspBlockRunner{}
	}
	return &httpRunner{
		tools:       registryToolProvider{defs: toolDefinitions(handlers)},
		bearerToken: bootstrap.SessionTokenFromEnv(),
	}
}

// lspBlockRunner 是非 peer 模式下的空 runner，阻塞直到 ctx 取消。
type lspBlockRunner struct{}

// Run 阻塞直到 ctx 取消后退出。
func (lspBlockRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Run 启动 HTTP MCP server，注册 peer discovery，等待 ctx 取消后优雅停机。
func (r *httpRunner) Run(ctx context.Context) error {
	if strings.TrimSpace(r.bearerToken) == "" {
		return errLSPHTTPSessionTokenRequired
	}
	srv := common.NewHTTPServer(httpLSPBinaryName, binaryVersion, r.tools, common.WithBearerToken(r.bearerToken))
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
	return srv.Stop(stopCtx)
}
