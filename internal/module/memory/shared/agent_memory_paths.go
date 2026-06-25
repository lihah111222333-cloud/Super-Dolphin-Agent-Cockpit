package memshared

import (
	"path/filepath"
	"strings"
)

// 历史遗留路径目录名常量，仅用于识别和拒绝旧数据，不应再被写入。
const (
	historicalAgentMemoryDir      = "agent-memory"
	historicalAgentMemoryLocalDir = "agent-memory-local"
)

// IsHistoricalAgentMemoryPath 检查路径中是否包含已废弃的 agent-memory 目录段，
// 用于阻止历史数据通过合并或嵌套触发路径重新引入系统。
func IsHistoricalAgentMemoryPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	clean, err := CleanAbsolutePath(path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for _, part := range parts {
		switch part {
		case historicalAgentMemoryDir, historicalAgentMemoryLocalDir:
			return true
		}
	}
	return false
}
