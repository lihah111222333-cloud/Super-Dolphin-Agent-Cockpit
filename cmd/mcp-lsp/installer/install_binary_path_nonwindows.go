//go:build !windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
)

// npmInstallBinDirFromPrefix 在非 Windows 构建中使用 npm prefix/bin；!windows
// build tag 明确隔离 POSIX 风格布局。
func npmInstallBinDirFromPrefix(prefix string) string {
	return filepath.Join(prefix, "bin")
}

// executableInDir 在非 Windows 构建中只接受带 execute bit 的普通文件，不追加
// Windows .exe 后缀。
func executableInDir(dir, binaryName string) (string, bool) {
	binaryName = strings.TrimSpace(binaryName)
	if strings.TrimSpace(dir) == "" || binaryName == "" {
		return "", false
	}
	candidate := filepath.Join(dir, binaryName)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "", false
	}
	return candidate, true
}
