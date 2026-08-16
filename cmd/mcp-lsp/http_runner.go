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
	bearerToken          string
	startServer          func(context.Context) (string, func(context.Context) error, error)
	writePeerDiscovery   func(string, string) error
	cleanupPeerDiscovery func(string) error
}

// newHTTPRunner 根据 peer 配置选择 HTTP MCP runner 或空阻塞 runner。
// 非 peer 模式仍要阻塞进程生命周期，避免 sidecar 初始化后立即退出。
func newHTTPRunner(handlers ToolHandlers, logRuntime *pkglogger.Runtime) (platformrunner.Runner, error) {
	if logRuntime == nil {
		return nil, errors.New("mcp-lsp logger runtime is required")
	}
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		return lspBlockRunner{}, nil
	}
	tools := registryToolProvider{defs: toolDefinitions(handlers)}
	bearerToken := bootstrap.SessionTokenFromEnv()
	return &httpRunner{
		bearerToken: bearerToken,
		startServer: func(ctx context.Context) (string, func(context.Context) error, error) {
			srv := common.NewHTTPServer(httpLSPBinaryName, binaryVersion, tools, common.WithBearerToken(bearerToken), common.WithHTTPLoggerRuntime(logRuntime), common.WithHTTPToolCallResultPolicy(lspToolCallResultPolicy()))
			addr, err := srv.Start(ctx, "127.0.0.1:0")
			return addr, srv.Stop, err
		},
		writePeerDiscovery:   discovery.WritePeerDiscovery,
		cleanupPeerDiscovery: discovery.CleanupPeerDiscovery,
	}, nil
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
	addr, stopServer, err := r.startServer(ctx)
	if err != nil {
		pkglogger.Warn("mcp-lsp http: start failed", "error", err)
		return err
	}

	if discoveryErr := r.writePeerDiscovery(httpLSPBinaryName, addr); discoveryErr != nil {
		pkglogger.Warn("mcp-lsp http: discovery write failed", "error", discoveryErr)
		stopCtx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(discoveryErr, stopServer(stopCtx))
	}

	pkglogger.Info("mcp-lsp http: listening",
		"addr", addr, "binary", httpLSPBinaryName)

	<-ctx.Done()

	cleanupErr := r.cleanupPeerDiscovery(httpLSPBinaryName)
	stopCtx, cancel := platformconfig.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return errors.Join(cleanupErr, stopServer(stopCtx))
}
