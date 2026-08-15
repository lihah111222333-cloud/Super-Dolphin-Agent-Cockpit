//go:build linux

package tools

import (
	"path/filepath"
	"strings"
)

// normalizePlatformWorkDir 把 Windows 主机传给 WSL sidecar 的绝对路径转换成挂载路径。
func normalizePlatformWorkDir(workDir string) string {
	if len(workDir) < 3 || workDir[1] != ':' || (workDir[2] != '\\' && workDir[2] != '/') {
		return workDir
	}
	drive := workDir[0]
	if drive >= 'A' && drive <= 'Z' {
		drive += 'a' - 'A'
	}
	if drive < 'a' || drive > 'z' {
		return workDir
	}
	remainder := strings.ReplaceAll(workDir[3:], "\\", "/")
	return filepath.Clean("/mnt/" + string(drive) + "/" + remainder)
}
