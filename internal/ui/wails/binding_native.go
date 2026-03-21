package wails

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func (a *App) SaveClipboardImage(filename string) (string, error) {
	path, err := resolveClipboardPath(filename)
	if err != nil {
		return "", err
	}
	if err := saveClipboardImage(path); err != nil {
		return "", err
	}
	return path, nil
}

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

func (a *App) SelectFiles() ([]string, error) {
	return a.selectFiles("")
}

func (a *App) selectFiles(defaultPath string) ([]string, error) {
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
	paths, err := dialog.PromptForMultipleSelection()
	if isDialogCancelError(err) || len(paths) == 0 {
		return []string{}, nil
	}
	return paths, err
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

func isDialogCancelError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "cancel")
}

func (a *App) CopyText(text string) (bool, error) {
	app, err := a.requireWailsApp()
	if err != nil {
		return false, err
	}
	return app.Clipboard.SetText(text), nil
}

func resolveClipboardPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		file, err := os.CreateTemp("", "clipboard-*.png")
		if err != nil {
			return "", err
		}
		defer file.Close()
		return file.Name(), nil
	}
	if filepath.Ext(name) == "" {
		name += ".png"
	}
	path, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func saveClipboardImage(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return saveClipboardImageDarwin(path)
	case "linux":
		return saveClipboardImageLinux(path)
	case "windows":
		return saveClipboardImageWindows(path)
	default:
		return fmt.Errorf("clipboard image is not supported on %s", runtime.GOOS)
	}
}

func saveClipboardImageDarwin(path string) error {
	command, err := exec.LookPath("pngpaste")
	if err != nil {
		return errors.New("clipboard image capture on darwin requires pngpaste")
	}
	return exec.Command(command, path).Run()
}

func saveClipboardImageLinux(path string) error {
	if command, err := exec.LookPath("wl-paste"); err == nil {
		return pipeCommandToFile(command, []string{"--no-newline", "--type", "image/png"}, path)
	}
	if command, err := exec.LookPath("xclip"); err == nil {
		return pipeCommandToFile(command, []string{"-selection", "clipboard", "-t", "image/png", "-o"}, path)
	}
	return errors.New("clipboard image capture on linux requires wl-paste or xclip")
}

func saveClipboardImageWindows(path string) error {
	command, err := exec.LookPath("powershell")
	if err != nil {
		return errors.New("clipboard image capture on windows requires powershell")
	}
	script := fmt.Sprintf(
		"Add-Type -AssemblyName System.Drawing; "+
			"$img = Get-Clipboard -Format Image; "+
			"if ($null -eq $img) { throw 'clipboard does not contain an image' }; "+
			"$img.Save('%s', [System.Drawing.Imaging.ImageFormat]::Png)",
		strings.ReplaceAll(path, "'", "''"),
	)
	return exec.Command(command, "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func pipeCommandToFile(command string, args []string, path string) error {
	data, err := exec.Command(command, args...).Output()
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("clipboard does not contain a PNG image")
	}
	return os.WriteFile(path, data, 0o600)
}
