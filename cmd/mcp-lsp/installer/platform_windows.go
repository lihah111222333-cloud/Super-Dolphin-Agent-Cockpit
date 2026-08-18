//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func isUnsafeAssetFile(info os.FileInfo) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// npmInstallBinDirFromPrefix 在 Windows 构建中使用 npm 全局 prefix 本身；npm.cmd
// shim 位于该目录，windows build tag 防止该布局泄漏到其他平台。
func npmInstallBinDirFromPrefix(prefix string) string {
	return prefix
}

// executableInDir 在 Windows 构建中同时检查原名与 .exe 候选；Windows 不使用
// POSIX execute bits，但目录和缺失文件仍然严格拒绝。
func executableInDir(dir, binaryName string) (string, bool) {
	binaryName = strings.TrimSpace(binaryName)
	if strings.TrimSpace(dir) == "" || binaryName == "" {
		return "", false
	}
	candidates := []string{filepath.Join(dir, binaryName)}
	if filepath.Ext(binaryName) == "" {
		candidates = append(candidates, filepath.Join(dir, binaryName+".exe"))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// ensureWindowsCatalogProductionBranch keeps the native catalog entry point
// registered only by the Windows production build.
func ensureWindowsCatalogProductionBranch() error {
	return nil
}
