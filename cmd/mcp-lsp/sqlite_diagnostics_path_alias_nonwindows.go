//go:build !windows

package main

import "path/filepath"

// evalSQLiteDiagnosticsExistingPath 在非 Windows 上解析现有路径的符号链接。
func evalSQLiteDiagnosticsExistingPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
