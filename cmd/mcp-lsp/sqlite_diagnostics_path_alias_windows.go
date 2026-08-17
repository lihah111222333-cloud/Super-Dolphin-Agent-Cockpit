//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const sqliteDiagnosticsWindowsFinalPathBuffer = 32768

// evalSQLiteDiagnosticsExistingPath 通过最终文件句柄解析 Windows 符号链接与 junction；
// filepath.EvalSymlinks 不会在所有 Go/Windows 组合上展开目录 junction。
func evalSQLiteDiagnosticsExistingPath(path string) (resolved string, retErr error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()

	buffer := make([]uint16, sqliteDiagnosticsWindowsFinalPathBuffer)
	length, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", fmt.Errorf("resolve final Windows SQLite diagnostics path: %w", err)
	}
	if length == 0 || length >= uint32(len(buffer)) {
		return "", fmt.Errorf("final Windows SQLite diagnostics path length %d is invalid", length)
	}
	resolved = windows.UTF16ToString(buffer[:length])
	if suffix, ok := strings.CutPrefix(resolved, `\\?\UNC\`); ok {
		resolved = `\\` + suffix
	} else {
		resolved, _ = strings.CutPrefix(resolved, `\\?\`)
	}
	return filepath.Clean(resolved), nil
}
