package shared

import (
	"path/filepath"
	"strings"
)

func NormalizeRelativePath(path string) string {
	return filepath.Clean(strings.TrimSpace(path))
}

func ContainsPath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
