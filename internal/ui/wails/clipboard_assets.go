package wails

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// clipboardPathPrefix 是 WebView 加载临时剪贴板图片的 URL 前缀。
const clipboardPathPrefix = "/clipboard/"

// localImagePathPrefix 是 WebView 加载本地图片预览的 URL 前缀。
const localImagePathPrefix = "/local-image"

// localImageAssetTTL 限制本地图片预览 token 的存活时间。
const localImageAssetTTL = 15 * time.Minute

// archguard:ignore global_vars -- 本地图片预览 token 是 Wails HTTP capability 状态，需跨窗口与 HTTP handler 共享。
var defaultLocalImageAssets = newLocalImageAssetRegistry(time.Now)

type localImageAssetRecord struct {
	Path        string
	ContentType string
	ExpiresAt   time.Time
	Source      string
}

type localImageAssetRegistry struct {
	mu     sync.Mutex
	now    func() time.Time
	assets map[string]localImageAssetRecord
}

// withClipboardAssets 包装前端资源 handler，额外提供剪贴板和本地图片预览路由。
func withClipboardAssets(inner http.Handler) http.Handler {
	return withClipboardAssetsRegistry(inner, defaultLocalImageAssets)
}

// withClipboardAssetsRegistry 使用给定 registry 包装前端资源 handler，便于测试隔离 token 状态。
func withClipboardAssetsRegistry(inner http.Handler, registry *localImageAssetRegistry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, clipboardPathPrefix) {
			serveClipboardAsset(w, r)
			return
		}
		if r.URL.Path == localImagePathPrefix {
			serveLocalImageAsset(w, r, registry)
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

// serveLocalImageAsset 为桌面 WebView 提供本地图片预览，只接受后端登记过的短期 token。
func serveLocalImageAsset(w http.ResponseWriter, r *http.Request, registry *localImageAssetRegistry) {
	record, ok := registry.lookup(strings.TrimSpace(r.URL.Query().Get("id")))
	if !ok {
		http.NotFound(w, r)
		return
	}
	full := record.Path
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if !localImageFileHasSupportedContent(full, record.ContentType) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", record.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, full)
}

// newLocalImageAssetRegistry 创建本地图片 token 注册表。
func newLocalImageAssetRegistry(now func() time.Time) *localImageAssetRegistry {
	if now == nil {
		now = time.Now
	}
	return &localImageAssetRegistry{
		now:    now,
		assets: make(map[string]localImageAssetRecord),
	}
}

// registerLocalImageAsset 登记本地图片路径并返回 WebView 可用的后端预览 URL。
func registerLocalImageAsset(path string, source string) (string, error) {
	return defaultLocalImageAssets.register(path, source)
}

// register 校验图片路径并登记短期 token，失败时不泄露文件内容。
func (r *localImageAssetRegistry) register(path string, source string) (string, error) {
	full := strings.TrimSpace(path)
	contentType, ok := localImageContentType(full)
	if !ok || !isValidLocalImagePath(full) {
		return "", fmt.Errorf("local image asset: invalid image path %q", path)
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("local image asset: stat %q: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("local image asset: %q is a directory", path)
	}
	if !localImageFileHasSupportedContent(full, contentType) {
		return "", fmt.Errorf("local image asset: unsupported image content %q", path)
	}
	id, err := newLocalImageAssetID()
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	r.assets[id] = localImageAssetRecord{
		Path:        full,
		ContentType: contentType,
		ExpiresAt:   now.Add(localImageAssetTTL),
		Source:      strings.TrimSpace(source),
	}
	values := url.Values{}
	values.Set("id", id)
	return localImagePathPrefix + "?" + values.Encode(), nil
}

// lookup 读取 token 对应图片记录，过期或缺失时拒绝访问。
func (r *localImageAssetRegistry) lookup(id string) (localImageAssetRecord, bool) {
	if r == nil || strings.TrimSpace(id) == "" {
		return localImageAssetRecord{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	record, ok := r.assets[id]
	if !ok || now.After(record.ExpiresAt) {
		delete(r.assets, id)
		return localImageAssetRecord{}, false
	}
	return record, true
}

// pruneLocked 在持锁状态下清理过期的本地图片 token。
func (r *localImageAssetRegistry) pruneLocked(now time.Time) {
	for id, record := range r.assets {
		if now.After(record.ExpiresAt) {
			delete(r.assets, id)
		}
	}
}

// newLocalImageAssetID 生成不可预测的本地图片 token。
func newLocalImageAssetID() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("local image asset: create token: %w", err)
	}
	return "img_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
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
