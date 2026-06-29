package wails

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

const sharedFilePreviewPathPrefix = "/shared-file-preview"
const sharedFilePreviewMaxBytes int64 = 50 * 1024 * 1024
const sharedFilePreviewTTL = 15 * time.Minute

// archguard:ignore global_vars -- shared file preview token 是 Wails HTTP capability 状态，需要被 RPC 和 HTTP handler 共同访问。
var defaultSharedFilePreviewAssets = newSharedFilePreviewRegistry(time.Now)

// openSharedFileParams 是 ui/sharedFile/open 的请求参数。
// Path 使用 shared file 相对路径 wire 格式，不能直接接受任意本地绝对路径。
type openSharedFileParams struct {
	Path    string `json:"path"`
	Preview bool   `json:"preview,omitempty"`
	clientMetaParams
}

// openSharedFileResult 是 shared file 打开请求的返回载荷。
// Path 返回清理后的相对路径，避免把后端沙箱绝对路径暴露给前端。
type openSharedFileResult struct {
	Opened bool   `json:"opened"`
	Path   string `json:"path"`
}

// sharedFilePreviewResult 是 shared file 媒体预览 token 的返回载荷。
// URL 指向后端短期 token endpoint，不包含项目根或本机绝对路径。
type sharedFilePreviewResult struct {
	URL         string `json:"url"`
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

type sharedFilePreviewRecord struct {
	ProjectRoot string
	Path        string
	ContentType string
	SizeBytes   int64
	ExpiresAt   time.Time
}

type sharedFilePreviewRegistry struct {
	mu     sync.Mutex
	now    func() time.Time
	assets map[string]sharedFilePreviewRecord
}

// handleOpenSharedFile 校验 shared file 路径并交给系统默认程序打开。
// 任何越界、非普通文件或系统打开失败都会返回错误，不做静默成功。
func handleOpenSharedFile(
	ctx context.Context,
	app *App,
	cfg *config.Config,
	p openSharedFileParams,
) (any, error) {
	_ = ctx
	if app == nil {
		return openSharedFileResult{}, errors.New("shared file open: app is required")
	}
	if cfg == nil {
		return openSharedFileResult{}, errors.New("shared file open: config is required")
	}
	if p.Preview {
		return handlePreviewSharedFile(cfg, p)
	}
	abs, cleaned, err := resolveSharedFileOpenPathWithCleanPath(cfg.ProjectRoot, p.Path)
	if err != nil {
		return openSharedFileResult{}, err
	}
	if err := openSharedFileWithSystemDefault(abs); err != nil {
		return openSharedFileResult{}, fmt.Errorf("shared file open: open %q: %w", cleaned, err)
	}
	return openSharedFileResult{Opened: true, Path: cleaned}, nil
}

// handlePreviewSharedFile 为 shared file 生成短期媒体预览 URL。
// 只返回 token URL 和清理后的相对路径，真实文件读取仍由 HTTP endpoint 再校验一次。
func handlePreviewSharedFile(cfg *config.Config, p openSharedFileParams) (sharedFilePreviewResult, error) {
	if cfg == nil {
		return sharedFilePreviewResult{}, errors.New("shared file preview: config is required")
	}
	_, result, err := defaultSharedFilePreviewAssets.register(cfg.ProjectRoot, p.Path)
	if err != nil {
		return sharedFilePreviewResult{}, err
	}
	return result, nil
}

// resolveSharedFileOpenPath 返回 shared file 的绝对路径，供测试和 handler 复用。
// 安全校验集中在 WithCleanPath 版本，避免测试绕过 shared file 路径策略。
func resolveSharedFileOpenPath(projectRoot, rawPath string) (string, error) {
	abs, _, err := resolveSharedFileOpenPathWithCleanPath(projectRoot, rawPath)
	return abs, err
}

// resolveSharedFileOpenPathWithCleanPath 解析 shared file 路径并返回清理后的相对路径。
// 解析过程同时校验项目根、shared path 规则、真实路径和普通文件类型。
func resolveSharedFileOpenPathWithCleanPath(projectRoot, rawPath string) (string, string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return "", "", errors.New("shared file open: project root is required")
	}
	if looksLikeWindowsDrivePath(rawPath) {
		return "", "", errors.New("shared file open: invalid path: windows drive paths are not allowed")
	}
	cleaned, err := sharedfilepath.ValidateReadPath(rawPath)
	if err != nil {
		return "", "", fmt.Errorf("shared file open: invalid path: %w", err)
	}
	fsCfg := sharedfilefs.Config{CWD: root}
	abs, err := fsCfg.ResolveAbs(cleaned)
	if err != nil {
		return "", "", fmt.Errorf("shared file open: resolve %q: %w", cleaned, err)
	}
	info, err := lstatSharedFileOpenPath(fsCfg.SandboxRoot(), cleaned, abs)
	if err != nil {
		return "", "", fmt.Errorf("shared file open: stat %q: %w", cleaned, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("shared file open: %q is not a regular file", cleaned)
	}
	return abs, cleaned, nil
}

func looksLikeWindowsDrivePath(path string) bool {
	trimmed := strings.TrimSpace(path)
	return len(trimmed) >= 2 && trimmed[1] == ':' &&
		((trimmed[0] >= 'A' && trimmed[0] <= 'Z') || (trimmed[0] >= 'a' && trimmed[0] <= 'z'))
}

// lstatSharedFileOpenPath 逐级 lstat shared file 路径，拒绝根目录或中间路径 symlink。
func lstatSharedFileOpenPath(sandboxRoot, cleaned, abs string) (os.FileInfo, error) {
	current := filepath.Clean(sandboxRoot)
	rootInfo, err := os.Lstat(current)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("sandbox root is a symlink")
	}
	for _, part := range strings.Split(filepath.FromSlash(cleaned), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%q is a symlink", cleaned)
		}
		if filepath.Clean(current) == filepath.Clean(abs) {
			return info, nil
		}
	}
	return os.Lstat(abs)
}

