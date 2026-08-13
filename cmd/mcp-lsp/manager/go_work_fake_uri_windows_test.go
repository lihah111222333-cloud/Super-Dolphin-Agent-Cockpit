//go:build windows && !lsp_integration

package manager_test

import (
	"net/url"
	"path/filepath"
	"strings"
)

// goWorkFakeFileURI 生成 Windows fake LSP 使用的标准 file URI。
func goWorkFakeFileURI(path string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}
