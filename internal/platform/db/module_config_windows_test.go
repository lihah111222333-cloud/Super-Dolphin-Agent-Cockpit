//go:build windows

package db

import (
	"database/sql"
	"testing"
)

// exerciseSQLiteRestrictiveFiles 在 Windows 上保持旧的跳过语义；ACL/只读属性由 Windows
// 专用测试覆盖，避免把 Unix mode bits 误当成 Windows 权限证明。
func exerciseSQLiteRestrictiveFiles(t *testing.T, database *sql.DB, path string) {
	t.Helper()
}
