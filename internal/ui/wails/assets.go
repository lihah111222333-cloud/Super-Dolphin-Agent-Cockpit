package wails

import (
	"embed"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// FrontendFS carries the fs.FS provided by the binary entrypoint
// (typically cmd/agent-terminal) via Fx dependency injection.
type FrontendFS struct {
	FS fs.FS
}

// placeholder keeps a minimal index.html so `go build ./internal/...`
// always succeeds even when no frontend has been built.
//
//go:embed frontend
var placeholderAssets embed.FS

// AssetHandlerFrom builds the Wails asset handler using the injected FS,
// falling back to the embedded placeholder when the injected FS is nil.
//
// When the VITE_DEV_URL environment variable is set (e.g. "http://localhost:5173"),
// all requests are reverse-proxied to the Vite dev server, enabling HMR
// (hot module replacement) and instant frontend updates without vite build.
// AssetHandlerFrom 从桌面 UI 桥接处理asset处理器。
func AssetHandlerFrom(injected FrontendFS) http.Handler {
	if devURL := strings.TrimSpace(os.Getenv("VITE_DEV_URL")); devURL != "" {
		return viteDevProxy(devURL)
	}
	return application.BundledAssetFileServer(resolveFS(injected))
}

// viteDevProxy returns an http.Handler that reverse-proxies all requests
// to the Vite dev server at the given URL.
func viteDevProxy(rawURL string) http.Handler {
	target, err := url.Parse(rawURL)
	if err != nil {
		pkglogger.Error("invalid VITE_DEV_URL, serving embedded frontend", "url", rawURL, "error", err)
		return application.BundledAssetFileServer(resolveFS(FrontendFS{}))
	}
	pkglogger.Info("frontend proxying to vite dev server", "url", target.String())
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Rewrite the Host header so Vite accepts the request.
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}
	return proxy
}

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
