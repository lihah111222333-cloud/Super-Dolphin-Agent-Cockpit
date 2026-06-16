package wails

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type pathOpenResult struct {
	Ok        bool   `json:"ok"`
	Opened    bool   `json:"opened"`
	Path      string `json:"path"`
	FilePath  string `json:"filePath"`
	Relative  string `json:"relative"`
	Directory bool   `json:"directory,omitempty"`
}

// openScopedPath 打开项目范围内的文件或目录；只做范围校验和系统打开，不读取文件内容。
func openScopedPath(ctx context.Context, rawPath string, line, column int, roots []string) (pathOpenResult, error) {
	target, info, err := resolvePathOpenTarget(ctx, rawPath, roots)
	if err != nil {
		return pathOpenResult{}, err
	}
	openLine, openColumn := line, column
	if info.IsDir() {
		openLine, openColumn = 0, 0
	}
	if !openCodeEditor(target.Abs, openLine, openColumn) {
		return pathOpenResult{}, fmt.Errorf("ui/path/open: open %q failed", target.Relative)
	}
	return pathOpenResult{
		Ok:        true,
		Opened:    true,
		Path:      target.Abs,
		FilePath:  target.Abs,
		Relative:  target.Relative,
		Directory: info.IsDir(),
	}, nil
}

// resolvePathOpenTarget 解析聊天里的本地路径引用，允许文件和目录，但必须落在当前项目根内。
func resolvePathOpenTarget(ctx context.Context, raw string, roots []string) (scopedPath, os.FileInfo, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return scopedPath{}, nil, errors.New("ui/path/open: filePath is required")
	}
	if filepath.IsAbs(value) {
		return matchExistingPathOpenTarget(value, roots)
	}
	if target, info, ok := firstExistingRelativePathOpenTarget(value, roots); ok {
		return target, info, nil
	}
	matches, _, err := findScopedFiles(ctx, value, roots, 1)
	if err != nil {
		return scopedPath{}, nil, err
	}
	if len(matches) == 0 {
		return scopedPath{}, nil, fs.ErrNotExist
	}
	info, err := os.Stat(matches[0].Abs)
	if err != nil {
		return scopedPath{}, nil, err
	}
	return matches[0], info, nil
}

func matchExistingPathOpenTarget(raw string, roots []string) (scopedPath, os.FileInfo, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(raw))
	if err != nil {
		return scopedPath{}, nil, err
	}
	var lastErr error
	for _, root := range roots {
		target, info, err := existingScopedPathCandidate(root, absPath)
		if err == nil {
			return target, info, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fs.ErrNotExist
	}
	return scopedPath{}, nil, lastErr
}

func firstExistingRelativePathOpenTarget(raw string, roots []string) (scopedPath, os.FileInfo, bool) {
	for _, root := range roots {
		target, info, err := existingScopedPathCandidate(root, filepath.Join(root, raw))
		if err == nil {
			return target, info, true
		}
	}
	return scopedPath{}, nil, false
}

func existingScopedPathCandidate(root, candidate string) (scopedPath, os.FileInfo, error) {
	absPath, err := filepath.Abs(candidate)
	if err != nil {
		return scopedPath{}, nil, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return scopedPath{}, nil, err
	}
	relative, err := secureRelativeToRoot(root, absPath)
	if err != nil {
		return scopedPath{}, nil, err
	}
	return scopedPath{
		Root:     root,
		Abs:      absPath,
		Relative: relative,
	}, info, nil
}
