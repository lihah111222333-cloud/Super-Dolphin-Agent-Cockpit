package wails

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed frontend
var frontendAssets embed.FS

func AssetHandler() http.Handler {
	return application.BundledAssetFileServer(frontendFS())
}

func frontendFS() fs.FS {
	sub, err := fs.Sub(frontendAssets, "frontend")
	if err == nil {
		return sub
	}
	return frontendAssets
}
