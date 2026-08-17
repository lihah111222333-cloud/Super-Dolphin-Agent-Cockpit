//go:build !windows && !lsp_integration

package manager_test

import (
	"net/url"
	"path/filepath"
)

// goWorkFakeFileURI 保持 POSIX fake LSP 原有的 file URI 语义。
func goWorkFakeFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