// withSharedFilePreviewAssets 包装前端资源 handler，额外提供 shared file token 预览路由。
func withSharedFilePreviewAssets(inner http.Handler) http.Handler {
	return withSharedFilePreviewAssetsRegistry(inner, defaultSharedFilePreviewAssets)
}

// withSharedFilePreviewAssetsRegistry 使用指定 registry，便于测试隔离 token 状态。
func withSharedFilePreviewAssetsRegistry(inner http.Handler, registry *sharedFilePreviewRegistry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == sharedFilePreviewPathPrefix {
			serveSharedFilePreviewAsset(w, r, registry)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// serveSharedFilePreviewAsset 只服务已登记 token，并在读取前重新执行 shared file 校验。
func serveSharedFilePreviewAsset(w http.ResponseWriter, r *http.Request, registry *sharedFilePreviewRegistry) {
	id, ok := sharedFilePreviewTokenFromQuery(r.URL.Query())
	if !ok {
		http.NotFound(w, r)
		return
	}
	record, ok := registry.lookup(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	abs, cleaned, err := resolveSharedFileOpenPathWithCleanPath(record.ProjectRoot, record.Path)
	if err != nil || cleaned != record.Path {
		http.NotFound(w, r)
		return
	}
	contentType, sizeBytes, err := validateSharedFilePreviewMedia(abs, cleaned)
	if err != nil || contentType != record.ContentType || sizeBytes > sharedFilePreviewMaxBytes {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", record.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=900")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, abs)
}

func sharedFilePreviewTokenFromQuery(values url.Values) (string, bool) {
	ids := values["id"]
	if len(values) != 1 || len(ids) != 1 {
		return "", false
	}
	id := strings.TrimSpace(ids[0])
	return id, id != ""
}

func newSharedFilePreviewRegistry(now func() time.Time) *sharedFilePreviewRegistry {
	if now == nil {
		now = time.Now
	}
	return &sharedFilePreviewRegistry{
		now:    now,
		assets: make(map[string]sharedFilePreviewRecord),
	}
}

// register 校验 shared file 媒体路径并登记短期 token。
func (r *sharedFilePreviewRegistry) register(projectRoot, rawPath string) (string, sharedFilePreviewResult, error) {
	if r == nil {
		return "", sharedFilePreviewResult{}, errors.New("shared file preview: registry is required")
	}
	abs, cleaned, err := resolveSharedFileOpenPathWithCleanPath(projectRoot, rawPath)
	if err != nil {
		return "", sharedFilePreviewResult{}, err
	}
	contentType, sizeBytes, err := validateSharedFilePreviewMedia(abs, cleaned)
	if err != nil {
		return "", sharedFilePreviewResult{}, err
	}
	id, err := newSharedFilePreviewID()
	if err != nil {
		return "", sharedFilePreviewResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	r.assets[id] = sharedFilePreviewRecord{
		ProjectRoot: strings.TrimSpace(projectRoot),
		Path:        cleaned,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		ExpiresAt:   now.Add(sharedFilePreviewTTL),
	}
	previewURL := sharedFilePreviewURL(id)
	return previewURL, sharedFilePreviewResult{
		URL:         previewURL,
		Path:        cleaned,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
	}, nil
}

// lookup 读取 token 记录，缺失或过期时拒绝访问。
func (r *sharedFilePreviewRegistry) lookup(id string) (sharedFilePreviewRecord, bool) {
	if r == nil || strings.TrimSpace(id) == "" {
		return sharedFilePreviewRecord{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	record, ok := r.assets[id]
	if !ok || now.After(record.ExpiresAt) {
		delete(r.assets, id)
		return sharedFilePreviewRecord{}, false
	}
	return record, true
}

func (r *sharedFilePreviewRegistry) pruneLocked(now time.Time) {
	for id, record := range r.assets {
		if now.After(record.ExpiresAt) {
			delete(r.assets, id)
		}
	}
}

func sharedFilePreviewURL(id string) string {
	values := url.Values{}
	values.Set("id", id)
	return (&url.URL{
		Scheme:   "http",
		Host:     resolveHTTPAssetAddr(),
		Path:     sharedFilePreviewPathPrefix,
		RawQuery: values.Encode(),
	}).String()
}

func newSharedFilePreviewID() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("shared file preview: create token: %w", err)
	}
	return "sf_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func validateSharedFilePreviewMedia(abs, cleaned string) (string, int64, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return "", 0, fmt.Errorf("shared file preview: stat %q: %w", cleaned, err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("shared file preview: %q is not a regular file", cleaned)
	}
	if info.Size() > sharedFilePreviewMaxBytes {
		return "", 0, fmt.Errorf("shared file preview: %q exceeds preview size limit", cleaned)
	}
	contentType, err := sniffSharedFilePreviewContentType(abs, cleaned)
	if err != nil {
		return "", 0, err
	}
	return contentType, info.Size(), nil
}

func sniffSharedFilePreviewContentType(abs, cleaned string) (string, error) {
	expected, ok := sharedFilePreviewContentTypeFromExt(cleaned)
	if !ok {
		return "", fmt.Errorf("shared file preview: unsupported media type %q", cleaned)
	}
	actual, err := sniffSharedFilePreviewHeader(abs, expected)
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", fmt.Errorf("shared file preview: MIME/header mismatch for %q", cleaned)
	}
	return expected, nil
}

func sharedFilePreviewContentTypeFromExt(path string) (string, bool) {
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
	case ".mp4":
		return "video/mp4", true
	case ".webm":
		return "video/webm", true
	case ".ogg":
		return "video/ogg", true
	case ".mov":
		return "video/quicktime", true
	default:
		return "", false
	}
}

