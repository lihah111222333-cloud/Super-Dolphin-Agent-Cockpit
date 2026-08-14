//go:build !windows

package search

import "path/filepath"

// platformNormalizeSearchGlob 保持 POSIX 原有的 filepath.ToSlash 语义。
func platformNormalizeSearchGlob(raw string) string {
	return filepath.ToSlash(raw)
}
