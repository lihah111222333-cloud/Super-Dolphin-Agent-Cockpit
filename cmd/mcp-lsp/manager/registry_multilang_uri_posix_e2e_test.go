//go:build !windows && e2e

package manager_test

import (
	"net/url"
	"path/filepath"
)

// e2eFileURI 保持 POSIX 多语言 E2E 原有的 file URI 语义。
func e2eFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
