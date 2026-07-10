package wails

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const maxClipboardImageBytes = 10 << 20

var clipboardPNGSig = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// SaveClipboardImage 接受前端从 `ClipboardEvent`/`Blob` 读取并编码好的 base64 图像数据，
// 解码后写入临时 PNG 文件并返回其路径。允许载荷带 `data:image/...;base64,` 前缀。
func (a *App) SaveClipboardImage(base64Payload string) (string, error) {
	data, err := decodeClipboardImagePayload(base64Payload)
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "clipboard-*.png")
	if err != nil {
		return "", fmt.Errorf("clipboard image: create temp file: %w", err)
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("clipboard image: write file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("clipboard image: close file: %w", err)
	}
	return path, nil
}

// decodeClipboardImagePayload 解码剪贴板图片载荷，支持 data URL 和裸 base64。
func decodeClipboardImagePayload(payload string) ([]byte, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, errors.New("clipboard image: base64 payload is empty")
	}
	if strings.HasPrefix(payload, "data:") {
		idx := strings.Index(payload, ",")
		if idx < 0 {
			return nil, errors.New("clipboard image: data URL is missing base64 separator")
		}
		header := strings.ToLower(strings.TrimSpace(payload[:idx]))
		if !strings.HasPrefix(header, "data:image/png;") || !strings.Contains(header, ";base64") {
			return nil, errors.New("clipboard image: data URL MIME must be image/png;base64")
		}
		payload = payload[idx+1:]
	}
	payload = stripBase64Whitespace(payload)
	data, err := decodeBase64Flexible(payload)
	if err != nil {
		return nil, fmt.Errorf("clipboard image: decode base64: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("clipboard image: payload decoded to zero bytes")
	}
	if len(data) > maxClipboardImageBytes {
		return nil, fmt.Errorf("clipboard image: payload exceeds size limit of %d bytes", maxClipboardImageBytes)
	}
	if !bytes.HasPrefix(data, clipboardPNGSig) {
		return nil, errors.New("clipboard image: png header mismatch")
	}
	return data, nil
}

// stripBase64Whitespace 剔除 MIME 换行 / 空格 / 制表符等软包装字符，
// 便于 base64 解码器容错多行载荷。
func stripBase64Whitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, s)
}

// decodeBase64Flexible 兼容剪贴板来源可能省略 padding 的 base64 载荷。
// 两种编码都失败时返回解码错误，避免把坏图片写入临时目录。
func decodeBase64Flexible(payload string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(payload); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(payload)
}

// SelectProjectDir 打开原生目录选择器并返回单个项目目录。
// 这是旧 Wails 绑定入口，RPC handler 仍复用 selectProjectDir 保持行为一致。
func (a *App) SelectProjectDir() (string, error) {
	return a.selectProjectDir("")
}

// selectProjectDir 打开单目录选择器，取消选择时返回空字符串。
// 取消不是错误，调用方据空字符串判断用户没有选择项目。
func (a *App) selectProjectDir(defaultPath string) (string, error) {
	dialog, err := a.newDialog()
	if err != nil {
		return "", err
	}
	dialog = dialog.
		SetTitle("Select Project Directory").
		SetMessage("Choose a project directory").
		SetButtonText("Select").
		SetDirectory(resolveDialogDirectory(defaultPath)).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		ShowHiddenFiles(true)
	path, err := dialog.PromptForSingleSelection()
	if isDialogCancelError(err) || path == "" {
		return "", nil
	}
	return path, err
}

// SelectProjectDirs 打开原生目录选择器并允许多选。
// 返回值直接作为前端项目根候选，后续仍需经过项目范围校验。
func (a *App) SelectProjectDirs(defaultPath string) ([]string, error) {
	dialog, err := a.newDialog()
	if err != nil {
		return nil, err
	}
	dialog = dialog.
		SetTitle("Select Project Directories").
		SetMessage("Choose one or more project directories").
		SetButtonText("Select").
		SetDirectory(resolveDialogDirectory(defaultPath)).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		ShowHiddenFiles(true)
	paths, err := dialog.PromptForMultipleSelection()
	if isDialogCancelError(err) || len(paths) == 0 {
		return []string{}, nil
	}
	return paths, err
}

