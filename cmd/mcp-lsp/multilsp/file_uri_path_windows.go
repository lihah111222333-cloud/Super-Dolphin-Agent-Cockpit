//go:build windows

package multilsp

import (
	"path/filepath"
	"strings"
)

// platformFileURIPath 把 Windows 盘符路径转换为 file URI 的绝对 path 部分。
func platformFileURIPath(absPath string) string {
	uriPath := filepath.ToSlash(absPath)
	if filepath.VolumeName(absPath) != "" && !strings.HasPrefix(uriPath, "/") {
		return "/" + uriPath
	}
	return uriPath
}
