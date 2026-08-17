package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestSQLiteBackupRestoreSmoke(t *testing.T) {
	sourceDB, sourcePath := openMigratedSQLiteDB(t, "backup-source")
	seedSQLiteReleaseFixture(t, sourceDB, sqliteFixtureConfig{
		Threads:         10,
		SystemLogs:      50,
		PromptTemplates: 10,
		CronJobs:        10,
		DAGRuns:         10,
		Wakeups:         50,
		SessionInsights: 10,
	})
	insertSmokeThreadPromptCronAndDAG(t, sourceDB, "backup-source")
	checkpointSQLiteTruncate(t, sourceDB)
	if err := sourceDB.Close(); err != nil {
		t.Fatalf("close source DB before copy: %v", err)
	}

	restorePath := sqliteTestDBPath(t, "restored")
	copySQLiteFile(t, sourcePath, restorePath)
	restored, err := OpenTest(context.Background(), restorePath)
	if err != nil {
		t.Fatalf("open restored SQLite DB: %v", err)
	}
	defer restored.Close()
	verifySQLiteRestoreSmokeOrder(t, restored)
}

func verifySQLiteRestoreSmokeOrder(t *testing.T, db *sql.DB) {
	t.Helper()
	assertSQLiteIntegrityCheck(t, db)
	assertSQLiteForeignKeyCheck(t, db)
	assertSQLiteSchemaFloor(t, db)
	assertRestoredReadWritePaths(t, db)
}

func assertSQLiteIntegrityCheck(t *testing.T, db *sql.DB) {
	t.Helper()
	var got string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&got); err != nil {
		t.Fatalf("PRAGMA integrity_check failed: %v", err)
	}
	if got != "ok" {
		t.Fatalf("PRAGMA integrity_check = %q, want ok", got)
	}
}

func assertSQLiteForeignKeyCheck(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_check failed: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("PRAGMA foreign_key_check returned violations")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign_key_check: %v", err)
	}
}

func assertRestoredReadWritePaths(t *testing.T, db *sql.DB) {
	t.Helper()
	insertSmokeThreadPromptCronAndDAG(t, db, "restored")
	for table, where := range map[string]string{
		"agent_threads":          "thread_id = 'thread-restored'",
		"prompt_templates":       "prompt_key = 'prompt-restored'",
		"cron_jobs":              "id = 'cron-restored'",
		"task_dag_runs":          "run_key = 'run-restored'",
		"task_dag_wakeups":       "status IN ('pending', 'dispatching', 'sent', 'failed')",
		"session_insights":       "approval_requests_observed = 1 AND token_snapshot_observed = 1",
		"system_logs":            "thread_id <> '' AND agent_id <> ''",
		"agent_status":           "agent_id <> ''",
		"agent_provider_binding": "agent_id <> ''",
	} {
		assertWhereCountAtLeast(t, db, table, where, nil, 1)
	}
}

func checkpointSQLiteTruncate(t *testing.T, db *sql.DB) {
	t.Helper()
	var busy, logFrames, checkpointedFrames int
	if err := db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		t.Fatalf("wal_checkpoint(TRUNCATE): %v", err)
	}
	if busy != 0 {
		t.Fatalf("wal_checkpoint(TRUNCATE) busy=%d log=%d checkpointed=%d", busy, logFrames, checkpointedFrames)
	}
	t.Logf("wal_checkpoint(TRUNCATE): busy=%d log=%d checkpointed=%d", busy, logFrames, checkpointedFrames)
}

func copySQLiteFile(t *testing.T, sourcePath, destPath string) {
	t.Helper()
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read SQLite backup source %s: %v", sourcePath, err)
	}
	if len(body) == 0 {
		t.Fatalf("SQLite backup source %s is empty", sourcePath)
	}
	// Windows 的 t.TempDir 会继承测试宿主的宽 DACL；恢复合同要求父目录先成为
	// owner-only，再让生产 prepareFilesystem 校验；不存在的父目录仍由生产代码创建并收紧。
	destParent := filepath.Dir(destPath)
	if _, err := os.Stat(destParent); err == nil {
		if err := securefs.RestrictOwnerOnly(destParent, 0o700); err != nil {
			t.Fatalf("protect SQLite restore parent %s: %v", destParent, err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect SQLite restore parent %s: %v", destParent, err)
	}
	if err := prepareFilesystem(destPath); err != nil {
		t.Fatalf("prepare SQLite restore target %s: %v", destPath, err)
	}
	if err := os.WriteFile(destPath, body, 0o600); err != nil {
		t.Fatalf("write SQLite restore target %s: %v", destPath, err)
	}
	if err := RestrictSidecarFilePermissions(destPath); err != nil {
		t.Fatalf("verify SQLite restore target owner-only %s: %v", destPath, err)
	}
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("stat SQLite restore target %s: %v", destPath, err)
	}
	if info.Size() != int64(len(body)) {
		t.Fatalf("restored SQLite size = %d, want %d", info.Size(), len(body))
	}
	t.Logf("quiesced SQLite copy complete: source=%s target=%s bytes=%d", sourcePath, destPath, len(body))
}
