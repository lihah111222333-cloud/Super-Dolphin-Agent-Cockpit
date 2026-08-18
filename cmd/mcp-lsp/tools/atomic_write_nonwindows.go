//go:build !windows

package tools

import (
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// Rename 在 POSIX 平台原子替换目标路径。
func (osFileWriter) Rename(oldPath string, newPath string) error {
	return os.Rename(oldPath, newPath)
}

// syncParentDirectory 在 POSIX 平台持久化 rename 后的目录项。
func syncParentDirectory(dir string, _ fileWriter) error {
	return securefs.SyncDirectory(dir)
}
