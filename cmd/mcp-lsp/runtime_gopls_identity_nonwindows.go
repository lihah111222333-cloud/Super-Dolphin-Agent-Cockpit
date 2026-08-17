//go:build !windows

package main

import (
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/lspplatform"
)

// runtimeServerStableFilesystemIdentity 只使用 canonical root 的稳定 dev+ino，
// 拒绝把 mtime/size 等可变 stat 字段纳入 cohort proof。
func runtimeServerStableFilesystemIdentity(path string, info os.FileInfo) (string, error) {
	return lspplatform.StableDirectoryIdentity(path, info)
}
