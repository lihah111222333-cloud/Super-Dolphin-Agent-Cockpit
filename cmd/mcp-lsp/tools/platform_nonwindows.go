//go:build !windows

package tools

import "path/filepath"

// hostColdInstallOuterTimeoutDisabled 在非 Windows 构建中保留工具层冷安装总 deadline。
func hostColdInstallOuterTimeoutDisabled() bool {
	return false
}

// sameDiagnosticPath 在非 Windows 平台保留大小写敏感的路径身份。
func sameDiagnosticPath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
