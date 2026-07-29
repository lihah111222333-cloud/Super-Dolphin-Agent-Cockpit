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

// TestRunMigrationsThreadTimestampMillisConvertsPersistedSeconds 验证历史 thread 秒时间戳会在持久化边界一次性升级为毫秒。
func TestRunMigrationsThreadTimestampMillisConvertsPersistedSeconds(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	createThreadTimestampMigrationTables(t, db)
	mustExec(t, db, `
		INSERT INTO agent_threads(thread_id, created_at, updated_at, finished_at)
		VALUES ('seconds', 1784719357, 1784719358, 1784719359),
		       ('millis', 1784719357000, 1784719358000, NULL);
		INSERT INTO agent_provider_binding(agent_id, created_at, updated_at)
		VALUES ('agent-seconds', 1784719357, 1784719358),
		       ('agent-millis', 1784719357000, 1784719358000)
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "118_thread_timestamp_millis.sql", readMigrationTestFile(t, "118_thread_timestamp_millis.sql"))

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	assertThreadTimestampRow(t, db, "seconds", 1784719357000, 1784719358000, 1784719359000)
	assertThreadTimestampRow(t, db, "millis", 1784719357000, 1784719358000, 0)
	assertBindingTimestampRow(t, db, "agent-seconds", 1784719357000, 1784719358000)
	assertBindingTimestampRow(t, db, "agent-millis", 1784719357000, 1784719358000)
	assertMigrationMarkerCount(t, db, "118_thread_timestamp_millis.sql", 1)
}

// TestRunMigrationsThreadTimestampMillisRejectsInvalidRange 验证非零且既非合理秒值也非毫秒值的数据会阻断并回滚迁移。
func TestRunMigrationsThreadTimestampMillisRejectsInvalidRange(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	createThreadTimestampMigrationTables(t, db)
	mustExec(t, db, "INSERT INTO agent_threads(thread_id, created_at, updated_at) VALUES ('invalid', 123, 1784719357)")
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "118_thread_timestamp_millis.sql", readMigrationTestFile(t, "118_thread_timestamp_millis.sql"))

	err := RunMigrations(ctx, db, dir)
	if err == nil {
		t.Fatal("RunMigrations() error = nil, want invalid thread timestamp rejection")
	}
	assertThreadTimestampRow(t, db, "invalid", 123, 1784719357, 0)
	assertMigrationMarkerCount(t, db, "118_thread_timestamp_millis.sql", 0)
}

func TestRunMigrationsCanonicalizesProviderBindingUUIDsAndRestoresTrigger(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	createProviderBindingUUIDMigrationTable(t, db)
	mustExec(t, db, `
		INSERT INTO agent_provider_binding (
			agent_id, provider, provider_thread_id, session_uuid, codex_home
		) VALUES (
			'agent-upgrade', 'codex',
			'019E218FB5147733BE85B3EE7F6A78A6',
			'019E218F-B514-7733-BE85-B3EE7F6A78A7',
			'/instances/codex-a'
		)
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "120_agent_provider_binding_recovery_owner.sql", "SELECT 1;\n")

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	var providerThreadID, sessionUUID, recoveryHome string
	if err := db.QueryRow(`
		SELECT provider_thread_id, session_uuid, provider_recovery_home
		FROM agent_provider_binding WHERE agent_id = 'agent-upgrade'
	`).Scan(&providerThreadID, &sessionUUID, &recoveryHome); err != nil {
		t.Fatalf("read upgraded binding: %v", err)
	}
	if providerThreadID != "019e218f-b514-7733-be85-b3ee7f6a78a6" {
		t.Fatalf("provider_thread_id = %q, want canonical UUID", providerThreadID)
	}
	if sessionUUID != "019e218f-b514-7733-be85-b3ee7f6a78a7" {
		t.Fatalf("session_uuid = %q, want canonical UUID", sessionUUID)
	}
	if recoveryHome != "/instances/codex-a" {
		t.Fatalf("provider_recovery_home = %q, want authoritative codex_home backfill", recoveryHome)
	}
	_, err := db.Exec(`
		UPDATE agent_provider_binding
		SET provider_thread_id = '019e218f-b514-7733-be85-b3ee7f6a78a8'
		WHERE agent_id = 'agent-upgrade'
	`)
	if err == nil || !strings.Contains(err.Error(), "identity is immutable") {
		t.Fatalf("restored trigger update error = %v, want immutable rejection", err)
	}
	assertMigrationMarkerCount(t, db, "120_agent_provider_binding_recovery_owner.sql", 1)
}

