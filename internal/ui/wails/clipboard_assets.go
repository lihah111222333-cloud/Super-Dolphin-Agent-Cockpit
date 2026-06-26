package wails

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// clipboardPathPrefix 是 WebView 加载临时剪贴板图片的 URL 前缀。
const clipboardPathPrefix = "/clipboard/"

// localImagePathPrefix 是 WebView 加载本地图片预览的 URL 前缀。
const localImagePathPrefix = "/local-image"

// withClipboardAssets 包装前端资源 handler，额外提供剪贴板和本地图片预览路由。
func withClipboardAssets(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, clipboardPathPrefix) {
			serveClipboardAsset(w, r)
			return
		}
		if r.URL.Path == localImagePathPrefix {
			serveLocalImageAsset(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// serveClipboardAsset 只按安全 basename 读取 os.TempDir 内的剪贴板 PNG。
// 这里会再次确认路径仍在 temp 目录下，避免 symlink 或路径拼接逃逸。
func serveClipboardAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, clipboardPathPrefix)
	if !isValidClipboardAssetName(name) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(os.TempDir(), name)
	if !isUnderTempDir(full) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, full)
}

// serveLocalImageAsset 为桌面 WebView 提供本地图片预览，避免前端直接加载 file://。
// 只允许绝对路径和真实图片内容，失败时返回 404，避免把本地任意文件暴露成资源。
func serveLocalImageAsset(w http.ResponseWriter, r *http.Request) {
	full := strings.TrimSpace(r.URL.Query().Get("path"))
	contentType, ok := localImageContentType(full)
	if !ok || !isValidLocalImagePath(full) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if !localImageFileHasSupportedContent(full, contentType) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, full)
}

// isValidClipboardAssetName 判断 URL 中的剪贴板资源名是否符合临时 PNG 命名规则。
func isValidClipboardAssetName(name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	if name == "." || name == ".." {
		return false
	}
	if !strings.HasPrefix(name, "clipboard-") && !strings.HasPrefix(name, "codex-clipboard-") {
		return false
	}
	if !strings.EqualFold(filepath.Ext(name), ".png") {
		return false
	}
	return true
}

// isValidLocalImagePath 判断本地图片路径是否能进入预览服务。
// 这里拒绝相对路径、UNC 网络路径和空字节，避免渲染层扩大文件读取范围。
func isValidLocalImagePath(path string) bool {
	if path == "" || strings.ContainsRune(path, '\x00') {
		return false
	}
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return false
	}
	return filepath.IsAbs(path)
}

// localImageContentType 根据扩展名给本地图片预览设置固定 MIME。
// SVG 不在这里放行，避免把可脚本化文本资源作为同源页面内容提供。
func localImageContentType(path string) (string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	default:
		return "", false
	}
}

// localImageFileHasSupportedContent 读取文件头确认内容确实是浏览器图片。
// 仅靠扩展名会让伪装文件被同源 fetch 读到，所以这里用 Go 的嗅探结果再拦一层。
func localImageFileHasSupportedContent(path string, contentType string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	if n == 0 {
		return false
	}
	return http.DetectContentType(buf[:n]) == contentType
}

// isUnderTempDir 判断候选文件解析真实路径后是否仍位于 os.TempDir 内。
// 文件不存在时回退到词法包含检查，便于测试覆盖缺失文件场景。
func isUnderTempDir(full string) bool {
	cleanFull := filepath.Clean(full)
	tempDir := filepath.Clean(os.TempDir())

	resolvedFull, err := filepath.EvalSymlinks(cleanFull)
	if err == nil {
		cleanFull = resolvedFull
	}
	resolvedTemp, err := filepath.EvalSymlinks(tempDir)
	if err == nil {
		tempDir = resolvedTemp
	}

	rel, err := filepath.Rel(tempDir, cleanFull)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}
