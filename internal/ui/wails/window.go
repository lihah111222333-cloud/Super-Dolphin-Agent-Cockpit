package wails

import (
	"fmt"
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

func bootstrapWindowHTML(title string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<script src="/wails/runtime.js"></script>
<script src="/wails/transport.js"></script>
<style>
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0f172a;color:#e2e8f0;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
main{max-width:36rem;padding:2.5rem}
h1{margin:0 0 1rem;font-size:2rem}
p{margin:0;line-height:1.6}
</style>
</head>
<body>
<main>
<h1>%s</h1>
<p>Wails binding layer is active. Frontend assets are not bundled in this milestone.</p>
</main>
</body>
</html>`, title, title)
}
