package gate

import (
	"context"
	"database/sql"
	"fmt"
)

const durationLedgerCompileTimingTableSchema = `
CREATE TABLE IF NOT EXISTS ci_compile_timing_observations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id TEXT NOT NULL REFERENCES ci_runs(job_id) ON DELETE CASCADE,
	package_target TEXT NOT NULL CHECK (length(trim(package_target)) > 0),
	semantic_key TEXT NOT NULL CHECK (length(trim(semantic_key)) > 0),
	platform TEXT NOT NULL CHECK (length(trim(platform)) > 0),
	runner_identity_digest TEXT NOT NULL CHECK (length(trim(runner_identity_digest)) > 0),
	toolchain_digest TEXT NOT NULL CHECK (length(trim(toolchain_digest)) > 0),
	execution_mode TEXT NOT NULL CHECK (execution_mode IN ('normal', 'calibration')),
	resource_class_id TEXT NOT NULL CHECK (length(trim(resource_class_id)) > 0),
	resource_cpu REAL NOT NULL CHECK (resource_cpu > 0),
	resource_memory_gib REAL NOT NULL CHECK (resource_memory_gib > 0),
	duration_ms INTEGER NOT NULL CHECK (duration_ms > 0),
	started_at_unix_ms INTEGER NOT NULL CHECK (started_at_unix_ms > 0),
	completed_at_unix_ms INTEGER NOT NULL CHECK (completed_at_unix_ms > started_at_unix_ms),
	measurement TEXT NOT NULL CHECK (measurement = 'measured'),
	aggregation TEXT NOT NULL CHECK (aggregation = 'raw'),
	UNIQUE (job_id, package_target, semantic_key, platform, runner_identity_digest, toolchain_digest,
		execution_mode, resource_class_id, resource_cpu, resource_memory_gib, started_at_unix_ms, completed_at_unix_ms)
);`

const durationLedgerCompileTimingLookupIndexSchema = `
CREATE INDEX IF NOT EXISTS idx_ci_compile_timing_lookup
	ON ci_compile_timing_observations (
		package_target, semantic_key, platform, runner_identity_digest,
		toolchain_digest, execution_mode, resource_class_id, resource_cpu,
		resource_memory_gib, job_id
	);`

const durationLedgerCompileTimingJobIndexSchema = `
CREATE INDEX IF NOT EXISTS idx_ci_compile_timing_job
	ON ci_compile_timing_observations (job_id, id);`

func durationLedgerCompileTimingSchemaStatements() []string {
	return []string{
		durationLedgerCompileTimingTableSchema,
		durationLedgerCompileTimingLookupIndexSchema,
		durationLedgerCompileTimingJobIndexSchema,
	}
}

// durationLedgerSQLiteV10SchemaStatements 重建迁移预检所需的 v10 原始结构。
// 它故意不包含新表和索引，畸形 v10 authority 不得被部分升级。
func durationLedgerSQLiteV10SchemaStatements() []string {
	statements := durationLedgerSQLiteLegacySchemaStatements()
	return append(statements,
		durationLedgerRawObservationEventsTableSchema,
		durationLedgerRawObservationEventsIndexSchema,
		durationLedgerRawObservationEventsUpdateTriggerSchema,
		durationLedgerRawObservationEventsDeleteTriggerSchema,
	)
}

// preflightDurationLedgerSQLiteV10Schema 在执行 DDL 前严格比对 v10 结构。
func preflightDurationLedgerSQLiteV10Schema(queryer durationLedgerSQLiteSchemaQueryer) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(queryer)
	if err != nil {
		return err
	}
	expected, err := buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteV10SchemaStatements())
	if err != nil {
		return err
	}
	return compareDurationLedgerSQLiteSchemaObjects(actual, expected)
}

// migrateDurationLedgerSQLiteV10Schema 在独立写事务中把 v10 依次升级到 v11 和当前版本。
func migrateDurationLedgerSQLiteV10Schema(database *sql.DB, validator *durationLedgerSQLiteSchemaValidator) error {
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite compile timing migration connection", err)
	}
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			mapDurationLedgerSQLiteError("begin duration ledger SQLite compile timing migration", err))
	}
	if err := migrateDurationLedgerSQLiteV10SchemaOnConnection(connection, validator); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection, err))
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection,
				mapDurationLedgerSQLiteError("commit duration ledger SQLite compile timing migration", err)))
	}
	return closeDurationLedgerSQLiteInitializerConnection(connection, nil)
}

// migrateDurationLedgerSQLiteV10SchemaOnConnection 在已有 BEGIN IMMEDIATE
// 事务内完成 v10 到 v11，再补齐当前终态证据结构。
func migrateDurationLedgerSQLiteV10SchemaOnConnection(connection *sql.Conn, validator *durationLedgerSQLiteSchemaValidator) error {
	schemaVersion, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if schemaVersion != durationLedgerSQLiteCompileTimingVersion {
		return fmt.Errorf("duration ledger SQLite compile timing migration expected schema version %d, got %d", durationLedgerSQLiteCompileTimingVersion, schemaVersion)
	}
	if err := preflightDurationLedgerSQLiteV10Schema(connection); err != nil {
		return fmt.Errorf("preflight duration ledger SQLite schema version %d: %w", durationLedgerSQLiteCompileTimingVersion, err)
	}
	for _, statement := range durationLedgerCompileTimingSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite compile timing schema", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, durationLedgerSQLiteV11SchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite compile timing schema version", err)
	}
	actual, err := loadDurationLedgerSQLiteSchemaObjects(connection)
	if err != nil {
		return err
	}
	expected, err := buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteV11SchemaStatements())
	if err != nil {
		return err
	}
	if err := compareDurationLedgerSQLiteSchemaObjects(actual, expected); err != nil {
		return fmt.Errorf("preflight duration ledger SQLite schema version %d: %w", durationLedgerSQLiteV11SchemaVersion, err)
	}
	return migrateDurationLedgerSQLiteV11SchemaOnConnection(connection, validator)
}
