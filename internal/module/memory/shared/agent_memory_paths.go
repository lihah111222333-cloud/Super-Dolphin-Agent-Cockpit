package memshared

import (
	"path/filepath"
	"strings"
)

const (
	historicalAgentMemoryDir      = "agent-memory"
	historicalAgentMemoryLocalDir = "agent-memory-local"
)

// IsHistoricalAgentMemoryPath recognizes legacy agent-memory directories after
// the feature surface has been removed. It is intentionally only a deny/ignore
// helper so historical data cannot be reintroduced through consolidation or
// nested trigger paths.
// IsHistoricalAgentMemoryPath 判断historical代理记忆路径是否可用。
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
