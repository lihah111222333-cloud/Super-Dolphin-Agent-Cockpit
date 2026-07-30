package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func TestRunMigrationsFreshDatabaseReachesProviderRecoveryVersion(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)

	if err := RunMigrations(ctx, db, "migrations"); err != nil {
		t.Fatalf("RunMigrations(fresh to 123) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 123)
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 1)

	if err := RunMigrations(ctx, db, "migrations"); err != nil {
		t.Fatalf("RunMigrations(fresh repeat) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 123)
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 1)
}

func TestRunMigrationsProviderRecoveryVersion123FollowsLegacy120Marker(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	preUpgradeDir := t.TempDir()
	copyBranchLocalMigrationsBefore120(t, "migrations", preUpgradeDir)
	if err := RunMigrations(ctx, db, preUpgradeDir); err != nil {
		t.Fatalf("RunMigrations(pre-123) error = %v", err)
	}
	mustExec(t, db, `
		ALTER TABLE agent_provider_binding
		ADD COLUMN provider_recovery_home TEXT NOT NULL DEFAULT ''
	`)
	mustExec(t, db, "DROP TRIGGER IF EXISTS trg_prevent_agent_provider_binding_rebind")
	mustExec(t, db, agentProviderBindingIdentityTriggerSQL)
	mustExec(t, db, `
		INSERT INTO schema_migrations(version, name, filename, applied_at)
		VALUES (
			120,
			'120_agent_provider_binding_recovery_owner',
			'120_agent_provider_binding_recovery_owner.sql',
			4800123
		)
	`)

	if err := RunMigrations(ctx, db, "migrations"); err != nil {
		t.Fatalf("RunMigrations(legacy 120 marker to 123) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 123)
	assertMigrationMarkerCount(t, db, "120_agent_provider_binding_recovery_owner.sql", 0)
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 1)
	assertMigrationAppliedAt(t, db, "123_agent_provider_binding_recovery_owner.sql", 4800123)
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

// TestRunMigrationsAddsTerminalOutcomeTransactionTables 锁定 v2 public outcome/CAS/outbox 的 additive migration。
func TestRunMigrationsAddsTerminalOutcomeTransactionTables(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "120_terminal_outcome_outbox.sql", readMigrationTestFile(t, "120_terminal_outcome_outbox.sql"))

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	tables := sqliteTables(t, db)
	for _, table := range []string{"terminal_outcome_heads", "public_terminal_outcomes", "terminal_outcome_outbox"} {
		if !tables[table] {
			t.Fatalf("%s table missing after migration 120", table)
		}
	}
	assertMigrationMarkerCount(t, db, "120_terminal_outcome_outbox.sql", 1)
	assertIndex(t, db, "terminal_outcome_outbox", "idx_terminal_outcome_outbox_claim", false, "status IN ('pending', 'claimed')")
}

// TestTerminalOutcomeCurrentHeadMigrationUpgradesV120AndBlocksLegacyWriter 锁定 mixed-version 旧写端 fail-fast。
func TestTerminalOutcomeCurrentHeadMigrationUpgradesV120AndBlocksLegacyWriter(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	dir120 := t.TempDir()
	writeMigrationTestFile(t, dir120, "120_terminal_outcome_outbox.sql", readMigrationTestFile(t, "120_terminal_outcome_outbox.sql"))
	if err := RunMigrations(ctx, db, dir120); err != nil {
		t.Fatalf("apply v120: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO terminal_outcome_heads VALUES
		  ('agent-1','terminal_outcome_commit_v2','thread-1','turn-1','session-1',7,'event-1','identity-1','turn_running','terminal',1000);
		INSERT INTO public_terminal_outcomes VALUES
		  ('agent-1',2,'turn_completed','thread-1','turn-1','session-1',7,'event-1','identity-1',
		   '{"kind":"success","code":"success","summary":"safe","completedAt":"2026-07-29T00:00:01Z"}','safe',1000);
		INSERT INTO terminal_outcome_outbox(event_id,payload_json,status,claimed_by,claimed_at,created_at)
		  VALUES ('event-1','{"schemaVersion":2,"projectionKind":"turn_completed","identity":{"capability":"terminal_outcome_commit_v2","agentId":"agent-1","publicThreadId":"thread-1","providerTurnId":"turn-1","sessionId":"session-1","generation":7,"eventId":"event-1","terminalIdentity":"identity-1","expectedActiveState":"turn_running"},"publicOutcome":{"kind":"success","code":"success","summary":"safe","completedAt":"2026-07-29T00:00:01Z"},"publicReport":"safe","occurredAt":"2026-07-29T00:00:01Z"}','claimed','old-worker',999,1000);
	`); err != nil {
		t.Fatalf("seed v120 terminal rows: %v", err)
	}
	dir121 := t.TempDir()
	writeMigrationTestFile(t, dir121, "121_terminal_outcome_current_head.sql", readMigrationTestFile(t, "121_terminal_outcome_current_head.sql"))
	if err := RunMigrations(ctx, db, dir121); err != nil {
		t.Fatalf("apply v121: %v", err)
	}
	for _, table := range []string{"terminal_outcome_current_heads", "public_terminal_outcome_history", "terminal_outcome_private_dag_payloads", "terminal_outcome_outbox_v2"} {
		if !sqliteTables(t, db)[table] {
			t.Fatalf("%s missing after v121", table)
		}
	}
	var status, worker string
	if err := db.QueryRow("SELECT status, claimed_by FROM terminal_outcome_outbox_v2 WHERE event_id='event-1'").Scan(&status, &worker); err != nil {
		t.Fatalf("read migrated outbox: %v", err)
	}
	if status != "pending" || worker != "" {
		t.Fatalf("migrated claimed outbox = status:%q worker:%q, want pending/unowned", status, worker)
	}
	var headVersion int
	if err := db.QueryRow("SELECT json_extract(public_payload_json, '$.identity.headVersion') FROM terminal_outcome_outbox_v2 WHERE event_id='event-1'").Scan(&headVersion); err != nil {
		t.Fatalf("read migrated payload headVersion: %v", err)
	}
	if headVersion != 1 {
		t.Fatalf("migrated payload headVersion = %d, want 1 compatibility adapter", headVersion)
	}
	for _, legacyObject := range []string{"terminal_outcome_heads", "public_terminal_outcomes", "terminal_outcome_outbox"} {
		var objectType string
		if err := db.QueryRow("SELECT type FROM sqlite_master WHERE name = ?", legacyObject).Scan(&objectType); err != nil {
			t.Fatalf("read legacy protocol object %s: %v", legacyObject, err)
		}
		if objectType == "table" {
			t.Fatalf("legacy protocol object %s remains writable table", legacyObject)
		}
	}
	if err := verifyLegacyTerminalWriterCanStart(db); err == nil || !strings.Contains(err.Error(), "requires writable table") {
		t.Fatalf("legacy binary startup check = %v, want protocol fail-fast", err)
	}
	if _, err := db.Exec(`
		INSERT INTO terminal_outcome_heads VALUES
		  ('legacy-agent','terminal_outcome_commit_v2','legacy-thread','legacy-turn','legacy-session',8,
		   'legacy-event','legacy-identity','turn_running','terminal',2000)
	`); err == nil || !strings.Contains(err.Error(), "terminal outcome protocol v121") {
		t.Fatalf("legacy writer error = %v, want v121 protocol fail-fast", err)
	}
}

func TestTerminalOutcomeV121ForwardOnlyBackupRestoreContract(t *testing.T) {
	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "terminal-v120.db")
	source, err := sql.Open(driverName, sourcePath)
	if err != nil {
		t.Fatalf("open v120 source: %v", err)
	}
	createMigrationMarkerTable(t, source)
	markBaselineApplied(t, source)
	dir120 := t.TempDir()
	writeMigrationTestFile(t, dir120, "120_terminal_outcome_outbox.sql", readMigrationTestFile(t, "120_terminal_outcome_outbox.sql"))
	if err := RunMigrations(ctx, source, dir120); err != nil {
		t.Fatalf("apply v120: %v", err)
	}
	if err := verifyLegacyTerminalWriterCanStart(source); err != nil {
		t.Fatalf("v120 compatibility preflight: %v", err)
	}
	mustExec(t, source, `
		INSERT INTO terminal_outcome_heads VALUES
		  ('backup-agent','terminal_outcome_commit_v2','backup-thread','backup-turn','backup-session',7,
		   'backup-event','backup-identity','turn_running','terminal',1000)
	`)
	assertSQLiteIntegrityCheck(t, source)
	checkpointSQLiteTruncate(t, source)
	if err := source.Close(); err != nil {
		t.Fatalf("close quiesced v120 source: %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "terminal-v120.backup.db")
	copySQLiteFile(t, sourcePath, backupPath)
	backup, err := sql.Open(driverName, backupPath)
	if err != nil {
		t.Fatalf("open v120 backup for verification: %v", err)
	}
	assertSQLiteIntegrityCheck(t, backup)
	var backupVersion int
	if err := backup.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&backupVersion); err != nil {
		t.Fatalf("read backup schema version: %v", err)
	}
	if backupVersion != 120 {
		t.Fatalf("backup schema version = %d, want 120", backupVersion)
	}
	if err := backup.Close(); err != nil {
		t.Fatalf("close verified v120 backup: %v", err)
	}

	upgraded, err := sql.Open(driverName, sourcePath)
	if err != nil {
		t.Fatalf("reopen source for v121 cutover: %v", err)
	}
	dir121 := t.TempDir()
	writeMigrationTestFile(t, dir121, "121_terminal_outcome_current_head.sql", readMigrationTestFile(t, "121_terminal_outcome_current_head.sql"))
	if err := RunMigrations(ctx, upgraded, dir121); err != nil {
		t.Fatalf("apply v121: %v", err)
	}
	mustExec(t, upgraded, `
		INSERT INTO terminal_outcome_current_heads (
			agent_id, capability, public_thread_id, provider_turn_id, session_id, generation,
			expected_active_state, version, state, terminal_event_id, terminal_identity,
			activated_at, updated_at
		) VALUES (
			'v121-agent','terminal_outcome_commit_v2','v121-thread','v121-turn','v121-session',8,
			'turn_running',1,'active','','',2000,2000
		)
	`)
	if err := verifyLegacyTerminalWriterCanStart(upgraded); err == nil {
		t.Fatal("legacy writer accepted v121 schema after new semantic write")
	}
	if err := upgraded.Close(); err != nil {
		t.Fatalf("close upgraded source: %v", err)
	}

	restoredPath := filepath.Join(t.TempDir(), "terminal-restored-v120.db")
	copySQLiteFile(t, backupPath, restoredPath)
	restored, err := sql.Open(driverName, restoredPath)
	if err != nil {
		t.Fatalf("open restored v120 database: %v", err)
	}
	defer restored.Close()
	assertSQLiteIntegrityCheck(t, restored)
	if err := verifyLegacyTerminalWriterCanStart(restored); err != nil {
		t.Fatalf("restored v120 compatibility preflight: %v", err)
	}
	mustExec(t, restored, `
		INSERT INTO terminal_outcome_heads VALUES
		  ('restored-agent','terminal_outcome_commit_v2','restored-thread','restored-turn','restored-session',9,
		   'restored-event','restored-identity','turn_running','terminal',3000)
	`)
	var restoredState string
	if err := restored.QueryRow(`
		SELECT state FROM terminal_outcome_heads WHERE agent_id = 'restored-agent'
	`).Scan(&restoredState); err != nil {
		t.Fatalf("read restored v120 write: %v", err)
	}
	if restoredState != "terminal" {
		t.Fatalf("restored v120 state = %q, want terminal", restoredState)
	}
	var restoredVersion int
	if err := restored.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&restoredVersion); err != nil {
		t.Fatalf("read restored schema version: %v", err)
	}
	if restoredVersion != 120 {
		t.Fatalf("restored schema version = %d, want 120", restoredVersion)
	}
	var v121Rows int
	if err := restored.QueryRow(`
		SELECT COUNT(*) FROM terminal_outcome_heads WHERE agent_id = 'v121-agent'
	`).Scan(&v121Rows); err != nil {
		t.Fatalf("check v121 semantic isolation: %v", err)
	}
	if v121Rows != 0 {
		t.Fatalf("restored v120 contains %d v121 semantic rows, want 0", v121Rows)
	}
}

// verifyLegacyTerminalWriterCanStart 模拟 v120 binary 对三张可写表的启动前置检查。
func verifyLegacyTerminalWriterCanStart(db *sql.DB) error {
	for _, name := range []string{"terminal_outcome_heads", "public_terminal_outcomes", "terminal_outcome_outbox"} {
		var objectType string
		if err := db.QueryRow("SELECT type FROM sqlite_master WHERE name = ?", name).Scan(&objectType); err != nil {
			return err
		}
		if objectType != "table" {
			return fmt.Errorf("legacy terminal writer requires writable table %s, got %s", name, objectType)
		}
	}
	return nil
}

// TestTerminalOutcomeMigrationPreservesLegacyRows 锁定 v2 rollout 只新增表，不改写旧 provider/DB 数据。
func TestTerminalOutcomeMigrationPreservesLegacyRows(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	if _, err := db.Exec(`
		CREATE TABLE legacy_provider_terminal (event_id TEXT PRIMARY KEY, report TEXT NOT NULL);
		INSERT INTO legacy_provider_terminal(event_id, report) VALUES ('legacy-event', 'legacy-report');
	`); err != nil {
		t.Fatalf("seed legacy provider terminal row: %v", err)
	}
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "120_terminal_outcome_outbox.sql", readMigrationTestFile(t, "120_terminal_outcome_outbox.sql"))

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	var report string
	if err := db.QueryRow("SELECT report FROM legacy_provider_terminal WHERE event_id = 'legacy-event'").Scan(&report); err != nil {
		t.Fatalf("query preserved legacy row: %v", err)
	}
	if report != "legacy-report" {
		t.Fatalf("legacy report = %q, want unchanged", report)
	}
}

// TestTerminalOutcomeMigrationRollbackLeavesNoPartialV2Tables 锁定 rollout 失败可回滚且不留半套 schema。
func TestTerminalOutcomeMigrationRollbackLeavesNoPartialV2Tables(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	if _, err := db.Exec("CREATE TABLE terminal_outcome_outbox (broken TEXT)"); err != nil {
		t.Fatalf("seed conflicting outbox table: %v", err)
	}
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "120_terminal_outcome_outbox.sql", readMigrationTestFile(t, "120_terminal_outcome_outbox.sql"))

	if err := RunMigrations(ctx, db, dir); err == nil {
		t.Fatal("RunMigrations() error = nil, want conflicting schema failure")
	}
	tables := sqliteTables(t, db)
	for _, table := range []string{"terminal_outcome_heads", "public_terminal_outcomes"} {
		if tables[table] {
			t.Fatalf("%s survived failed migration transaction", table)
		}
	}
	assertMigrationMarkerCount(t, db, "120_terminal_outcome_outbox.sql", 0)
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

// TestMCPManagedGenerationTablesComeFromMigration 验证 generation owner 只由规范 migration 122 建表。
func TestMCPManagedGenerationTablesComeFromMigration(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	dir := t.TempDir()
	const migration = "122_mcp_managed_generations.sql"
	writeMigrationTestFile(t, dir, migration, readMigrationTestFile(t, migration))

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations(repeated) error = %v", err)
	}
	tables := sqliteTables(t, db)
	for _, table := range []string{
		"mcp_managed_generation_owner",
		"mcp_managed_generation_instances",
		"mcp_managed_generations",
	} {
		if !tables[table] {
			t.Fatalf("%s table missing after migration 122", table)
		}
	}
	assertMigrationMarkerCount(t, db, migration, 1)
	assertMigrationVersion(t, db, migration, 122)
	assertPrimaryKey(t, db, "mcp_managed_generation_owner", []string{"singleton_id"})
	assertPrimaryKey(t, db, "mcp_managed_generation_instances", []string{"instance_id"})
	assertPrimaryKey(t, db, "mcp_managed_generations", []string{"instance_id"})
	assertNotNullColumns(t, db, "mcp_managed_generation_owner", []string{
		"owner_epoch",
		"marker_initialized",
		"ledger_initialized",
	})
	assertNotNullColumns(t, db, "mcp_managed_generations", []string{
		"generation",
		"claim_id",
		"external_committed",
	})
}

// TestMCPManagedGenerationFreshDatabaseReachesMigration122 锁定 fresh DB 的 0→122 完整升级路径。
func TestMCPManagedGenerationFreshDatabaseReachesMigration122(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := RunMigrations(context.Background(), db, sqliteMigrationsDir(t)); err != nil {
		t.Fatalf("RunMigrations(fresh 0→122) error = %v", err)
	}
	assertMigrationMarkerCount(t, db, "122_mcp_managed_generations.sql", 1)
	assertMigrationVersion(t, db, "122_mcp_managed_generations.sql", 122)
}

// TestMCPManagedGenerationLegacy120MarkerAdoptsCanonical122 验证旧文件名已执行时只登记规范 marker，并保留 durable generation 数据。
func TestMCPManagedGenerationLegacy120MarkerAdoptsCanonical122(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	setupLegacyManagedGenerationMigration(t, db)
	dir := managedGenerationMigrationDir(t)

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations(legacy 120→122) error = %v", err)
	}
	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations(legacy repeated) error = %v", err)
	}
	assertMigrationMarkerCount(t, db, "120_mcp_managed_generations.sql", 1)
	assertMigrationMarkerCount(t, db, "122_mcp_managed_generations.sql", 1)
	assertMigrationVersion(t, db, "122_mcp_managed_generations.sql", 122)
	assertManagedGenerationRow(t, db, "legacy-instance", 7, 1)
}

func TestMCPManagedGenerationLegacy120MarkerRejectsDriftWithoutSideEffects(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*testing.T, *sql.DB)
		wantError   string
		assertDrift func(*testing.T, *sql.DB)
	}{
		{
			name: "partial table",
			mutate: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, "DROP TABLE mcp_managed_generations")
			},
			wantError: "table mcp_managed_generations is missing",
			assertDrift: func(t *testing.T, db *sql.DB) {
				assertMigrationTableMissing(t, db, "mcp_managed_generations")
				assertManagedGenerationInstanceCount(t, db, "legacy-instance", 1)
			},
		},
		{
			name: "schema drift",
			mutate: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, "ALTER TABLE mcp_managed_generation_instances ADD COLUMN unexpected TEXT")
			},
			wantError: "table mcp_managed_generation_instances definition does not match",
			assertDrift: func(t *testing.T, db *sql.DB) {
				assertManagedGenerationRow(t, db, "legacy-instance", 7, 1)
			},
		},
		{
			name: "constraint drift",
			mutate: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, `
					ALTER TABLE mcp_managed_generations RENAME TO mcp_managed_generations_old;
					CREATE TABLE mcp_managed_generations (
						instance_id TEXT PRIMARY KEY,
						generation INTEGER NOT NULL,
						claim_id TEXT NOT NULL,
						external_committed INTEGER NOT NULL DEFAULT 0
					);
					INSERT INTO mcp_managed_generations
					SELECT * FROM mcp_managed_generations_old;
					DROP TABLE mcp_managed_generations_old
				`)
			},
			wantError: "table mcp_managed_generations definition does not match",
			assertDrift: func(t *testing.T, db *sql.DB) {
				assertManagedGenerationRow(t, db, "legacy-instance", 7, 1)
			},
		},
		{
			name: "singleton identity mismatch",
			mutate: func(t *testing.T, db *sql.DB) {
				mustExec(t, db, `
					PRAGMA ignore_check_constraints = ON;
					UPDATE mcp_managed_generation_owner SET singleton_id = 2;
					PRAGMA ignore_check_constraints = OFF
				`)
			},
			wantError: "owner identity is invalid",
			assertDrift: func(t *testing.T, db *sql.DB) {
				assertManagedGenerationOwnerID(t, db, 2)
				assertManagedGenerationRow(t, db, "legacy-instance", 7, 1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigrationTestDB(t)
			setupLegacyManagedGenerationMigration(t, db)
			test.mutate(t, db)

			err := RunMigrations(context.Background(), db, managedGenerationMigrationDir(t))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("RunMigrations() error = %v, want %q", err, test.wantError)
			}
			assertMigrationMarkerCount(t, db, "120_mcp_managed_generations.sql", 1)
			assertMigrationMarkerCount(t, db, "122_mcp_managed_generations.sql", 0)
			test.assertDrift(t, db)
		})
	}
}

func TestMCPManagedGenerationLegacy120MarkerRollsBackCanonicalMarkerConflict(t *testing.T) {
	db := openMigrationTestDB(t)
	setupLegacyManagedGenerationMigration(t, db)
	mustExec(t, db, `
		INSERT INTO schema_migrations(version, name, filename, applied_at)
		VALUES (122, 'conflicting migration', '122_conflicting.sql', 2)
	`)

	err := RunMigrations(context.Background(), db, managedGenerationMigrationDir(t))
	if err == nil || !strings.Contains(err.Error(), "canonical migration target 122_mcp_managed_generations.sql has a marker collision") {
		t.Fatalf("RunMigrations() error = %v, want canonical marker conflict", err)
	}
	assertMigrationMarkerCount(t, db, "120_mcp_managed_generations.sql", 1)
	assertMigrationMarkerCount(t, db, "122_mcp_managed_generations.sql", 0)
	assertMigrationMarkerCount(t, db, "122_conflicting.sql", 1)
	assertManagedGenerationRow(t, db, "legacy-instance", 7, 1)
}

func TestMCPManagedGenerationLegacy120MarkerRejectsForgedIdentity(t *testing.T) {
	db := openMigrationTestDB(t)
	setupLegacyManagedGenerationMigration(t, db)
	mustExec(t, db, `
		UPDATE schema_migrations
		SET name = 'forged'
		WHERE filename = '120_mcp_managed_generations.sql'
	`)

	err := RunMigrations(context.Background(), db, managedGenerationMigrationDir(t))
	if err == nil || !strings.Contains(err.Error(), "legacy managed generation marker identity is invalid") {
		t.Fatalf("RunMigrations() error = %v, want legacy marker identity rejection", err)
	}
	assertMigrationMarkerCount(t, db, "120_mcp_managed_generations.sql", 1)
	assertMigrationMarkerCount(t, db, "122_mcp_managed_generations.sql", 0)
	assertManagedGenerationRow(t, db, "legacy-instance", 7, 1)
}

// TestMCPManagedGenerationLegacyValidatorConsumesCanonicalDDL 动态修改真实 migration 约束，证明兼容校验读取规范 SQL 而非测试侧字段清单。
func TestMCPManagedGenerationLegacyValidatorConsumesCanonicalDDL(t *testing.T) {
	db := openMigrationTestDB(t)
	setupLegacyManagedGenerationMigration(t, db)
	body := readMigrationTestFile(t, "122_mcp_managed_generations.sql")
	mutated := strings.Replace(body, "CHECK (generation > 0)", "CHECK (generation >= 0)", 1)
	if mutated == body {
		t.Fatal("managed generation constraint mutation did not change canonical SQL")
	}
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "122_mcp_managed_generations.sql", mutated)

	err := RunMigrations(context.Background(), db, dir)
	if err == nil || !strings.Contains(err.Error(), "table mcp_managed_generations definition does not match") {
		t.Fatalf("RunMigrations() error = %v, want canonical constraint mismatch", err)
	}
	assertMigrationMarkerCount(t, db, "122_mcp_managed_generations.sql", 0)
	assertManagedGenerationRow(t, db, "legacy-instance", 7, 1)
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
	writeMigrationTestFile(t, dir, "123_agent_provider_binding_recovery_owner.sql", "SELECT 1;\n")

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations(repeat) error = %v", err)
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
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 1)
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
	writeMigrationTestFile(t, dir, "123_agent_provider_binding_recovery_owner.sql", "SELECT 1;\n")
	before := providerBindingMigrationSnapshot(t, db)

	err := RunMigrations(ctx, db, dir)
	if err == nil || !strings.Contains(err.Error(), "canonical UUID collision") {
		t.Fatalf("RunMigrations() error = %v, want canonical UUID collision", err)
	}
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 0)
	assertProviderBindingMigrationRollback(t, db, before)
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
	writeMigrationTestFile(t, dir, "123_agent_provider_binding_recovery_owner.sql", "SELECT 1;\n")
	before := providerBindingMigrationSnapshot(t, db)

	err := RunMigrations(ctx, db, dir)
	if err == nil || !strings.Contains(err.Error(), "provider_thread_id") {
		t.Fatalf("RunMigrations() error = %v, want invalid provider_thread_id rejection", err)
	}
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 0)
	assertProviderBindingMigrationRollback(t, db, before)
}

func TestRunMigrationsRealDBUpgradeCanonicalizesProviderBindingUUIDs(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	preUpgradeDir := t.TempDir()
	copyBranchLocalMigrationsBefore120(t, "migrations", preUpgradeDir)
	if err := RunMigrations(ctx, db, preUpgradeDir); err != nil {
		t.Fatalf("RunMigrations(pre-123) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 119)
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
		t.Fatalf("RunMigrations(123) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 123)
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 1)
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

func providerBindingMigrationSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var snapshot []string
	schemaRows, err := db.Query(`
		SELECT type, name, tbl_name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		t.Fatalf("read migration schema snapshot: %v", err)
	}
	for schemaRows.Next() {
		var objectType, name, tableName, sqlText string
		if err := schemaRows.Scan(&objectType, &name, &tableName, &sqlText); err != nil {
			schemaRows.Close()
			t.Fatalf("scan migration schema snapshot: %v", err)
		}
		snapshot = append(snapshot, fmt.Sprintf("schema|%s|%s|%s|%s", objectType, name, tableName, sqlText))
	}
	if err := schemaRows.Err(); err != nil {
		schemaRows.Close()
		t.Fatalf("iterate migration schema snapshot: %v", err)
	}
	if err := schemaRows.Close(); err != nil {
		t.Fatalf("close migration schema snapshot: %v", err)
	}
	appendProviderBindingRowsSnapshot(t, db, &snapshot)
	appendMigrationMarkerRowsSnapshot(t, db, &snapshot)
	return snapshot
}

func appendProviderBindingRowsSnapshot(t *testing.T, db *sql.DB, snapshot *[]string) {
	t.Helper()
	rows, err := db.Query(`
		SELECT agent_id, provider, provider_thread_id, session_uuid,
		       codex_home, codex_instance_key, codex_model_provider
		FROM agent_provider_binding
		ORDER BY agent_id
	`)
	if err != nil {
		t.Fatalf("read provider binding row snapshot: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var values [7]string
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6]); err != nil {
			t.Fatalf("scan provider binding row snapshot: %v", err)
		}
		*snapshot = append(*snapshot, "binding|"+strings.Join(values[:], "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate provider binding row snapshot: %v", err)
	}
}

func appendMigrationMarkerRowsSnapshot(t *testing.T, db *sql.DB, snapshot *[]string) {
	t.Helper()
	rows, err := db.Query(`
		SELECT version, name, filename, applied_at
		FROM schema_migrations
		ORDER BY version
	`)
	if err != nil {
		t.Fatalf("read migration marker snapshot: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version, appliedAt int64
		var name, filename string
		if err := rows.Scan(&version, &name, &filename, &appliedAt); err != nil {
			t.Fatalf("scan migration marker snapshot: %v", err)
		}
		*snapshot = append(*snapshot, fmt.Sprintf("marker|%d|%s|%s|%d", version, name, filename, appliedAt))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration marker snapshot: %v", err)
	}
}

func assertProviderBindingMigrationRollback(t *testing.T, db *sql.DB, before []string) {
	t.Helper()
	after := providerBindingMigrationSnapshot(t, db)
	if !slices.Equal(after, before) {
		t.Fatalf("migration rollback snapshot changed\nbefore=%q\nafter=%q", before, after)
	}
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

func assertMigrationVersion(t *testing.T, db *sql.DB, filename string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT version FROM schema_migrations WHERE filename = ?", filename).Scan(&got); err != nil {
		t.Fatalf("read migration version for %s: %v", filename, err)
	}
	if got != want {
		t.Fatalf("migration %s version = %d, want %d", filename, got, want)
	}
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

func assertManagedGenerationRow(t *testing.T, db *sql.DB, instanceID string, wantGeneration, wantExternalCommitted int) {
	t.Helper()
	var generation, externalCommitted int
	if err := db.QueryRow(
		"SELECT generation, external_committed FROM mcp_managed_generations WHERE instance_id = ?",
		instanceID,
	).Scan(&generation, &externalCommitted); err != nil {
		t.Fatalf("read managed generation %s: %v", instanceID, err)
	}
	if generation != wantGeneration || externalCommitted != wantExternalCommitted {
		t.Fatalf(
			"managed generation %s = (generation=%d, external_committed=%d), want (%d, %d)",
			instanceID,
			generation,
			externalCommitted,
			wantGeneration,
			wantExternalCommitted,
		)
	}
}

func setupLegacyManagedGenerationMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	createMigrationMarkerTable(t, db)
	markBaselineApplied(t, db)
	mustExec(t, db, readMigrationTestFile(t, "122_mcp_managed_generations.sql"))
	mustExec(t, db, `
		INSERT INTO schema_migrations(version, name, filename, applied_at)
		VALUES (120, '120_mcp_managed_generations', '120_mcp_managed_generations.sql', 1);
		INSERT INTO mcp_managed_generation_instances(instance_id) VALUES ('legacy-instance');
		INSERT INTO mcp_managed_generations(instance_id, generation, claim_id, external_committed)
		VALUES ('legacy-instance', 7, 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 1)
	`)
}

func managedGenerationMigrationDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const migration = "122_mcp_managed_generations.sql"
	writeMigrationTestFile(t, dir, migration, readMigrationTestFile(t, migration))
	return dir
}

func assertManagedGenerationInstanceCount(t *testing.T, db *sql.DB, instanceID string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM mcp_managed_generation_instances WHERE instance_id = ?",
		instanceID,
	).Scan(&got); err != nil {
		t.Fatalf("count managed generation instance %s: %v", instanceID, err)
	}
	if got != want {
		t.Fatalf("managed generation instance %s count = %d, want %d", instanceID, got, want)
	}
}

func assertManagedGenerationOwnerID(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT singleton_id FROM mcp_managed_generation_owner").Scan(&got); err != nil {
		t.Fatalf("read managed generation owner identity: %v", err)
	}
	if got != want {
		t.Fatalf("managed generation owner singleton_id = %d, want %d", got, want)
	}
}

func assertMaxMigrationVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&got); err != nil {
		t.Fatalf("read max migration version: %v", err)
	}
	if got != want {
		t.Fatalf("max migration version = %d, want %d", got, want)
	}
}
