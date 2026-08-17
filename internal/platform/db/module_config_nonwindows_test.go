//go:build !windows

package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// exerciseSQLiteRestrictiveFiles 保留 Unix 文件模式与 SQLite sidecar 断言；Windows 使用 ACL
// 专用测试覆盖同一生产路径，因此公共数据库测试不再读取 runtime.GOOS 分支。
func exerciseSQLiteRestrictiveFiles(t *testing.T, database *sql.DB, path string) {
	t.Helper()
	assertSQLiteFileMode(t, path, 0o600)
	if _, err := database.ExecContext(context.Background(), "CREATE TABLE file_mode_probe(id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatalf("create file mode probe: %v", err)
	}
	if _, err := database.ExecContext(context.Background(), "INSERT INTO file_mode_probe(value) VALUES ('x')"); err != nil {
		t.Fatalf("insert file mode probe: %v", err)
	}
	if err := sqliteruntime.RestrictSidecarFilePermissions(path); err != nil {
		t.Fatalf("RestrictSidecarFilePermissions() error = %v", err)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			continue
		}
		assertSQLiteFileMode(t, candidate, 0o600)
	}
}

func TestNewDBRejectsUnwritableExistingParentWithRedaction(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	path := filepath.Join(parent, "super-dolphin.db")

	database, err := NewDB(&config.Config{SQLitePath: path})
	if err == nil {
		_ = database.Close()
		t.Fatal("NewDB() error = nil, want unwritable parent fail-fast")
	}
	if strings.Contains(err.Error(), parent) || strings.Contains(err.Error(), path) {
		t.Fatalf("NewDB() error leaked full path: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted:state>") {
		t.Fatalf("NewDB() error = %v, want redacted parent path", err)
	}
}
