package gate

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

type durationLedgerSQLiteSchemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type durationLedgerSQLiteSchemaValidator struct {
	expectedSchema      func() (map[string]string, error)
	initializeAuthority func(*sql.DB, func() time.Time, *durationLedgerSQLiteSchemaValidator) error
}

func newDurationLedgerSQLiteSchemaValidator() *durationLedgerSQLiteSchemaValidator {
	return &durationLedgerSQLiteSchemaValidator{
		expectedSchema:      sync.OnceValues(buildDurationLedgerSQLiteReferenceSchema),
		initializeAuthority: initializeDurationLedgerSQLiteCurrentSchema,
	}
}

// durationLedgerSQLiteCurrentSchemaStatements returns the sole physical schema
// definition used by both empty-authority initialization and exact comparison.
func durationLedgerSQLiteCurrentSchemaStatements() []string {
	statements := durationLedgerSQLiteSchemaStatementsV14()
	statements = append(statements, durationLedgerRemoteCIExecutionScopeSchemaStatements()...)
	statements = append(statements, durationLedgerRetainedWorkloadPassProofSchemaStatements()...)
	return append(statements, durationLedgerSourceReplayIndexSchemaStatements()...)
}

// durationLedgerSQLiteSchemaStatementsV14 is the complete pre-scope schema.
func durationLedgerSQLiteSchemaStatementsV14() []string {
	statements := durationLedgerSQLiteLegacySchemaStatements()
	statements = append(statements, durationLedgerCompileTimingSchemaStatements()...)
	statements = append(statements, durationLedgerRemoteCITerminalEvidenceSchemaStatements()...)
	return append(statements, strictLocalWorkloadPassSQLiteSchema)
}

func durationLedgerSQLiteLegacySchemaStatements() []string {
	statements := []string{
		durationLedgerSQLiteSchema,
		strictWorkloadPassReuseSQLiteSchema,
		strictCheckReceiptReuseSQLiteSchema,
		durationLedgerLiveTimingWarningTableSchema,
		durationLedgerRunTimingWarningTableSchema,
		durationLedgerLiveTimingWarningIndexSchema,
		durationLedgerRunTimingWarningIndexSchema,
	}
	return statements
}

func durationLedgerSQLiteLegacySchemaStatementsV13() []string {
	statements := []string{
		durationLedgerSQLiteSchema,
		frozenWorkloadPassReuseSQLiteSchemaV13,
		strictCheckReceiptReuseSQLiteSchema,
		durationLedgerLiveTimingWarningTableSchema,
		durationLedgerRunTimingWarningTableSchema,
		durationLedgerLiveTimingWarningIndexSchema,
		durationLedgerRunTimingWarningIndexSchema,
	}
	statements = append(statements, durationLedgerCompileTimingSchemaStatements()...)
	return append(statements, durationLedgerRemoteCITerminalEvidenceSchemaStatements()...)
}

func (validator *durationLedgerSQLiteSchemaValidator) preflight(queryer durationLedgerSQLiteSchemaQueryer, schemaVersion int) error {
	actual, err := loadDurationLedgerSQLiteSchemaObjects(queryer)
	if err != nil {
		return err
	}
	if len(actual) == 0 {
		if schemaVersion != 0 {
			return fmt.Errorf("duration ledger SQLite empty authority has nonzero schema version %d", schemaVersion)
		}
		return nil
	}
	if schemaVersion != durationLedgerSQLiteSchemaVersion {
		return fmt.Errorf("duration ledger SQLite schema version %d is incompatible; refuse migration", schemaVersion)
	}
	expected, err := validator.expectedSchema()
	if err != nil {
		return err
	}
	return compareDurationLedgerSQLiteSchemaObjects(actual, expected)
}

func compareDurationLedgerSQLiteSchemaObjects(actual, expected map[string]string) error {
	for key, expectedDDL := range expected {
		actualDDL, ok := actual[key]
		if !ok {
			return fmt.Errorf("duration ledger SQLite schema object %q is missing; refuse partial authority migration", key)
		}
		if actualDDL != expectedDDL {
			return fmt.Errorf("duration ledger SQLite schema object %q has incompatible authority shape; refuse migration", key)
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("duration ledger SQLite schema object %q is not canonical; refuse migration", key)
		}
	}
	return nil
}

// buildDurationLedgerSQLiteReferenceSchema 返回由唯一 current DDL 构造的物理 schema。
func buildDurationLedgerSQLiteReferenceSchema() (map[string]string, error) {
	return buildDurationLedgerSQLiteReferenceSchemaForStatements(durationLedgerSQLiteCurrentSchemaStatements())
}

func buildDurationLedgerSQLiteReferenceSchemaForStatements(statements []string) (map[string]string, error) {
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open duration ledger SQLite reference schema: %w", err)
	}
	reference.SetMaxOpenConns(1)
	defer reference.Close()
	for _, schema := range statements {
		if _, err := reference.Exec(schema); err != nil {
			return nil, mapDurationLedgerSQLiteError("build duration ledger SQLite reference schema", err)
		}
	}
	return loadDurationLedgerSQLiteSchemaObjects(reference)
}

func loadDurationLedgerSQLiteSchemaObjects(queryer durationLedgerSQLiteSchemaQueryer) (map[string]string, error) {
	rows, err := queryer.QueryContext(context.Background(), `
		SELECT type, name, sql
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("read duration ledger SQLite physical schema", err)
	}
	defer rows.Close()
	objects := map[string]string{}
	for rows.Next() {
		var (
			objectType, name string
			ddl              sql.NullString
		)
		if err := rows.Scan(&objectType, &name, &ddl); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan duration ledger SQLite physical schema", err)
		}
		normalizedDDL := "<null>"
		if ddl.Valid {
			normalizedDDL = normalizeSQLiteDDL(ddl.String)
		}
		objects[objectType+":"+name] = normalizedDDL
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate duration ledger SQLite physical schema", err)
	}
	return objects, nil
}
