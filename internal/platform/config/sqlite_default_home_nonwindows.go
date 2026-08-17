//go:build !windows

package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// resolveDefaultSQLiteHome 在非 Windows 平台保持项目内状态目录契约，
// 让 Windows 桌面状态策略不会改变 Linux、Darwin 或其他受支持系统。
func resolveDefaultSQLiteHome(projectRoot string) (string, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return "", fmt.Errorf("SQLite path requires PROJECT_ROOT or SUPER_DOLPHIN_HOME")
	}
	return filepath.Join(projectRoot, ".super-dolphin"), nil
}
