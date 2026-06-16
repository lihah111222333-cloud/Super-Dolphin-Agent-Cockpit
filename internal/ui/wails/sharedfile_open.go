package wails

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

type openSharedFileParams struct {
	Path string `json:"path"`
	clientMetaParams
}

type openSharedFileResult struct {
	Opened bool   `json:"opened"`
	Path   string `json:"path"`
}

func handleOpenSharedFile(
	ctx context.Context,
	app *App,
	cfg *config.Config,
	p openSharedFileParams,
) (openSharedFileResult, error) {
	_ = ctx
	if app == nil {
		return openSharedFileResult{}, errors.New("shared file open: app is required")
	}
	if cfg == nil {
		return openSharedFileResult{}, errors.New("shared file open: config is required")
	}
	abs, cleaned, err := resolveSharedFileOpenPathWithCleanPath(cfg.ProjectRoot, p.Path)
	if err != nil {
		return openSharedFileResult{}, err
	}
	if err := openSharedFileWithSystemDefault(abs); err != nil {
		return openSharedFileResult{}, fmt.Errorf("shared file open: open %q: %w", cleaned, err)
	}
	return openSharedFileResult{Opened: true, Path: cleaned}, nil
}

// resolveSharedFileOpenPathWithCleanPath 解析带clean路径的shared文件打开路径。
func resolveSharedFileOpenPathWithCleanPath(projectRoot, rawPath string) (string, string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return "", "", errors.New("shared file open: project root is required")
	}
	cleaned, err := sharedfilepath.ValidateReadPath(rawPath)
	if err != nil {
		return "", "", fmt.Errorf("shared file open: invalid path: %w", err)
	}
	fsCfg := sharedfilefs.Config{CWD: root}
	abs, err := fsCfg.ResolveAbs(cleaned)
	if err != nil {
		return "", "", fmt.Errorf("shared file open: resolve %q: %w", cleaned, err)
	}
	info, err := lstatSharedFileOpenPath(fsCfg.SandboxRoot(), cleaned, abs)
	if err != nil {
		return "", "", fmt.Errorf("shared file open: stat %q: %w", cleaned, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("shared file open: %q is not a regular file", cleaned)
	}
	return abs, cleaned, nil
}

// lstatSharedFileOpenPath 处理lstatshared文件打开路径。
func lstatSharedFileOpenPath(sandboxRoot, cleaned, abs string) (os.FileInfo, error) {
	current := filepath.Clean(sandboxRoot)
	rootInfo, err := os.Lstat(current)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("sandbox root is a symlink")
	}
	for _, part := range strings.Split(filepath.FromSlash(cleaned), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%q is a symlink", cleaned)
		}
		if filepath.Clean(current) == filepath.Clean(abs) {
			return info, nil
		}
	}
	return os.Lstat(abs)
}

// openSharedFileWithSystemDefault 打开带systemdefault的shared文件。
func openSharedFileWithSystemDefault(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("shared file open: resolved path is required")
	}
	switch runtime.GOOS {
	case "darwin":
		if openSystemPath("open", path) {
			return nil
		}
		return errors.New("open command failed")
	case "linux":
		if openSystemPath("xdg-open", path) {
			return nil
		}
		return errors.New("xdg-open command failed")
	case "windows":
		binary, err := exec.LookPath("rundll32")
		if err != nil {
			return err
		}
		if err := exec.Command(binary, "url.dll,FileProtocolHandler", path).Run(); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
}