func sniffSharedFilePreviewHeader(abs, expected string) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("shared file preview: open for sniff: %w", err)
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("shared file preview: sniff: %w", err)
	}
	if n == 0 {
		return "", errors.New("shared file preview: empty file")
	}
	return sniffSharedFilePreviewHeaderBytes(buf[:n], expected)
}

func sniffSharedFilePreviewHeaderBytes(buf []byte, expected string) (string, error) {
	if contentType := sniffSharedImageHeader(buf); contentType != "" {
		return contentType, nil
	}
	if contentType := sniffSharedVideoHeader(buf, expected); contentType != "" {
		return contentType, nil
	}
	return "", errors.New("shared file preview: unsupported media header")
}

func sniffSharedImageHeader(buf []byte) string {
	switch {
	case hasSharedPreviewPNGHeader(buf):
		return "image/png"
	case hasSharedPreviewJPEGHeader(buf):
		return "image/jpeg"
	case hasSharedPreviewGIFHeader(buf):
		return "image/gif"
	case hasSharedPreviewWEBPHeader(buf):
		return "image/webp"
	case hasSharedPreviewBMPHeader(buf):
		return "image/bmp"
	default:
		return ""
	}
}

func hasSharedPreviewPNGHeader(buf []byte) bool {
	return bytes.HasPrefix(buf, []byte("\x89PNG\r\n\x1a\n"))
}

func hasSharedPreviewJPEGHeader(buf []byte) bool {
	return len(buf) >= 3 && buf[0] == 0xFF && buf[1] == 0xD8 && buf[2] == 0xFF
}

func hasSharedPreviewGIFHeader(buf []byte) bool {
	return bytes.HasPrefix(buf, []byte("GIF87a")) || bytes.HasPrefix(buf, []byte("GIF89a"))
}

func hasSharedPreviewWEBPHeader(buf []byte) bool {
	return len(buf) >= 12 && bytes.HasPrefix(buf, []byte("RIFF")) && string(buf[8:12]) == "WEBP"
}

func hasSharedPreviewBMPHeader(buf []byte) bool {
	return bytes.HasPrefix(buf, []byte("BM"))
}

func sniffSharedVideoHeader(buf []byte, expected string) string {
	switch {
	case len(buf) >= 12 && string(buf[4:8]) == "ftyp" && expected == "video/quicktime":
		return "video/quicktime"
	case len(buf) >= 12 && string(buf[4:8]) == "ftyp":
		return "video/mp4"
	case bytes.HasPrefix(buf, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return "video/webm"
	case bytes.HasPrefix(buf, []byte("OggS")):
		return "video/ogg"
	default:
		return ""
	}
}

// openSharedFileWithSystemDefault 使用当前系统默认程序打开文件。
func openSharedFileWithSystemDefault(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("shared file open: resolved path is required")
	}
	switch runtime.GOOS {
	case "darwin":
		if openSystemPath("open", path) {
			return nil
		}
		return errors.New("open command failed")
	case "linux":
		if openSystemPath("xdg-open", path) {
			return nil
		}
		return errors.New("xdg-open command failed")
	case "windows":
		binary, err := exec.LookPath("rundll32")
		if err != nil {
			return err
		}
		if err := exec.Command(binary, "url.dll,FileProtocolHandler", path).Run(); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
}
