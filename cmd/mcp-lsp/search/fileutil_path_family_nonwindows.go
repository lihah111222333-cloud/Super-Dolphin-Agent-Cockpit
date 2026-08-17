//go:build !windows

package search

import "strings"

// foreignPathFamily 在非 Windows 构建中拒绝盘符与 UNC 绝对路径；!windows build
// tag 保证该路径族判断不会被编入 Windows 产物。
func foreignPathFamily(path string) bool {
	if path == "" {
		return false
	}
	return isWindowsAbsolutePath(path)
}

// isWindowsAbsolutePath 识别非 Windows 进程收到的盘符或 UNC 绝对路径。
func isWindowsAbsolutePath(path string) bool {
	if strings.HasPrefix(path, `\\`) {
		return true
	}
	if len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return false
	}
	return (path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')
}
