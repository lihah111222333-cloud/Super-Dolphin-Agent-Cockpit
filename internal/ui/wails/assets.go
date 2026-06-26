package wails

import (
	"embed"
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
		return viteDevProxy(devURL)
	}
	return application.BundledAssetFileServer(resolveFS(injected))
}

// viteDevProxy 创建指向 Vite dev server 的反向代理。
func viteDevProxy(rawURL string) http.Handler {
	target, err := url.Parse(rawURL)
	if err != nil {
		panic("invalid VITE_DEV_URL: " + err.Error())
	}
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
