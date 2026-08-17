//go:build !windows

package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"

// runtimeServerProductProfile 非 Windows 没有 Windows product-owned profile，保持公共能力原样。
func runtimeServerProductProfile(_ multilsp.LanguageAdapter, _ multilsp.ServerCommand, _ string, _ []string) string {
	return ""
}