// SelectFiles 打开原生文件选择器并返回多个文件路径。
func (a *App) SelectFiles() ([]string, error) {
	return a.selectFiles("")
}

// selectFiles 使用默认文件选择配置打开原生文件选择器。
func (a *App) selectFiles(defaultPath string) ([]string, error) {
	return a.selectFilesWithFilters(defaultPath, nil)
}

// selectFilesWithFilters 打开带可选过滤器的文件选择器。
func (a *App) selectFilesWithFilters(defaultPath string, filters []selectFileFilter) ([]string, error) {
	dialog, err := a.newDialog()
	if err != nil {
		return nil, err
	}
	dialog = dialog.
		SetTitle("Select Files").
		SetMessage("Choose one or more files").
		SetButtonText("Select").
		SetDirectory(resolveDialogDirectory(defaultPath)).
		CanChooseDirectories(false).
		CanChooseFiles(true).
		ShowHiddenFiles(true)
	for _, filter := range normalizeSelectFileFilters(filters) {
		dialog = dialog.AddFilter(filter.DisplayName, filter.Pattern)
	}
	paths, err := dialog.PromptForMultipleSelection()
	if isDialogCancelError(err) || len(paths) == 0 {
		return []string{}, nil
	}
	return paths, err
}

// selectSingleFileWithFilters 打开单文件选择器，专供需要为单一路径签发 capability 的入口使用。
// 它不改变 selectFilesWithFilters 的多选数组契约，取消选择时返回空字符串。
func (a *App) selectSingleFileWithFilters(defaultPath string, filters []selectFileFilter) (string, error) {
	if a != nil && a.selectFileInvoker != nil {
		return a.selectFileInvoker(defaultPath, filters)
	}
	dialog, err := a.newDialog()
	if err != nil {
		return "", err
	}
	dialog = dialog.
		SetTitle("Select Datasource File").
		SetMessage("Choose a datasource file").
		SetButtonText("Select").
		SetDirectory(resolveDialogDirectory(defaultPath)).
		CanChooseDirectories(false).
		CanChooseFiles(true).
		ShowHiddenFiles(true)
	for _, filter := range normalizeSelectFileFilters(filters) {
		dialog = dialog.AddFilter(filter.DisplayName, filter.Pattern)
	}
	path, err := dialog.PromptForSingleSelection()
	if isDialogCancelError(err) || path == "" {
		return "", nil
	}
	return path, err
}

// normalizeSelectFileFilters 清理前端传入的可选文件类型过滤器。
// 空名称或空模式会被丢弃，避免原生 dialog 展示不可理解的过滤项。
func normalizeSelectFileFilters(filters []selectFileFilter) []selectFileFilter {
	normalized := make([]selectFileFilter, 0, len(filters))
	for _, filter := range filters {
		displayName := strings.TrimSpace(filter.DisplayName)
		pattern := strings.TrimSpace(filter.Pattern)
		if displayName == "" || pattern == "" {
			continue
		}
		normalized = append(normalized, selectFileFilter{
			DisplayName: displayName,
			Pattern:     pattern,
		})
	}
	return normalized
}

// saveTextFile 通过原生目录选择器导出文本文件，拒绝覆盖已有目标。
func (a *App) saveTextFile(defaultPath, defaultFilename, content string) (string, error) {
	filename := normalizeSaveFilename(defaultFilename)
	if filename == "" {
		return "", errors.New("save text file: default filename is required")
	}
	dir, err := a.promptExportDirectory(defaultPath)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	path := filepath.Join(dir, filename)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("save text file: create %q: %w", path, err)
	}
	data := []byte(content)
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = fmt.Errorf("short write: wrote %d of %d bytes", n, len(data))
	}
	if closeErr := file.Close(); closeErr != nil {
		writeErr = errors.Join(writeErr, closeErr)
	}
	if writeErr != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			writeErr = errors.Join(writeErr, fmt.Errorf("remove incomplete file: %w", removeErr))
		}
		return "", fmt.Errorf("save text file: write %q: %w", path, writeErr)
	}
	return path, nil
}

