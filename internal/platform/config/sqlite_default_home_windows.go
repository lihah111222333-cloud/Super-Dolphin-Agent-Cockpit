//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveDefaultSQLiteHome 在 Windows 上把默认状态目录固定到用户主目录，
// 避免把可变项目目录当成桌面应用的持久状态根。
func resolveDefaultSQLiteHome(_ string) (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return "", fmt.Errorf("SQLite path requires SUPER_DOLPHIN_HOME or user home directory on Windows")
	}
	return filepath.Join(userHome, ".super-dolphin", "state"), nil
}
