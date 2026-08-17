//go:build windows

package search

import (
	"path/filepath"
	"strings"
)

// foreignPathFamily 在 Windows 构建中拒绝跨 WSL 边界后仍使用 /mnt/<drive>/
// 表示的输入；windows build tag 是路径族行为的源码选择边界。
func foreignPathFamily(path string) bool {
	if path == "" {
		return false
	}
	return isWSLMountPath(path)
}

// isWSLMountPath 识别 Windows 进程收到的 /mnt/<drive>/ 路径。
func isWSLMountPath(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(path))
	return strings.HasPrefix(normalized, "/mnt/") && len(normalized) > len("/mnt/x/") &&
		normalized[5] >= 'a' && normalized[5] <= 'z' && normalized[6] == '/'
}
