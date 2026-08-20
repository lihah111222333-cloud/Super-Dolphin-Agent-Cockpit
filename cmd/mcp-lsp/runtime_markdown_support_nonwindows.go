//go:build !windows

package main

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// newRuntimeMarkdownClientSupport 在非 Windows 平台只安装设置通知 seam，
// 不伪造 Windows 专用 markdown/* 文件系统协议。
type runtimeNonWindowsMarkdownClientSupport struct{}

func (runtimeNonWindowsMarkdownClientSupport) RequestHandler() multilsp.ServerRequestHandler {
	return nil
}

func (runtimeNonWindowsMarkdownClientSupport) ServerNotificationHandler() multilsp.ServerNotificationHandler {
	return nil
}

func (runtimeNonWindowsMarkdownClientSupport) Attach(multilsp.Client) {}

func (runtimeNonWindowsMarkdownClientSupport) Healthy() bool { return true }

func (runtimeNonWindowsMarkdownClientSupport) Close() error { return nil }

// newRuntimeMarkdownClientSupport supplies the settings notification on
// non-Windows platforms without pretending to implement the Windows custom
// markdown/* filesystem protocol.
func newRuntimeMarkdownClientSupport(adapter multilsp.LanguageAdapter, _ string, _ []string, _ string) (runtimeMarkdownClientSupport, error) {
	if adapter == nil || !runtimeMarkdownAdapter(adapter) {
		return nil, nil
	}
	return runtimeNonWindowsMarkdownClientSupport{}, nil
}

// wrapRuntimeMarkdownClient 在 initialize 后发送真实 Markdown 服务端设置。
type runtimeNonWindowsMarkdownClient struct {
	multilsp.Client
}

func (c *runtimeNonWindowsMarkdownClient) Initialize(ctx context.Context, rootURI string) error {
	if err := c.Client.Initialize(ctx, rootURI); err != nil {
		return err
	}
	return runtimeMarkdownNotifyConfiguration(ctx, c.Client)
}

func (c *runtimeNonWindowsMarkdownClient) UnderlyingLSPClient() multilsp.Client {
	if c == nil {
		return nil
	}
	return c.Client
}

func (c *runtimeNonWindowsMarkdownClient) ServerCapabilities() protocol.ServerCapabilities {
	if c == nil || c.Client == nil {
		return protocol.ServerCapabilities{}
	}
	capabilities, ok := c.Client.(multilsp.ServerCapabilitiesClient)
	if !ok {
		return protocol.ServerCapabilities{}
	}
	return capabilities.ServerCapabilities()
}

func (c *runtimeNonWindowsMarkdownClient) Healthy() bool {
	if c == nil || c.Client == nil {
		return false
	}
	health, ok := c.Client.(multilsp.HealthCheckedClient)
	return ok && health.Healthy()
}

// wrapRuntimeMarkdownClient sends the documented configuration notification
// after initialize while preserving the underlying server capability snapshot.
func wrapRuntimeMarkdownClient(client multilsp.Client, support runtimeMarkdownClientSupport) multilsp.Client {
	if client == nil || support == nil {
		return client
	}
	return &runtimeNonWindowsMarkdownClient{Client: client}
}

var (
	_ multilsp.Client                   = (*runtimeNonWindowsMarkdownClient)(nil)
	_ multilsp.WrappedClient            = (*runtimeNonWindowsMarkdownClient)(nil)
	_ multilsp.HealthCheckedClient      = (*runtimeNonWindowsMarkdownClient)(nil)
	_ multilsp.ServerCapabilitiesClient = (*runtimeNonWindowsMarkdownClient)(nil)
)
