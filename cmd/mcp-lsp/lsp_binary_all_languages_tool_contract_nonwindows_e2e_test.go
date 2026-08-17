//go:build !windows && e2e

package main

import (
	"os"
	"path/filepath"
)

// 非 Windows 保留原始命令名；POSIX 平台通过显式 chmod 使复制的 fake binary 可执行。
func allLanguageToolContractExecutableName(name string) string {
	return name
}

// 非 Windows 复制 fake binary 后显式设置 POSIX owner-only 执行权限，保持原有受控测试语义。
func allLanguageToolContractPrepareExecutable(path string) error {
	return os.Chmod(path, 0o700)
}

// 非 Windows file URI 直接按 POSIX slash 规则转换为本机路径。
func allLanguageToolContractNativePathFromURIPath(path string) string {
	return filepath.FromSlash(path)
}
