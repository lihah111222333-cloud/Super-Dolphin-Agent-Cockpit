//go:build !linux || !amd64

package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"

// registerLinuxCSharpInstaller 保持非 Linux 平台既有的系统安装行为。
func registerLinuxCSharpInstaller(_ *installer.Provider) error {
	return nil
}
