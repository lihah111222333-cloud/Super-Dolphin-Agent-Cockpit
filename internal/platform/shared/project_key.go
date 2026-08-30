package shared

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/pathutil"

// MemoryProjectKeyFromCwd 根据工作目录生成 memory 专用项目键。
func MemoryProjectKeyFromCwd(cwd string) (string, error) {
	return pathutil.MemoryProjectKeyFromCwd(cwd)
}

// SanitizeMemoryProjectKey 清理 memory 项目键中的不安全字符。
func SanitizeMemoryProjectKey(raw string) string { return pathutil.SanitizeMemoryProjectKey(raw) }
