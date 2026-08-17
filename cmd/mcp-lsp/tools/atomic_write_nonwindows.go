//go:build !windows

package tools

import (
	"os"
)

// Rename 在 POSIX 平台原子替换目标路径。
func (osFileWriter) Rename(oldPath string, newPath string) error {
	return os.Rename(oldPath, newPath)
}
