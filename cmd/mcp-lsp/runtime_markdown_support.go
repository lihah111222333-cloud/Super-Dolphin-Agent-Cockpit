package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"

// runtimeMarkdownClientSupport 是 Markdown server 的 Windows client-side protocol seam。
// 非 Windows 实现返回 nil，确保既有平台仍沿用原始 transport 行为。
type runtimeMarkdownClientSupport interface {
	RequestHandler() multilsp.ServerRequestHandler
	ServerNotificationHandler() multilsp.ServerNotificationHandler
	Attach(multilsp.Client)
	Healthy() bool
	Close() error
}

const runtimeMarkdownItInstallVersion = "14.2.0"

// runtimeMarkdownClientSupportForAdapter 仅为 Markdown adapter 创建官方客户端协议处理器。
func runtimeMarkdownClientSupportForAdapter(adapter multilsp.LanguageAdapter, root string, env []string, serverBinary string) (runtimeMarkdownClientSupport, error) {
	return newRuntimeMarkdownClientSupport(adapter, root, env, serverBinary)
}
