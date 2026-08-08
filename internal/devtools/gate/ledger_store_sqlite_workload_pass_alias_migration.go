package gate

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateDurationLedgerSQLiteWorkloadPassEvidenceAliasSchema 将 v12 authority
// 升级为仅新增规范化 alias relation；DDL 前严格预检 v12 物理形状，不改写历史行。
func migrateDurationLedgerSQLiteWorkloadPassEvidenceAliasSchema(
	database *sql.DB,
	validator *durationLedgerSQLiteSchemaValidator,
) error {
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger SQLite workload evidence alias migration connection", err)
	}
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			mapDurationLedgerSQLiteError("begin duration ledger SQLite workload evidence alias migration", err))
	}
	if err := migrateDurationLedgerSQLiteWorkloadPassEvidenceAliasSchemaOnConnection(connection, validator); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection, err))
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection,
				mapDurationLedgerSQLiteError("commit duration ledger SQLite workload evidence alias migration", err)))
	}
	return closeDurationLedgerSQLiteInitializerConnection(connection, nil)
}

// migrateDurationLedgerSQLiteWorkloadPassEvidenceAliasSchemaOnConnection 在已锁定事务内完成 v12→v13 alias DDL、索引补齐和最终预检。
func migrateDurationLedgerSQLiteWorkloadPassEvidenceAliasSchemaOnConnection(
	connection *sql.Conn,
	validator *durationLedgerSQLiteSchemaValidator,
) error {
	schemaVersion, err := readDurationLedgerSQLiteSchemaVersion(connection)
	if err != nil {
		return err
	}
	if schemaVersion != durationLedgerSQLitePreviousSchemaVersion {
		return fmt.Errorf("duration ledger SQLite workload evidence alias migration expected schema version %d, got %d", durationLedgerSQLitePreviousSchemaVersion, schemaVersion)
	}
	actual, err := loadDurationLedgerSQLiteSchemaObjects(connection)
	if err != nil {
		return err
	}
	expectedV12, err := buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteV12SchemaStatements())
	if err != nil {
		return err
	}
	migratable := durationLedgerSQLitePassIndexMigrations()
	if err := validateDurationLedgerSQLiteIndexMigrations(actual, expectedV12, migratable); err != nil {
		return err
	}
	withoutMissingIndexes := durationLedgerSQLiteSchemaWithoutMissingIndexes(actual, expectedV12, migratable)
	if err := compareDurationLedgerSQLiteSchemaObjects(actual, withoutMissingIndexes); err != nil {
		return fmt.Errorf("preflight duration ledger SQLite schema version %d before workload evidence alias migration: %w", schemaVersion, err)
	}
	if err := createMissingDurationLedgerSQLiteIndexes(connection, actual, migratable); err != nil {
		return err
	}
	if _, err := connection.ExecContext(context.Background(), strictWorkloadPassEvidenceAliasSQLiteSchema); err != nil {
		return mapDurationLedgerSQLiteError("migrate duration ledger SQLite workload evidence alias schema", err)
	}
	if _, err := connection.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, durationLedgerSQLiteSchemaVersion)); err != nil {
		return mapDurationLedgerSQLiteError("write duration ledger SQLite workload evidence alias schema version", err)
	}
	return validator.preflight(connection, durationLedgerSQLiteSchemaVersion)
}
