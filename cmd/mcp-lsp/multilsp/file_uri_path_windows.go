//go:build windows

package multilsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

// platformCanonicalAbsolutePath 统一 Windows 本地盘符路径的进程内身份。
// file URI 按 vscode-uri 使用小写盘符，但解码后的磁盘路径统一回到大写盘符，
// 避免同一 workspace 因 C:\\ 与 c:\\ 生成两个 client、把 didOpen 和语义请求拆开。
func platformCanonicalAbsolutePath(absPath string) string {
	volume := filepath.VolumeName(absPath)
	if len(volume) != 2 || volume[1] != ':' || len(absPath) < 2 {
		return absPath
	}
	return strings.ToUpper(absPath[:1]) + absPath[1:]
}

// platformFileURIPath 把 Windows 盘符路径转换为 file URI 的绝对 path 部分。
func platformFileURIPath(absPath string) string {
	uriPath := filepath.ToSlash(absPath)
	if filepath.VolumeName(absPath) != "" && !strings.HasPrefix(uriPath, "/") {
		return "/" + uriPath
	}
	return uriPath
}

// platformFileURIFromPath 使用 VS Code/vscode-uri 在 Windows 上采用的规范形式编码盘符 URI：
// 驱动器字母小写且冒号写成 %3A。Prisma 等会在加载 schema 时按此形式规范化 URI，
// 请求与 schema tuple 必须逐字一致；UNC、无盘符路径仍保持 net/url 的既有编码语义。
func platformFileURIFromPath(absPath string) string {
	uriPath := platformFileURIPath(absPath)
	volume := filepath.VolumeName(absPath)
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