func TestRunMigrationsRejectsCanonicalProviderBindingUUIDCollision(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	createProviderBindingUUIDMigrationTable(t, db)
	mustExec(t, db, `
		INSERT INTO agent_provider_binding(agent_id, provider, provider_thread_id)
		VALUES
			('agent-a', 'claude', '019E218FB5147733BE85B3EE7F6A78A6'),
			('agent-b', 'claude', '019e218f-b514-7733-be85-b3ee7f6a78a6')
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "120_agent_provider_binding_recovery_owner.sql", "SELECT 1;\n")

	err := RunMigrations(ctx, db, dir)
	if err == nil || !strings.Contains(err.Error(), "canonical UUID collision") {
		t.Fatalf("RunMigrations() error = %v, want canonical UUID collision", err)
	}
	assertMigrationMarkerCount(t, db, "120_agent_provider_binding_recovery_owner.sql", 0)
}

func TestRunMigrationsRejectsInvalidProviderBindingUUID(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	createProviderBindingUUIDMigrationTable(t, db)
	mustExec(t, db, `
		INSERT INTO agent_provider_binding(agent_id, provider, provider_thread_id)
		VALUES ('agent-invalid', 'claude', 'not-a-provider-uuid')
	`)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "120_agent_provider_binding_recovery_owner.sql", "SELECT 1;\n")

	err := RunMigrations(ctx, db, dir)
	if err == nil || !strings.Contains(err.Error(), "provider_thread_id") {
		t.Fatalf("RunMigrations() error = %v, want invalid provider_thread_id rejection", err)
	}
	assertMigrationMarkerCount(t, db, "120_agent_provider_binding_recovery_owner.sql", 0)
}

func TestRunMigrationsRealDBUpgradeCanonicalizesProviderBindingUUIDs(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	preUpgradeDir := t.TempDir()
	copyMigrationsBefore120(t, "migrations", preUpgradeDir)
	if err := RunMigrations(ctx, db, preUpgradeDir); err != nil {
		t.Fatalf("RunMigrations(pre-120) error = %v", err)
	}
	mustExec(t, db, `
		INSERT INTO agent_provider_binding (
			agent_id, provider, provider_thread_id, codex_thread_id,
			session_uuid, codex_home
		) VALUES (
			'agent-real-upgrade', 'codex',
			'019E218FB5147733BE85B3EE7F6A78A6', 'public-thread',
			'019E218F-B514-7733-BE85-B3EE7F6A78A7', '/instances/codex-real'
		)
	`)
	if err := RunMigrations(ctx, db, "migrations"); err != nil {
		t.Fatalf("RunMigrations(120) error = %v", err)
	}
	var providerThreadID, sessionUUID, recoveryHome string
	if err := db.QueryRow(`
		SELECT provider_thread_id, session_uuid, provider_recovery_home
		FROM agent_provider_binding WHERE agent_id = 'agent-real-upgrade'
	`).Scan(&providerThreadID, &sessionUUID, &recoveryHome); err != nil {
		t.Fatalf("read real upgraded binding: %v", err)
	}
	if providerThreadID != "019e218f-b514-7733-be85-b3ee7f6a78a6" ||
		sessionUUID != "019e218f-b514-7733-be85-b3ee7f6a78a7" ||
		recoveryHome != "/instances/codex-real" {
		t.Fatalf("real upgraded binding = %q/%q/%q", providerThreadID, sessionUUID, recoveryHome)
	}
}

func copyMigrationsBefore120(t *testing.T, sourceDir, targetDir string) {
	t.Helper()
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("read migration source: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") || name >= "120_" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(targetDir, name), body, 0o600); err != nil {
			t.Fatalf("copy migration %s: %v", name, err)
		}
	}
}

func createProviderBindingUUIDMigrationTable(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		CREATE TABLE agent_provider_binding (
			agent_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			provider_thread_id TEXT NOT NULL DEFAULT '',
			session_uuid TEXT NOT NULL DEFAULT '',
			codex_home TEXT NOT NULL DEFAULT '',
			codex_instance_key TEXT NOT NULL DEFAULT '',
			codex_model_provider TEXT NOT NULL DEFAULT ''
		);
		CREATE UNIQUE INDEX uq_agent_provider_binding_provider_thread
		ON agent_provider_binding(provider, provider_thread_id)
		WHERE provider_thread_id <> '';
		CREATE TRIGGER trg_prevent_agent_provider_binding_rebind
		BEFORE UPDATE ON agent_provider_binding
		FOR EACH ROW
		WHEN OLD.provider_thread_id <> '' AND NEW.provider_thread_id <> OLD.provider_thread_id
		BEGIN
			SELECT RAISE(ABORT, 'agent_provider_binding identity is immutable');
		END
	`)
}

// createThreadTimestampMigrationTables 创建 118 migration 所需的最小持久化结构。
func createThreadTimestampMigrationTables(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		CREATE TABLE agent_threads (
			thread_id TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			finished_at INTEGER
		);
		CREATE TABLE agent_provider_binding (
			agent_id TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
}

// assertThreadTimestampRow 断言 agent_threads 的三个时间字段均保持毫秒契约。
func assertThreadTimestampRow(t *testing.T, db *sql.DB, threadID string, wantCreatedAt, wantUpdatedAt, wantFinishedAt int64) {
	t.Helper()
	var createdAt, updatedAt int64
	var finishedAt sql.NullInt64
	if err := db.QueryRow("SELECT created_at, updated_at, finished_at FROM agent_threads WHERE thread_id = ?", threadID).Scan(&createdAt, &updatedAt, &finishedAt); err != nil {
		t.Fatalf("read thread timestamp row %s: %v", threadID, err)
	}
	if createdAt != wantCreatedAt || updatedAt != wantUpdatedAt || finishedAt.Int64 != wantFinishedAt {
		t.Fatalf("thread timestamp row %s = (%d, %d, %d), want (%d, %d, %d)", threadID, createdAt, updatedAt, finishedAt.Int64, wantCreatedAt, wantUpdatedAt, wantFinishedAt)
	}
}

// assertBindingTimestampRow 断言 agent_provider_binding 时间字段保持毫秒契约。
func assertBindingTimestampRow(t *testing.T, db *sql.DB, agentID string, wantCreatedAt, wantUpdatedAt int64) {
	t.Helper()
	var createdAt, updatedAt int64
	if err := db.QueryRow("SELECT created_at, updated_at FROM agent_provider_binding WHERE agent_id = ?", agentID).Scan(&createdAt, &updatedAt); err != nil {
		t.Fatalf("read binding timestamp row %s: %v", agentID, err)
	}
	if createdAt != wantCreatedAt || updatedAt != wantUpdatedAt {
		t.Fatalf("binding timestamp row %s = (%d, %d), want (%d, %d)", agentID, createdAt, updatedAt, wantCreatedAt, wantUpdatedAt)
	}
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
