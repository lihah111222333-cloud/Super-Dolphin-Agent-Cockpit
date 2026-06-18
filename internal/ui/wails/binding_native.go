package wails

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

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

// decodeClipboardImagePayload 解码clipboardimage载荷。
func decodeClipboardImagePayload(payload string) ([]byte, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, errors.New("clipboard image: base64 payload is empty")
	}
	// 容错 data URL 前缀（例如 data:image/png;base64,AAAA）与裸 base64。
	if strings.HasPrefix(payload, "data:") {
		if idx := strings.Index(payload, ","); idx >= 0 {
			payload = payload[idx+1:]
		}
	}
	payload = stripBase64Whitespace(payload)
	data, err := decodeBase64Flexible(payload)
	if err != nil {
		return nil, fmt.Errorf("clipboard image: decode base64: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("clipboard image: payload decoded to zero bytes")
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

// decodeBase64Flexible 先按标准 base64 解码，失败时自动回退到 Raw 变体。
func decodeBase64Flexible(payload string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(payload); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(payload)
}

// SelectProjectDir 选择项目目录。
func (a *App) SelectProjectDir() (string, error) {
	return a.selectProjectDir("")
}

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

// SelectProjectDirs 选择项目目录。
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

// SelectFiles 选择文件。
func (a *App) SelectFiles() ([]string, error) {
	return a.selectFiles("")
}

func (a *App) selectFiles(defaultPath string) ([]string, error) {
	return a.selectFilesWithFilters(defaultPath, nil)
}

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

// saveTextFile 保存文本文件。
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

// promptExportDirectory 处理promptexportdirectory。
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

func (a *App) newDialog() (*application.OpenFileDialogStruct, error) {
	app, err := a.requireWailsApp()
	if err != nil {
		return nil, err
	}
	dialog := app.Dialog.OpenFile()
	if current := app.Window.Current(); current != nil {
		dialog = dialog.AttachToWindow(current)
	}
	return dialog, nil
}

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
	if current := app.Window.Current(); current != nil {
		dialog = dialog.AttachToWindow(current)
	}
	return dialog, nil
}

func (a *App) requireWailsApp() (*application.App, error) {
	if a == nil || a.wailsApp == nil {
		return nil, errors.New("wails binding: application is not ready")
	}
	return a.wailsApp, nil
}

func defaultDialogDirectory() string {
	dir, err := os.Getwd()
	if err != nil {
		return os.TempDir()
	}
	return dir
}

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

func isDialogCancelError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "cancel")
}

// CopyText 复制文本。
func (a *App) CopyText(text string) (bool, error) {
	app, err := a.requireWailsApp()
	if err != nil {
		return false, err
	}
	return app.Clipboard.SetText(text), nil
}
