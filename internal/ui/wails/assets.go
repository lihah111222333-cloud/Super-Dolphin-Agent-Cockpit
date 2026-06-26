package wails

import (
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// FrontendFS 承载入口程序通过 Fx 注入的前端资源文件系统。
type FrontendFS struct {
	FS fs.FS
}

// placeholderAssets 内置最小前端资源，保证未构建前端时 Go 包仍可编译。
//
//go:embed frontend
var placeholderAssets embed.FS

// AssetHandlerFrom 构造 Wails 前端资源处理器。
// VITE_DEV_URL 存在时会转发到 Vite dev server；否则使用注入资源或内置占位资源。
func AssetHandlerFrom(injected FrontendFS) http.Handler {
	if devURL := strings.TrimSpace(os.Getenv("VITE_DEV_URL")); devURL != "" {
		target, err := parseViteDevProxyURL(devURL)
		if err != nil {
			panic("invalid VITE_DEV_URL: " + err.Error())
		}
		return viteDevProxy(target)
	}
	return application.BundledAssetFileServer(resolveFS(injected))
}

// viteDevProxy 创建指向 Vite dev server 的反向代理。
func viteDevProxy(target *url.URL) http.Handler {
	slog.Info("frontend proxying to vite dev server", "url", target.String())
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Vite dev server 会校验 Host，这里随反向代理目标同步请求头。
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}
	return proxy
}

// parseViteDevProxyURL 校验 VITE_DEV_URL 只指向本机 HTTP(S) Vite 服务。
// 桌面壳会把该地址作为反向代理目标，非 loopback 或缺少端口必须 fail-fast，避免代理到外部主机。
func parseViteDevProxyURL(rawURL string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if target == nil || target.Host == "" || strings.TrimSpace(target.Hostname()) == "" || target.Port() == "" {
		return nil, fmt.Errorf("must include host and port, got %q", rawURL)
	}
	if target.User != nil {
		return nil, fmt.Errorf("must not include user info, got %q", rawURL)
	}
	switch strings.ToLower(strings.TrimSpace(target.Scheme)) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("must use http/https scheme, got %q", target.Scheme)
	}
	if !isLoopbackHost(target.Hostname()) {
		return nil, fmt.Errorf("must use loopback host, got %q", target.Hostname())
	}
	return target, nil
}

// resolveFS 选择实际提供给 Wails 的前端资源文件系统。
func resolveFS(injected FrontendFS) fs.FS {
	if injected.FS != nil {
		return injected.FS
	}
	sub, err := fs.Sub(placeholderAssets, "frontend")
	if err == nil {
		return sub
	}
	return placeholderAssets
}
