//go:build !windows

package multilsp

import (
	"net/url"
	"path/filepath"
)

// platformCanonicalAbsolutePath 保持非 Windows 绝对路径的原始大小写与身份语义。
// 本实现故意只带 !windows build tag，Windows 盘符规则不会进入其他平台产物。
func platformCanonicalAbsolutePath(absPath string) string {
	return absPath
}

// platformFileURIPath 保持 POSIX 路径原有的斜杠语义。
func platformFileURIPath(absPath string) string {
	return filepath.ToSlash(absPath)
}

// platformFileURIFromPath 保持非 Windows 平台原有的 net/url file URI 编码语义。
// 本实现故意只带 !windows build tag，Windows 盘符规范化不会进入其他平台产物。
func platformFileURIFromPath(absPath string) string {
	return (&url.URL{Scheme: "file", Path: platformFileURIPath(absPath)}).String()
}
