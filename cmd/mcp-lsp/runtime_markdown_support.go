package main

import (
	"context"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeMarkdownClientSupport 是 Markdown server 的客户端协议 seam。
// Windows 实现额外承载官方 markdown/* 文件系统请求；其他平台只启用设置通知。
type runtimeMarkdownClientSupport interface {
	RequestHandler() multilsp.ServerRequestHandler
	ServerNotificationHandler() multilsp.ServerNotificationHandler
	Attach(multilsp.Client)
	Healthy() bool
	Close() error
}

const runtimeMarkdownItInstallVersion = "14.2.0"

func runtimeMarkdownAdapter(adapter multilsp.LanguageAdapter) bool {
	if adapter == nil {
		return false
	}
	for _, languageID := range adapter.LanguageIDs() {
		if strings.EqualFold(strings.TrimSpace(languageID), "markdown") {
			return true
		}
	}
	return false
}

// runtimeMarkdownConfigurationSettings enables the real server's path
// completion feature through its documented configuration notification. The
// validation fields are included because the server reads the complete
// settings shape when this notification arrives; validation remains disabled
// so this capability seam does not opt the generic client into extra FS work.
func runtimeMarkdownConfigurationSettings() map[string]any {
	return map[string]any{
		"markdown": map[string]any{
			"suggest": map[string]any{
				"paths": map[string]any{
					"enabled":                           true,
					"includeWorkspaceHeaderCompletions": "onSingleOrDoubleHash",
				},
			},
			"occurrencesHighlight": map[string]any{"enabled": false},
			"validate": map[string]any{
				"enabled":        false,
				"referenceLinks": map[string]any{"enabled": "ignore"},
				"fragmentLinks":  map[string]any{"enabled": "ignore"},
				"fileLinks": map[string]any{
					"enabled":               "ignore",
					"markdownFragmentLinks": "inherit",
				},
				"ignoredLinks":          []string{},
				"unusedLinkDefinitions": map[string]any{"enabled": "ignore"},
				"duplicateLinkDefinitions": map[string]any{
					"enabled": "ignore",
				},
			},
		},
	}
}

// runtimeMarkdownNotifyConfiguration activates settings-driven server
// features only after the LSP initialize handshake has completed.
func runtimeMarkdownNotifyConfiguration(ctx context.Context, client multilsp.Client) error {
	if client == nil {
		return nil
	}
	return client.Notify(ctx, "workspace/didChangeConfiguration", map[string]any{
		"settings": runtimeMarkdownConfigurationSettings(),
	})
}

// runtimeMarkdownClientSupportForAdapter 仅为 Markdown adapter 创建官方客户端协议处理器。
func runtimeMarkdownClientSupportForAdapter(adapter multilsp.LanguageAdapter, root string, env []string, serverBinary string) (runtimeMarkdownClientSupport, error) {
	return newRuntimeMarkdownClientSupport(adapter, root, env, serverBinary)
}
