//go:build windows && e2e

package manager_test

import (
	"net/url"
	"path/filepath"
	"strings"
)

// e2eFileURI 生成 Windows 多语言 E2E 使用的标准 file URI。
func e2eFileURI(path string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}
