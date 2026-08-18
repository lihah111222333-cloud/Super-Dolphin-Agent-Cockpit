//go:build windows

package tools

import (
	"path/filepath"
	"strings"
)

// normalizePlatformWorkDir 保留 Windows sidecar 使用的主机绝对路径。
func normalizePlatformWorkDir(workDir string) string {
	return workDir
}

// hostColdInstallOuterTimeoutDisabled 在 Windows 构建中关闭工具层冷安装总 deadline。
func hostColdInstallOuterTimeoutDisabled() bool {
	return true
}

// sameDiagnosticPath 按 Windows 路径身份比较诊断目标，盘符与路径大小写不参与身份。
func sameDiagnosticPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
