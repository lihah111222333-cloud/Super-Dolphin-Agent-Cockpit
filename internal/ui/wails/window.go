package wails

import (
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func CreateMainWindow(app *application.App, title string, debug bool) {
	if app == nil {
		return
	}
	options := application.WebviewWindowOptions{
		Name:                   "main",
		Title:                  title,
		Width:                  1440,
		Height:                 900,
		MinWidth:               800,
		MinHeight:              600,
		EnableFileDrop:         true,
		DevToolsEnabled:        debug,
		OpenInspectorOnStartup: debug,
		BackgroundColour:       application.NewRGB(15, 23, 42),
	}
	if url := strings.TrimSpace(os.Getenv("FRONTEND_DEVSERVER_URL")); url != "" {
		options.URL = url
	} else {
		options.URL = "/"
	}
	app.Window.NewWithOptions(options)
}
