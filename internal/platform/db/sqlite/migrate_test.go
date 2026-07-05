package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunMigrationsRollsBackBodyAndMarkerOnFailure(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "001_baseline.sql", baselineMigrationForTest())
	writeMigrationTestFile(t, dir, "002_bad.sql", `
CREATE TABLE rollback_probe(id INTEGER PRIMARY KEY);
INSERT INTO missing_table(id) VALUES (1);
`)

	err := RunMigrations(ctx, db, dir)
	if err == nil {
		t.Fatal("RunMigrations() error = nil, want migration failure")
	}
	assertMigrationTableMissing(t, db, "rollback_probe")
	assertMigrationMarkerCount(t, db, "001_baseline.sql", 1)
	assertMigrationMarkerCount(t, db, "002_bad.sql", 0)
}

func TestRunMigrationsRejectsInvalidVersionWithoutMarker(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "001_baseline.sql", baselineMigrationForTest())
	writeMigrationTestFile(t, dir, "bad_name.sql", "SELECT 1;\n")

	err := RunMigrations(ctx, db, dir)
	if err == nil {
		t.Fatal("RunMigrations() error = nil, want invalid version failure")
	}
	assertMigrationMarkerCount(t, db, "bad_name.sql", 0)
}

func TestRunMigrationsAcceptsSelfRecordedBaselineMarker(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "001_baseline.sql", baselineMigrationForTest())

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	assertMigrationMarkerCount(t, db, "001_baseline.sql", 1)
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("query max migration version: %v", err)
	}
	if version != 103 {
		t.Fatalf("max migration version = %d, want 103", version)
	}
}

func TestRunMigrationsSystemLogsTraceSpanPreservesAgentV3Columns(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	mustExec(t, db, `
		CREATE TABLE system_logs (
			id INTEGER PRIMARY KEY,
			ts INTEGER NOT NULL,
			level TEXT NOT NULL,
			logger TEXT NOT NULL,
			message TEXT NOT NULL,
			raw TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			component TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER,
			extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
		)
	`)
	mustExec(t, db, `
		INSERT INTO system_logs (
			id, ts, level, logger, message, raw, source, component,
			agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
		)
		VALUES (
			10, 1710000000000, 'warn', 'mcp-control', 'agent-v3 row', 'raw',
			'mcp-control', 'mcp-lsp', 'agent-1', 'thread-1',
			'trace-1', 'ctl/log', 'definition', 42, '{"ok":true}'
		)
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "112_system_logs_trace_span.sql", "SELECT broken_reference FROM nowhere;")

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	assertMigrationMarkerCount(t, db, "112_system_logs_trace_span.sql", 1)
	assertSystemLogsTraceSpanRow(t, db, systemLogsTraceSpanWant{
		id:      10,
		source:  "mcp-control",
		traceID: "trace-1",
		spanID:  "",
		extra:   `{"ok":true}`,
	})
}

func TestRunMigrationsSystemLogsTraceSpanAcceptsCurrentAgentV3Table(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	mustExec(t, db, `
		CREATE TABLE system_logs (
			id INTEGER PRIMARY KEY,
			ts INTEGER NOT NULL,
			level TEXT NOT NULL,
			logger TEXT NOT NULL,
			message TEXT NOT NULL,
			raw TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			component TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			span_id TEXT NOT NULL DEFAULT '',
			parent_span_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER,
			extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
		)
	`)
	mustExec(t, db, `
		INSERT INTO system_logs (
			id, ts, level, logger, message, raw, source, component,
			agent_id, thread_id, trace_id, span_id, parent_span_id,
			event_type, tool_name, duration_ms, extra
		)
		VALUES (
			11, 1710000000000, 'info', 'mcp-control', 'current row', 'raw',
			'mcp-control', 'mcp-lsp', 'agent-1', 'thread-1',
			'trace-2', 'span-2', 'parent-2', 'ctl/log', 'definition', 7, '{"ok":true}'
		)
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "112_system_logs_trace_span.sql", "SELECT broken_reference FROM nowhere;")

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	assertMigrationMarkerCount(t, db, "112_system_logs_trace_span.sql", 1)
	assertSystemLogsTraceSpanRow(t, db, systemLogsTraceSpanWant{
		id:           11,
		source:       "mcp-control",
		traceID:      "trace-2",
		spanID:       "span-2",
		parentSpanID: "parent-2",
		extra:        `{"ok":true}`,
	})
}