// promptExportDirectory 选择导出目录，测试可通过 saveDirectoryInvoker 替换。
func (a *App) promptExportDirectory(defaultPath string) (string, error) {
	if a != nil && a.saveDirectoryInvoker != nil {
		return a.saveDirectoryInvoker(defaultPath)
	}
	dialog, err := a.newExportDirectoryDialog(defaultPath)
	if err != nil {
		return "", err
	}
	path, err := dialog.PromptForSingleSelection()
	if isDialogCancelError(err) || path == "" {
		return "", nil
	}
	return path, err
}

// newDialog 创建附着到当前窗口的打开文件 dialog。
func (a *App) newDialog() (*application.OpenFileDialogStruct, error) {
	app, err := a.requireWailsApp()
	if err != nil {
		return nil, err
	}
	dialog := app.Dialog.OpenFile()
	if parent := dialogParentWindow(app); parent != nil {
		prepareDialogParentWindow(parent)
		dialog = dialog.AttachToWindow(parent)
	}
	return dialog, nil
}

// newExportDirectoryDialog 创建附着到当前窗口的导出目录 dialog。
func (a *App) newExportDirectoryDialog(defaultPath string) (*application.OpenFileDialogStruct, error) {
	app, err := a.requireWailsApp()
	if err != nil {
		return nil, err
	}
	dialog := app.Dialog.OpenFile().
		SetTitle("导出文件").
		SetMessage("选择保存文件夹").
		SetButtonText("导出").
		SetDirectory(resolveDialogDirectory(defaultPath)).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		ShowHiddenFiles(true)
	if parent := dialogParentWindow(app); parent != nil {
		prepareDialogParentWindow(parent)
		dialog = dialog.AttachToWindow(parent)
	}
	return dialog, nil
}

// dialogParentWindow 优先使用当前活动窗口；来自调试浏览器的 RPC 没有窗口上下文时回到主窗口。
func dialogParentWindow(app *application.App) application.Window {
	if app == nil || app.Window == nil {
		return nil
	}
	if current := app.Window.Current(); current != nil {
		return current
	}
	mainWindow, _ := app.Window.GetByName("main")
	return mainWindow
}

// prepareDialogParentWindow 在展示原生选择器前恢复并聚焦父窗口，避免面板附着在后台不可见窗口。
func prepareDialogParentWindow(window application.Window) {
	if window == nil {
		return
	}
	if window.IsMinimised() {
		window.UnMinimise()
	}
	if !window.IsVisible() {
		window.Show()
	}
	window.Focus()
}

// requireWailsApp 返回 Wails app，未绑定 runtime 时立即报错。
func (a *App) requireWailsApp() (*application.App, error) {
	if a == nil || a.wailsApp == nil {
		return nil, errors.New("wails binding: application is not ready")
	}
	return a.wailsApp, nil
}

// defaultDialogDirectory 返回 dialog 的默认目录。
// 获取当前目录失败时使用系统临时目录，保证原生 dialog 仍有可打开的起点。
func defaultDialogDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return os.TempDir()
	}
	return dir
}

// resolveDialogDirectory 解析 dialog 初始目录，非法路径回到默认目录。
// 该目录只影响原生选择器起点，不作为最终文件访问权限依据。
func resolveDialogDirectory(defaultPath string) string {
	path := strings.TrimSpace(defaultPath)
	if path == "" {
		return defaultDialogDirectory()
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return defaultDialogDirectory()
	}
	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		return defaultDialogDirectory()
	}
	return absPath
}

// normalizeSaveFilename 清理导出文件名，只保留 basename 以避免路径穿越。
func normalizeSaveFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	base := filepath.Base(name)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// isDialogCancelError 判断原生 dialog 错误是否表示用户取消。
func isDialogCancelError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "cancel")
}

// CopyText 将文本写入系统剪贴板。
func (a *App) CopyText(text string) (bool, error) {
	app, err := a.requireWailsApp()
	if err != nil {
		return false, err
	}
	return app.Clipboard.SetText(text), nil
}
