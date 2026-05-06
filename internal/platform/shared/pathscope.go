package shared

import (
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

func NormalizeRelativePath(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

// ContainsPath delegates to pathutil.ContainsPath.
func ContainsPath(root, target string) bool { return pathutil.ContainsPath(root, target) }
