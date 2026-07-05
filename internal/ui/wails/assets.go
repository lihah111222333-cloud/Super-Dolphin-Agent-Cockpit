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
	"text/template"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	viteDevURLEnv           = "VITE_DEV_URL"
	frontendDevServerURLEnv = "FRONTEND_DEVSERVER_URL"
)

// FrontendFS 承载入口程序通过 Fx 注入的前端资源文件系统。
type FrontendFS struct {
	FS fs.FS
}

// placeholderAssets 内置最小前端资源，保证未构建前端时 Go 包仍可编译。
//
//go:embed frontend
var placeholderAssets embed.FS

// AssetHandlerFrom 构造生产模式 Wails 前端资源处理器。
// 生产模式拒绝 VITE_DEV_URL，避免发布环境被环境变量切到 dev proxy。
func AssetHandlerFrom(injected FrontendFS) http.Handler {
	handler, err := assetHandlerFromForMode(injected, false)
	return mustAssetHandler(handler, err)
}

// AssetHandlerFromForMode 构造 Wails 前端资源处理器。
// 只有调试模式允许 VITE_DEV_URL，并且目标必须是 loopback dev server。
func AssetHandlerFromForMode(injected FrontendFS, debug bool) http.Handler {
	handler, err := assetHandlerFromForMode(injected, debug)
	return mustAssetHandler(handler, err)
}

// assetHandlerFromForMode 返回 Wails 前端资源处理器或启动配置错误。
func assetHandlerFromForMode(injected FrontendFS, debug bool) (http.Handler, error) {
	if devURL := strings.TrimSpace(os.Getenv(viteDevURLEnv)); devURL != "" {
		target, err := parseFrontendDevServerURL(devURL, viteDevURLEnv)
		if err == nil && !debug {
			err = fmt.Errorf("production mode rejects dev URL")
		}
		if err != nil {
			return nil, assetConfigError("invalid " + viteDevURLEnv + ": " + err.Error())
		}
		return viteDevProxy(target), nil
	}
	frontend, err := resolveFS(injected, debug)
	if err != nil {
		return nil, err
	}
	return application.BundledAssetFileServer(frontend), nil
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

// parseFrontendDevServerURL 校验桌面前端 dev server URL 只指向本机 HTTP(S) 服务。
// Wails 会直接或通过反向代理加载该地址，非 loopback 或缺少端口必须 fail-fast。
func parseFrontendDevServerURL(rawURL string, envName string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if target == nil || target.Host == "" || strings.TrimSpace(target.Hostname()) == "" || target.Port() == "" {
		return nil, fmt.Errorf("%s must include host and port, got %q", envName, rawURL)
	}
	if target.User != nil {
		return nil, fmt.Errorf("%s must not include user info, got %q", envName, rawURL)
	}
	switch strings.ToLower(strings.TrimSpace(target.Scheme)) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("%s must use http/https scheme, got %q", envName, target.Scheme)
	}
	if !isLoopbackHost(target.Hostname()) {
		return nil, fmt.Errorf("%s must use loopback host, got %q", envName, target.Hostname())
	}
	return target, nil
}

// resolveFS 选择实际提供给 Wails 的前端资源文件系统。
// 生产模式必须使用包含 index.html 的注入资源；placeholder 仅允许 debug/test 显式启用。
func resolveFS(injected FrontendFS, debug bool) (fs.FS, error) {
	if injected.FS != nil {
		if err := requireFrontendIndex(injected.FS); err != nil {
			return nil, assetConfigError("invalid production frontend assets: " + err.Error())
		}
		return injected.FS, nil
	}
	if !debug {
		return nil, assetConfigError("production frontend assets are not configured")
	}
	sub, err := fs.Sub(placeholderAssets, "frontend")
	if err == nil {
		return sub, nil
	}
	return placeholderAssets, nil
}

type assetConfigError string

// Error 返回前端资源配置错误文本。
func (e assetConfigError) Error() string {
	return string(e)
}

// mustAssetHandler preserves the legacy fail-fast helper contract.
// Startup wiring uses assetHandlerFromForMode so asset errors can be returned.
func mustAssetHandler(handler http.Handler, err error) http.Handler {
	if err == nil {
		return handler
	}
	template.Must(template.New("frontend-assets"), err)
	return nil
}

// requireFrontendIndex 确认资源文件系统可以提供 Wails 启动所需的 index.html。
func requireFrontendIndex(frontend fs.FS) error {
	info, err := fs.Stat(frontend, "index.html")
	if err != nil {
		return fmt.Errorf("missing index.html")
	}
	if info.IsDir() {
		return fmt.Errorf("index.html must be a file")
	}
	return nil
}
