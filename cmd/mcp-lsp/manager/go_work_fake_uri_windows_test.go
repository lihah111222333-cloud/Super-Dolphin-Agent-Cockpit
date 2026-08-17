//go:build windows && !lsp_integration

package manager_test

import (
	"net/url"
	"path/filepath"
	"strings"
)

// goWorkFakeFileURI 生成 Windows fake LSP 使用的 vscode-uri 规范盘符 URI。
// 这是 Windows 专用测试合同：盘符小写且冒号编码为 %3A，与生产初始化 rootURI 逐字一致。
func goWorkFakeFileURI(path string) string {
	uriPath := filepath.ToSlash(path)
	volume := filepath.VolumeName(path)
	if volume != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	if len(volume) != 2 || volume[1] != ':' || !strings.HasPrefix(uriPath, "/"+volume) {
		return (&url.URL{Scheme: "file", Path: uriPath}).String()
	}
	drive := strings.ToLower(volume[:1])
	uriPath = "/" + drive + ":" + uriPath[len(volume)+1:]
	fileURL := &url.URL{Scheme: "file", Path: uriPath}
	escapedPath := fileURL.EscapedPath()
	fileURL.RawPath = "/" + drive + "%3A" + escapedPath[3:]
	return fileURL.String()
}
