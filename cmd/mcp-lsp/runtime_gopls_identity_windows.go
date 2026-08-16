//go:build windows

package main

import (
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
)

// runtimeServerStableFilesystemIdentity 明确拒绝 Windows 的未证明 root cohort identity。
func runtimeServerStableFilesystemIdentity(path string, info os.FileInfo) (string, error) {
	return lspplatform.StableDirectoryIdentity(path, info)
}
