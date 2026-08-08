package gate

import (
	"context"
	"database/sql"
	"fmt"
)

// 终态证据采用规范化表，使 SQLite projection 保持唯一查询来源；禁止增加 terminal_evidence_json 列。
const durationLedgerRemoteCITerminalContainersTableSchema = `
CREATE TABLE IF NOT EXISTS ci_shard_terminal_containers (
	job_id TEXT NOT NULL,
	shard_identity TEXT NOT NULL,
	container_kind TEXT NOT NULL CHECK (container_kind IN ('container', 'init')),
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	name TEXT NOT NULL CHECK (length(trim(name)) > 0),
	state TEXT NOT NULL,
	exit_code INTEGER,
	reason TEXT NOT NULL,
	message TEXT NOT NULL,
	PRIMARY KEY (job_id, shard_identity, container_kind, ordinal),
	FOREIGN KEY (job_id, shard_identity)
		REFERENCES ci_shards(job_id, shard_identity) ON DELETE CASCADE
);`

const durationLedgerRemoteCITerminalEventsTableSchema = `
CREATE TABLE IF NOT EXISTS ci_shard_terminal_events (
	job_id TEXT NOT NULL,
	shard_identity TEXT NOT NULL,
	ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
	type TEXT NOT NULL,
	reason TEXT NOT NULL,
	message TEXT NOT NULL,
	count INTEGER NOT NULL CHECK (count >= 0),
	last_timestamp TEXT NOT NULL,
	PRIMARY KEY (job_id, shard_identity, ordinal),
	FOREIGN KEY (job_id, shard_identity)
		REFERENCES ci_shards(job_id, shard_identity) ON DELETE CASCADE
);`

func durationLedgerRemoteCITerminalEvidenceSchemaStatements() []string {
	return []string{
		durationLedgerRemoteCITerminalContainersTableSchema,
		durationLedgerRemoteCITerminalEventsTableSchema,
	}
}

// durationLedgerSQLiteV11SchemaStatements 返回无终态证据时的精确 v11 schema，用于严格迁移预检。
func durationLedgerSQLiteV11SchemaStatements() []string {
	statements := durationLedgerSQLiteLegacySchemaStatements()
	statements = append(statements,
		durationLedgerRawObservationEventsTableSchema,
		durationLedgerRawObservationEventsIndexSchema,
		durationLedgerRawObservationEventsUpdateTriggerSchema,
		durationLedgerRawObservationEventsDeleteTriggerSchema,
	)
	return append(statements, durationLedgerCompileTimingSchemaStatements()...)
}

// migrateDurationLedgerSQLiteV11Schema 只升级结构完全匹配的 v11 authority。
func migrateDurationLedgerSQLiteV11Schema(database *sql.DB, validator *durationLedgerSQLiteSchemaValidator) error {
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite terminal evidence migration connection", err)
	}
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			mapDurationLedgerSQLiteError("begin duration ledger SQLite terminal evidence migration", err))
	}
	if err := migrateDurationLedgerSQLiteV11SchemaOnConnection(connection, validator); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection, err))
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection,
				mapDurationLedgerSQLiteError("commit duration ledger SQLite terminal evidence migration", err)))
	}
	return closeDurationLedgerSQLiteInitializerConnection(connection, nil)
}

// migrateDurationLedgerSQLiteV11SchemaOnConnection 在锁定事务内完成 v11→v12 终态证据迁移并衔接 v13 alias。
func migrateDurationLedgerSQLiteV11SchemaOnConnection(connection *sql.Conn, validator *durationLedgerSQLiteSchemaValidator) error {
	schemaVersion, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if schemaVersion != durationLedgerSQLiteV11SchemaVersion {
		return fmt.Errorf("duration ledger SQLite terminal evidence migration expected schema version %d, got %d", durationLedgerSQLiteV11SchemaVersion, schemaVersion)
	}
	if err := preflightDurationLedgerSQLiteV11OnConnection(connection, schemaVersion); err != nil {
		return err
	}
	for _, statement := range durationLedgerRemoteCITerminalEvidenceSchemaStatements() {
		if _, err := connection.ExecContext(context.Background(), statement); err != nil {
			return mapDurationLedgerSQLiteError("migrate duration ledger SQLite terminal evidence schema", err)
		}
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, durationLedgerSQLitePreviousSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite terminal evidence schema version", err)
	}
	if err := preflightDurationLedgerSQLiteV12OnConnection(connection); err != nil {
		return err
	}
	return migrateDurationLedgerSQLiteWorkloadPassEvidenceAliasSchemaOnConnection(connection, validator)
}

// preflightDurationLedgerSQLiteV11OnConnection 严格确认 v11 旧 authority 形状。
func preflightDurationLedgerSQLiteV11OnConnection(connection *sql.Conn, schemaVersion int) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(connection)
	if err != nil {
		return err
	}
	expected, err := buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteV11SchemaStatements())
	if err != nil {
		return err
	}
	if err := compareDurationLedgerSQLiteSchemaObjects(actual, expected); err != nil {
		return fmt.Errorf("preflight duration ledger SQLite schema version %d: %w", schemaVersion, err)
	}
	return nil
}

// preflightDurationLedgerSQLiteV12OnConnection 严格确认终态证据补齐后的 v12 形状。
func preflightDurationLedgerSQLiteV12OnConnection(connection *sql.Conn) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(connection)
	if err != nil {
		return err
	}
	expected, err := buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteV12SchemaStatements())
	if err != nil {
		return err
	}
	if err := compareDurationLedgerSQLiteSchemaObjects(actual, expected); err != nil {
		return fmt.Errorf("preflight duration ledger SQLite schema version %d: %w", durationLedgerSQLitePreviousSchemaVersion, err)
	}
	return nil
}
