package gate

import (
	"context"
	"database/sql"
	"fmt"
)

const durationLedgerSQLiteReusablePassIndexName = "index:idx_ci_runs_reusable_pass"
const durationLedgerSQLiteMigrationPassIndexName = "index:idx_ci_workload_pass_evidence_migration"

const durationLedgerSQLiteReusablePassIndexMigrationSchema = `
CREATE INDEX IF NOT EXISTS idx_ci_runs_reusable_pass
	ON ci_runs (accepted_generation, completed_at_unix_ms DESC, job_id DESC)
	WHERE authoritative = 1 AND status = 'passed' AND cleanup_complete = 1;`

const durationLedgerSQLiteMigrationPassIndexMigrationSchema = `
CREATE INDEX IF NOT EXISTS idx_ci_workload_pass_evidence_migration
	ON ci_workload_pass_evidence (workload_id, execution_digest, environment_digest, accepted_generation, origin_job_id, identity_digest);`

type durationLedgerSQLiteIndexMigration struct {
	name   string
	create string
}

func durationLedgerSQLitePassIndexMigrations() []durationLedgerSQLiteIndexMigration {
	return []durationLedgerSQLiteIndexMigration{
		{name: durationLedgerSQLiteReusablePassIndexName, create: durationLedgerSQLiteReusablePassIndexMigrationSchema},
		{name: durationLedgerSQLiteMigrationPassIndexName, create: durationLedgerSQLiteMigrationPassIndexMigrationSchema},
	}
}

// migrateDurationLedgerSQLiteReusablePassIndex 在 current schema 仅缺新索引时
// 原子补齐索引；任何其他 shape 漂移均保持 strict fail-closed。
func migrateDurationLedgerSQLiteReusablePassIndex(
	database *sql.DB,
	validator *durationLedgerSQLiteSchemaValidator,
) error {
	if err := validator.preflight(database, durationLedgerSQLiteSchemaVersion); err == nil {
		return nil
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		return mapDurationLedgerSQLiteError("open duration ledger reusable PASS index migration connection", err)
	}
	if _, err := connection.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			mapDurationLedgerSQLiteError("begin duration ledger reusable PASS index migration", err))
	}
	if err := migrateDurationLedgerSQLiteReusablePassIndexOnConnection(connection, validator); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection, err))
	}
	if _, err := connection.ExecContext(context.Background(), `COMMIT`); err != nil {
		return closeDurationLedgerSQLiteInitializerConnection(connection,
			rollbackDurationLedgerSQLiteInitializer(connection,
				mapDurationLedgerSQLiteError("commit duration ledger reusable PASS index migration", err)))
	}
	return closeDurationLedgerSQLiteInitializerConnection(connection, nil)
}

// migrateDurationLedgerSQLiteReusablePassIndexOnConnection 在锁定事务内复核
// 旧 current shape，再执行单一 CREATE INDEX，不触碰任何历史行。
func migrateDurationLedgerSQLiteReusablePassIndexOnConnection(
	connection *sql.Conn,
	validator *durationLedgerSQLiteSchemaValidator,
) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(connection)
	if err != nil {
		return err
	}
	expected, err := validator.expectedSchema()
	if err != nil {
		return err
	}
	migratable := durationLedgerSQLitePassIndexMigrations()
	if err := validateDurationLedgerSQLiteIndexMigrations(actual, expected, migratable); err != nil {
		return err
	}
	withoutIndex := durationLedgerSQLiteSchemaWithoutMissingIndexes(actual, expected, migratable)
	if err := compareDurationLedgerSQLiteSchemaObjects(actual, withoutIndex); err != nil {
		return fmt.Errorf("preflight duration ledger SQLite current schema before migration indexes: %w", err)
	}
	if err := createMissingDurationLedgerSQLiteIndexes(connection, actual, migratable); err != nil {
		return err
	}
	return validator.preflight(connection, durationLedgerSQLiteSchemaVersion)
}

func validateDurationLedgerSQLiteIndexMigrations(actual, expected map[string]string, migrations []durationLedgerSQLiteIndexMigration) error {
	for _, migration := range migrations {
		expectedDDL, ok := expected[migration.name]
		if !ok {
			return fmt.Errorf("duration ledger SQLite migration index %q is absent from canonical reference schema", migration.name)
		}
		if actualDDL, exists := actual[migration.name]; exists && actualDDL != expectedDDL {
			return fmt.Errorf("duration ledger SQLite schema object %q has incompatible authority shape; refuse migration", migration.name)
		}
	}
	return nil
}

func durationLedgerSQLiteSchemaWithoutMissingIndexes(actual, expected map[string]string, migrations []durationLedgerSQLiteIndexMigration) map[string]string {
	withoutMissing := make(map[string]string, len(expected))
	for name, ddl := range expected {
		if durationLedgerSQLiteIndexMissing(actual, name, migrations) {
			continue
		}
		withoutMissing[name] = ddl
	}
	return withoutMissing
}

func durationLedgerSQLiteIndexMissing(actual map[string]string, name string, migrations []durationLedgerSQLiteIndexMigration) bool {
	if _, exists := actual[name]; exists {
		return false
	}
	for _, migration := range migrations {
		if migration.name == name {
			return true
		}
	}
	return false
}

func createMissingDurationLedgerSQLiteIndexes(connection *sql.Conn, actual map[string]string, migrations []durationLedgerSQLiteIndexMigration) error {
	for _, migration := range migrations {
		if _, exists := actual[migration.name]; exists {
			continue
		}
		if _, err := connection.ExecContext(context.Background(), migration.create); err != nil {
			return mapDurationLedgerSQLiteError("create duration ledger SQLite migration index "+migration.name, err)
		}
	}
	return nil
}
