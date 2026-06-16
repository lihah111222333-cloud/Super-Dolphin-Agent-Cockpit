package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
)

func TestNewDBRejectsEmptySQLitePath(t *testing.T) {
	database, err := NewDB(&config.Config{})
	if err == nil {
		if database != nil {
			_ = database.Close()
		}
		t.Fatal("NewDB() error = nil, want empty SQLite path fail-fast")
	}
	if database != nil {
		t.Fatalf("NewDB() database = %v, want nil on empty SQLite path", database)
	}
	if !strings.Contains(err.Error(), "SQLite path") {
		t.Fatalf("NewDB() error = %v, want SQLite path guidance", err)
	}
}

func TestNewDBRejectsDirectorySQLitePathWithRedaction(t *testing.T) {
	dir := t.TempDir()

	database, err := NewDB(&config.Config{SQLitePath: dir})
	if err == nil {
		if database != nil {
			_ = database.Close()
		}
		t.Fatal("NewDB() error = nil, want directory path fail-fast")
	}
	if strings.Contains(err.Error(), dir) {
		t.Fatalf("NewDB() error leaked full path: %v", err)
	}
	if !strings.Contains(err.Error(), "<redacted:") {
		t.Fatalf("NewDB() error = %v, want redacted path", err)
	}
}

func TestNewDBRejectsParentFileWithParentRedaction(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "private-parent")
	if err := os.WriteFile(parentFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	dbPath := filepath.Join(parentFile, "secret.db")

	database, err := NewDB(&config.Config{SQLitePath: dbPath})
	if err == nil {
		if database != nil {
			_ = database.Close()
		}
		t.Fatal("NewDB() error = nil, want parent-file fail-fast")
	}
	if database != nil {
		t.Fatalf("NewDB() database = %v, want nil on parent-file path", database)
	}
	if strings.Contains(err.Error(), parentFile) || strings.Contains(err.Error(), dbPath) {
		t.Fatalf("NewDB() error leaked full path: %v", err)
	}
	if strings.Contains(err.Error(), "<redacted:secret.db>") {
		t.Fatalf("NewDB() error = %v, want parent path redaction, not leaf path", err)
	}
	if !strings.Contains(err.Error(), "<redacted:private-parent>") {
		t.Fatalf("NewDB() error = %v, want redacted parent path", err)
	}
}

func TestNewDBCreatesSQLiteWithPragmasAndRestrictiveFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "super-dolphin.db")

	database, err := NewDB(&config.Config{SQLitePath: path})
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	assertIntPragma(t, database, "foreign_keys", 1)
	assertTextPragma(t, database, "journal_mode", "wal")
	assertIntPragma(t, database, "busy_timeout", 5000)
	assertIntPragma(t, database, "synchronous", 2)
	assertIntPragma(t, database, "wal_autocheckpoint", 1000)
	if got := database.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}

	if runtime.GOOS != "windows" {
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
}

func TestNewDBRejectsReadOnlyDatabaseFileWithRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "super-dolphin.db")
	database, err := NewDB(&config.Config{SQLitePath: path})
	if err != nil {
		t.Fatalf("NewDB() setup error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close setup database: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("chmod read-only database: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	database, err = NewDB(&config.Config{SQLitePath: path})
	if err == nil {
		_ = database.Close()
		t.Fatal("NewDB() error = nil, want read-only DB fail-fast")
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("NewDB() error leaked full path: %v", err)
	}
	if !strings.Contains(err.Error(), "SQLite database file is not writable") {
		t.Fatalf("NewDB() error = %v, want explicit DB file writability failure", err)
	}
	if !strings.Contains(err.Error(), "<redacted:super-dolphin.db>") {
		t.Fatalf("NewDB() error = %v, want redacted DB path", err)
	}
}

func TestNewDBRejectsUnwritableExistingParentWithRedaction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows writability is covered by ACL checks and read-only file attribute coverage")
	}
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

func TestRunSQLiteMigrationsAndSchemaGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "super-dolphin.db")
	database, err := NewDB(&config.Config{SQLitePath: path})
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := runFixtureMigrations(t, database); err != nil {
		t.Fatalf("runFixtureMigrations() error = %v", err)
	}
	if err := VerifyMinSchemaVersion(context.Background(), database); err != nil {
		t.Fatalf("VerifyMinSchemaVersion() error = %v", err)
	}
	for _, table := range requiredBaselineTables {
		assertSQLiteTableExists(t, database, table)
	}
	assertMigrationMarkerCount(t, database, "001_baseline.sql", 1)
}

