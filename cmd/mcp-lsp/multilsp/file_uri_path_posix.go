//go:build !windows

package multilsp

import "path/filepath"

// platformFileURIPath 保持 POSIX 路径原有的斜杠语义。
func platformFileURIPath(absPath string) string {
	return filepath.ToSlash(absPath)
}
