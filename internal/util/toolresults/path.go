// Package toolresults 集中定义 tool-call 结果缓存目录。
// turn 写入方和 memory 嵌套读取方共同依赖这里，避免为了共享路径而反向导入上层模块。
package toolresults

import (
	"os"
	"path/filepath"
	"strings"
)

// CacheDir 返回 tool-results 缓存目录的绝对路径。
// 它只解析路径不创建目录；若用户缓存目录和临时目录都不可用则返回空字符串，调用方必须按 fail-closed 处理。
func CacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	if strings.TrimSpace(base) == "" {
		return ""
	}
	return filepath.Join(base, "super-agent-v3", "tool-results")
}
