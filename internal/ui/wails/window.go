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
// 主窗口固定使用 main 分组，不携带一次性 bootstrap，子窗口入口由 openNewWindow 负责。
func CreateMainWindow(app *application.App, title string, debug bool) {
	if app == nil {
		return
	}
	createWindow(app, title, debug, "main", "", "")
}

// newWindowOptions 构造桌面窗口默认参数，并把 bootstrap/cwd 编入 URL。
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
	// 后端把 bootstrap 值放进窗口 URL，前端从查询串读取 ao_ui_bootstrap/ao_window_cwd。
	options.URL = windowURLForMode(debug, uiBootstrap, cwd)
	return options
}

// createWindow 创建 Wails WebviewWindow 并绑定文件拖拽事件。
// binding 为空时仍能创建窗口，只是拖拽文件不会登记到后端读取白名单。
func createWindow(app *application.App, title string, debug bool, name, uiBootstrap, cwd string, bindings ...*App) *application.WebviewWindow {
	if app == nil {
		return nil
	}
	window := app.Window.NewWithOptions(newWindowOptions(title, debug, name, uiBootstrap, cwd))
	bindFileDrop(window, app, firstAppBinding(bindings))
	return window
}

// firstAppBinding 返回可选绑定中的第一个 App。
// 这是 createWindow 的可选依赖入口，测试可不传绑定以跳过拖拽白名单登记。
func firstAppBinding(bindings []*App) *App {
	if len(bindings) == 0 {
		return nil
	}
	return bindings[0]
}

// bindFileDrop 绑定原生文件拖拽事件，并把文件路径登记到读取白名单。
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

// emitFilesDroppedEvent 发送文件拖拽事件，优先走绑定对象的 runtime 推送。
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

// buildFilesDroppedPayload 构造前端文件拖拽事件载荷。
func buildFilesDroppedPayload(files []string, details *application.DropTargetDetails) (map[string]any, bool) {
	if len(files) == 0 {
		return nil, false
	}
	payload := map[string]any{
		"files": append([]string(nil), files...),
	}
	if previews := droppedImagePreviews(files); len(previews) > 0 {
		payload["imagePreviews"] = previews
	}
	if detailPayload := fileDropDetails(details); detailPayload != nil {
		payload["details"] = detailPayload
	}
	return payload, true
}

// droppedImagePreviews 为拖入的本地图片登记短期预览 token。
func droppedImagePreviews(files []string) map[string]string {
	previews := make(map[string]string)
	for _, raw := range files {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		previewURL, err := registerLocalImageAsset(path, "dropped-file")
		if err == nil {
			previews[path] = previewURL
		}
	}
	return previews
}

// fileDropDetails 提取 Wails 提供的拖拽目标元素信息。
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

// windowURL 以生产模式生成窗口入口 URL，并附加启动快照和工作目录参数。
func windowURL(uiBootstrap, cwd string) string {
	return windowURLForMode(false, uiBootstrap, cwd)
}

// windowURLForMode 生成窗口入口 URL，并在开发模式下允许 loopback dev server。
func windowURLForMode(debug bool, uiBootstrap, cwd string) string {
	base := strings.TrimSpace(os.Getenv(frontendDevServerURLEnv))
	if base == "" {
		base = "/"
	} else {
		target, err := parseFrontendDevServerURL(base, frontendDevServerURLEnv)
		if err != nil {
			panic("invalid " + frontendDevServerURLEnv + ": " + err.Error())
		}
		if !debug {
			panic("invalid " + frontendDevServerURLEnv + ": production mode rejects dev URL")
		}
		base = target.String()
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

// buildWindowName 生成同组窗口名，使用时间戳避免重复。
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
