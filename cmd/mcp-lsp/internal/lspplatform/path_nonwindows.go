//go:build !windows

package lspplatform

import (
	"fmt"
	"os"
	"path/filepath"
)

// CanonicalDirectoryPath 在非 Windows 平台保留原有符号链接规范化语义。
func CanonicalDirectoryPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("canonical path is not a directory: %s", resolved)
	}
	return resolved, nil
}

// CanonicalExistingPath 在非 Windows 平台解析现存文件或目录的符号链接。
func CanonicalExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