// TestRunMigrationsAddsCronJobRunsTurnStatusIndex verifies migration 114 adds the terminal turn lookup index.
func TestRunMigrationsAddsCronJobRunsTurnStatusIndex(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	mustExec(t, db, `
		CREATE TABLE cron_job_runs (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			scheduled_at INTEGER NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			dedupe_key TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			turn_id TEXT NOT NULL DEFAULT '',
			submitted_at INTEGER,
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "114_cron_job_runs_turn_status_index.sql", readMigrationTestFile(t, "114_cron_job_runs_turn_status_index.sql"))

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	assertMigrationMarkerCount(t, db, "114_cron_job_runs_turn_status_index.sql", 1)
	assertIndex(t, db, "cron_job_runs", "idx_cron_job_runs_turn_status", false, "turn_id <> '' AND status IN ('submitted', 'running')")
}

// TestDatasourceDocumentsTableComesFromMigration verifies migration 116 owns datasource_documents.
func TestDatasourceDocumentsTableComesFromMigration(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "116_datasource_documents.sql", readMigrationTestFile(t, "116_datasource_documents.sql"))

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	if !sqliteTables(t, db)["datasource_documents"] {
		t.Fatal("datasource_documents table missing after migration 116")
	}
	assertMigrationMarkerCount(t, db, "116_datasource_documents.sql", 1)
	assertPrimaryKey(t, db, "datasource_documents", []string{"workspace_root", "name"})
	assertNotNullColumns(t, db, "datasource_documents", []string{"workspace_root", "name", "extension", "size_bytes", "stored_path", "content", "created_at", "updated_at"})
	assertTableSQLContains(t, db, "datasource_documents", []string{"size_bytes >= 0", "content <> ''"})
	assertIndex(t, db, "datasource_documents", "idx_datasource_documents_workspace_name", false, "")
}

func TestRunMigrationsSystemLogsTraceSpanRejectsLegacyShape(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	mustExec(t, db, `
		CREATE TABLE system_logs (
			id INTEGER PRIMARY KEY,
			ts INTEGER NOT NULL,
			level TEXT NOT NULL,
			logger TEXT NOT NULL,
			message TEXT NOT NULL,
			raw TEXT NOT NULL DEFAULT ''
		)
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "112_system_logs_trace_span.sql", "SELECT broken_reference FROM nowhere;")

	err := RunMigrations(ctx, db, dir)
	if err == nil {
		t.Fatal("RunMigrations() error = nil, want legacy system_logs shape rejection")
	}
	if !strings.Contains(err.Error(), "system_logs missing required column source") {
		t.Fatalf("RunMigrations() error = %v, want missing source", err)
	}
	assertMigrationMarkerCount(t, db, "112_system_logs_trace_span.sql", 0)
}

func baselineMigrationForTest() string {
	return `
CREATE TABLE schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	filename TEXT NOT NULL,
	applied_at INTEGER NOT NULL
);
CREATE TABLE runtime_probe(id INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO schema_migrations(version, name, filename, applied_at)
VALUES (103, 'sqlite baseline', '001_baseline.sql', 0);
`
}

func createMigrationMarkerTable(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			filename TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)
	`)
}

func markBaselineApplied(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		INSERT INTO schema_migrations(version, name, filename, applied_at)
		VALUES (103, 'sqlite baseline', '001_baseline.sql', 0)
	`)
}

type systemLogsTraceSpanWant struct {
	id           int64
	source       string
	traceID      string
	spanID       string
	parentSpanID string
	extra        string
}

func assertSystemLogsTraceSpanRow(t *testing.T, db *sql.DB, want systemLogsTraceSpanWant) {
	t.Helper()
	var source, traceID, spanID, parentSpanID, extra string
	if err := db.QueryRow(`
		SELECT source, trace_id, span_id, parent_span_id, extra
		FROM system_logs
		WHERE id = ?
	`, want.id).Scan(&source, &traceID, &spanID, &parentSpanID, &extra); err != nil {
		t.Fatalf("read migrated system log %d: %v", want.id, err)
	}
	if source != want.source || traceID != want.traceID || spanID != want.spanID || parentSpanID != want.parentSpanID || extra != want.extra {
		t.Fatalf("migrated row = source:%q trace:%q span:%q parent:%q extra:%q, want source:%q trace:%q span:%q parent:%q extra:%q",
			source, traceID, spanID, parentSpanID, extra, want.source, want.traceID, want.spanID, want.parentSpanID, want.extra)
	}
	assertIndex(t, db, "system_logs", "idx_system_logs_trace_ts_id", false, "trace_id <> ''")
	assertIndex(t, db, "system_logs", "idx_system_logs_span_ts_id", false, "span_id <> ''")
}

func openMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, filepath.Join(t.TempDir(), "migration-test.db"))
	if err != nil {
		t.Fatalf("open migration test DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeMigrationTestFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write migration %s: %v", name, err)
	}
}

func readMigrationTestFile(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(sqliteMigrationsDir(t), name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(body)
}

func assertMigrationTableMissing(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		t.Fatalf("probe table %s: %v", table, err)
	}
	t.Fatalf("table %s exists after failed migration", table)
}

func assertMigrationMarkerCount(t *testing.T, db *sql.DB, filename string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE filename = ?", filename).Scan(&got); err != nil {
		t.Fatalf("count migration marker %s: %v", filename, err)
	}
	if got != want {
		t.Fatalf("migration marker count for %s = %d, want %d", filename, got, want)
	}
}
