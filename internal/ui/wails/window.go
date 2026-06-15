package wails

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// CreateMainWindow 创建桌面主窗口。
func CreateMainWindow(app *application.App, title string, debug bool) {
	if app == nil {
		return
	}
	createWindow(app, title, debug, "main", "", "")
}

func newWindowOptions(title string, debug bool, name, uiBootstrap, cwd string) application.WebviewWindowOptions {
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
	if name = strings.TrimSpace(name); name != "" {
		options.Name = name
	}
	// Backend propagates bootstrap values into the window URL; frontend
	// consumers read ao_ui_bootstrap/ao_window_cwd from the query string.
	options.URL = windowURL(uiBootstrap, cwd)
	return options
}

func createWindow(app *application.App, title string, debug bool, name, uiBootstrap, cwd string, bindings ...*App) *application.WebviewWindow {
	if app == nil {
		return nil
	}
	window := app.Window.NewWithOptions(newWindowOptions(title, debug, name, uiBootstrap, cwd))
	bindFileDrop(window, app, firstAppBinding(bindings))
	return window
}

func firstAppBinding(bindings []*App) *App {
	if len(bindings) == 0 {
		return nil
	}
	return bindings[0]
}

// bindFileDrop 绑定文件drop。
func bindFileDrop(window *application.WebviewWindow, app *application.App, binding *App) {
	if window == nil || app == nil {
		return
	}
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		if event == nil {
			return
		}
		ctx := event.Context()
		files := ctx.DroppedFiles()
		details := ctx.DropTargetDetails()
		if binding != nil {
			binding.recordDroppedFiles(files, details)
		}
		payload, ok := buildFilesDroppedPayload(files, details)
		if !ok {
			return
		}
		emitFilesDroppedEvent(app, binding, payload)
	})
}

func emitFilesDroppedEvent(app *application.App, binding *App, payload map[string]any) {
	if binding != nil {
		binding.emitRuntimeEvent("files-dropped", payload)
		return
	}
	if app == nil || app.Event == nil {
		return
	}
	app.Event.Emit("files-dropped", payload)
}

func buildFilesDroppedPayload(files []string, details *application.DropTargetDetails) (map[string]any, bool) {
	if len(files) == 0 {
		return nil, false
	}
	payload := map[string]any{
		"files": append([]string(nil), files...),
	}
	if detailPayload := fileDropDetails(details); detailPayload != nil {
		payload["details"] = detailPayload
	}
	return payload, true
}

func fileDropDetails(details *application.DropTargetDetails) map[string]any {
	if details == nil {
		return nil
	}
	return map[string]any{
		"id":         details.ElementID,
		"classList":  append([]string(nil), details.ClassList...),
		"attributes": details.Attributes,
		"x":          details.X,
		"y":          details.Y,
	}
}

// windowURL 处理windowURL。
func windowURL(uiBootstrap, cwd string) string {
	base := strings.TrimSpace(os.Getenv("FRONTEND_DEVSERVER_URL"))
	if base == "" {
		base = "/"
	}
	values := url.Values{}
	if value := strings.TrimSpace(uiBootstrap); value != "" {
		values.Set("ao_ui_bootstrap", value)
	}
	if value := strings.TrimSpace(cwd); value != "" {
		values.Set("ao_window_cwd", value)
	}
	if len(values) == 0 {
		return base
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	query := parsed.Query()
	for key, items := range values {
		for _, item := range items {
			query.Set(key, item)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func buildWindowName(group string, n int) string {
	prefix := strings.TrimSpace(group)
	if prefix == "" {
		prefix = "window"
	}
	if n > 0 {
		return fmt.Sprintf("%s-%d-%d", prefix, n, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
