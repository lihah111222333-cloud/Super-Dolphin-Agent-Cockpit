package wails

import (
	"embed"
	"io/fs"
	"net/http"

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
func AssetHandlerFrom(injected FrontendFS) http.Handler {
	return application.BundledAssetFileServer(resolveFS(injected))
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
