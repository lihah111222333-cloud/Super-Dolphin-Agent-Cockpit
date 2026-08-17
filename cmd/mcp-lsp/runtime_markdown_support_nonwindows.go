//go:build !windows

package main

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// newRuntimeMarkdownClientSupport 在非 Windows 平台不安装 Markdown client-side seam。
// 这保持 Linux/macOS 原有的 client 请求处理和 Node cohort 不变。
func newRuntimeMarkdownClientSupport(_ multilsp.LanguageAdapter, _ string, _ []string, _ string) (runtimeMarkdownClientSupport, error) {
	return nil, nil
}

// wrapRuntimeMarkdownClient 在非 Windows 平台保持原始 LSP client，不改变既有行为。
func wrapRuntimeMarkdownClient(client multilsp.Client, _ runtimeMarkdownClientSupport) multilsp.Client {
	return client
}
