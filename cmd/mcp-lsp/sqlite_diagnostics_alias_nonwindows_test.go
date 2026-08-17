//go:build !windows

package main

import (
	"os"
	"testing"
)

// createSQLiteDiagnosticsDirectoryAlias 在非 Windows 上用符号链接构造物理路径别名。
func createSQLiteDiagnosticsDirectoryAlias(t *testing.T, target, alias string) {
	t.Helper()
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("create SQLite diagnostics directory symlink: %v", err)
	}
}