func TestVerifyMinSchemaVersionRejectsBelowMinimumSQLiteDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "super-dolphin.db")
	database, err := NewDB(&config.Config{SQLitePath: path})
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.ExecContext(context.Background(), `
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  filename TEXT NOT NULL,
  applied_at INTEGER NOT NULL
);
INSERT INTO schema_migrations(version, name, filename, applied_at)
VALUES (?, 'old', '0102_old.sql', 0);
`, MinRequiredSchemaVersion-1); err != nil {
		t.Fatalf("seed below-minimum schema_migrations: %v", err)
	}

	err = VerifyMinSchemaVersion(context.Background(), database)
	if err == nil {
		t.Fatal("VerifyMinSchemaVersion() error = nil, want below-minimum SQLite DB rejection")
	}
	if !strings.Contains(err.Error(), "database migration version") {
		t.Fatalf("VerifyMinSchemaVersion() error = %v, want schema gate guidance", err)
	}
}

func TestSQLiteLifecycleRejectsMarkerOnlyBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "super-dolphin.db")
	database, err := NewDB(&config.Config{SQLitePath: path})
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	seedMarkerOnlyBaseline(t, database)

	if err := runFixtureMigrations(t, database); err != nil {
		t.Logf("runFixtureMigrations() rejected marker-only baseline before schema verify: %v", err)
	}
	err = VerifyMinSchemaVersion(context.Background(), database)
	if err == nil {
		t.Fatal("VerifyMinSchemaVersion() error = nil, want marker-only SQLite baseline rejection")
	}
	msg := err.Error()
	for _, want := range []string{"SQLite baseline schema incomplete", "marker-only", "agent_threads"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("VerifyMinSchemaVersion() error = %v, want %q", err, want)
		}
	}
}

func TestVerifyMinSchemaVersionRejectsOld26TablePartialBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "super-dolphin.db")
	database, err := NewDB(&config.Config{SQLitePath: path})
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	seedMarkerOnlyBaseline(t, database)
	for _, table := range legacySQLiteBaselineGateTablesForRegression {
		createSQLiteBaselineProbeTable(t, database, table)
	}

	err = VerifyMinSchemaVersion(context.Background(), database)
	if err == nil {
		t.Fatal("VerifyMinSchemaVersion() error = nil, want partial Task 02 baseline rejection")
	}
	msg := err.Error()
	for _, want := range []string{"SQLite baseline schema incomplete", "partial baseline", "runtime_locks", "task_dag_runs"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("VerifyMinSchemaVersion() error = %v, want %q", err, want)
		}
	}
}

func TestCheckpointTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "super-dolphin.db")
	database, err := NewDB(&config.Config{SQLitePath: path})
	if err != nil {
		t.Fatalf("NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.ExecContext(context.Background(), "CREATE TABLE checkpoint_probe(id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatalf("create checkpoint probe: %v", err)
	}
	if _, err := database.ExecContext(context.Background(), "INSERT INTO checkpoint_probe(value) VALUES ('x')"); err != nil {
		t.Fatalf("insert checkpoint probe: %v", err)
	}
	if err := Checkpoint(context.Background(), database, "TRUNCATE"); err != nil {
		t.Fatalf("Checkpoint(TRUNCATE) error = %v", err)
	}
}

var legacySQLiteBaselineGateTablesForRegression = []string{
	"agent_codex_binding",
	"agent_interactions",
	"agent_provider_binding",
	"agent_status",
	"agent_threads",
	"audit_events",
	"bus_exception_logs",
	"command_card_runs",
	"command_card_versions",
	"command_cards",
	"cwd_instance_locks",
	"prompt_template_versions",
	"prompt_versions",
	"prompt_templates",
	"prompts",
	"shared_files",
	"system_logs",
	"task_acks",
	"task_dag_nodes",
	"task_dags",
	"task_traces",
	"topology_approval_archives",
	"topology_approvals",
	"ui_preferences",
	"workspace_run_files",
	"workspace_runs",
}

func createSQLiteBaselineProbeTable(t *testing.T, database *sql.DB, table string) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), "CREATE TABLE "+table+" (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create baseline probe table %s: %v", table, err)
	}
}

func seedMarkerOnlyBaseline(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), `
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  filename TEXT NOT NULL,
  applied_at INTEGER NOT NULL
);
INSERT INTO schema_migrations(version, name, filename, applied_at)
VALUES (?, 'sqlite baseline', '001_baseline.sql', 0);
`, MinRequiredSchemaVersion); err != nil {
		t.Fatalf("seed marker-only baseline: %v", err)
	}
}

func assertSQLiteFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat SQLite file %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("SQLite file %q mode = %o, want %o", filepath.Base(path), got, want)
	}
}

func assertSQLiteTableExists(t *testing.T, database *sql.DB, table string) {
	t.Helper()
	var name string
	if err := database.QueryRowContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name); err != nil {
		t.Fatalf("table %s missing: %v", table, err)
	}
}

func assertMigrationMarkerCount(t *testing.T, database *sql.DB, filename string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations WHERE filename = ?", filename).Scan(&got); err != nil {
		t.Fatalf("count migration marker %s: %v", filename, err)
	}
	if got != want {
		t.Fatalf("migration marker count for %s = %d, want %d", filename, got, want)
	}
}
