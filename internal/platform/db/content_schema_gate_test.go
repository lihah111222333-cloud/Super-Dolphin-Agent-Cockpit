package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestContentSchemaGateRequiresCurrentSchemaFloor(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	assertContentSchemaMutationRejected(
		t,
		database,
		"DELETE FROM schema_migrations WHERE version >= 134",
		"database migration version below 134",
	)
}

func TestContentSchemaGateRejectsMissingTables(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	for _, table := range sortedContentSchemaTables() {
		t.Run(table, func(t *testing.T) {
			assertContentSchemaMutationRejected(t, database, "DROP TABLE "+table, table)
		})
	}
}

func TestContentSchemaGateRejectsMissingColumns(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	for _, required := range representativeContentSchemaColumns(t) {
		required := required
		t.Run(required.table+"_"+required.column, func(t *testing.T) {
			assertContentSchemaMutationRejected(
				t,
				database,
				fmt.Sprintf(
					"ALTER TABLE %s RENAME COLUMN %s TO %s_corrupt",
					required.table,
					required.column,
					required.column,
				),
				required.table+"."+required.column,
			)
		})
	}
}

func TestContentSchemaGateRejectsMissingTriggers(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	for _, trigger := range requiredContentSchemaTriggers() {
		trigger := trigger
		t.Run(trigger, func(t *testing.T) {
			assertContentSchemaMutationRejected(t, database, "DROP TRIGGER "+trigger, trigger)
		})
	}
}

func TestContentSchemaGateColumnsMatchMigration132(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	for _, table := range sortedContentSchemaTables() {
		table := table
		t.Run(table, func(t *testing.T) {
			migrated, err := sqliteTableColumns(context.Background(), database, table)
			if err != nil {
				t.Fatalf("read migrated CONTENT-1 columns for %s: %v", table, err)
			}
			registered := make(map[string]struct{}, len(requiredContentSchemaColumns()[table]))
			for _, column := range requiredContentSchemaColumns()[table] {
				if _, duplicated := registered[column]; duplicated {
					t.Fatalf("content schema gate registers duplicate column %s.%s", table, column)
				}
				registered[column] = struct{}{}
			}
			if difference := contentSchemaColumnDifference(migrated, registered); len(difference) > 0 {
				t.Fatalf("migration 132 has unregistered CONTENT-1 column(s) for %s: %s", table, strings.Join(difference, ", "))
			}
			if difference := contentSchemaColumnDifference(registered, migrated); len(difference) > 0 {
				t.Fatalf("content schema gate has stale column(s) for %s: %s", table, strings.Join(difference, ", "))
			}
		})
	}
}

func TestContentSchemaGateRejectsSecretBearingColumns(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	for _, column := range []string{"plaintext", "raw_content"} {
		column := column
		t.Run(column, func(t *testing.T) {
			const table = "orchestration_content_private_payload_receipts"
			assertContentSchemaMutationRejected(
				t,
				database,
				"ALTER TABLE "+table+" ADD COLUMN "+column+" TEXT",
				table+"."+column,
			)
		})
	}
	for _, table := range sortedContentSchemaTables() {
		table := table
		t.Run("blob_"+table, func(t *testing.T) {
			const column = "private_payload_bytes"
			assertContentSchemaMutationRejected(
				t,
				database,
				"ALTER TABLE "+table+" ADD COLUMN "+column+" BLOB",
				table+"."+column,
			)
		})
	}
}

func sortedContentSchemaTables() []string {
	tables := make([]string, 0, len(requiredContentSchemaColumns()))
	for table := range requiredContentSchemaColumns() {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

func representativeContentSchemaColumns(t *testing.T) []requiredSQLiteColumn {
	t.Helper()
	tables := sortedContentSchemaTables()
	required := make([]requiredSQLiteColumn, 0, len(tables))
	for _, table := range tables {
		columns := requiredContentSchemaColumns()[table]
		if len(columns) == 0 {
			t.Fatalf("content schema gate table has no required columns: %s", table)
		}
		required = append(required, requiredSQLiteColumn{table: table, column: columns[len(columns)/2]})
	}
	return required
}

func contentSchemaColumnDifference(left, right map[string]struct{}) []string {
	difference := make([]string, 0)
	for column := range left {
		if _, ok := right[column]; !ok {
			difference = append(difference, column)
		}
	}
	sort.Strings(difference)
	return difference
}

func assertContentSchemaMutationRejected(t *testing.T, database *sql.DB, statement, want string) {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin CONTENT-1 schema mutation transaction: %v", err)
	}
	finished := false
	t.Cleanup(func() {
		if finished {
			return
		}
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("rollback CONTENT-1 schema mutation transaction: %v", err)
		}
	})
	if _, err := tx.ExecContext(context.Background(), statement); err != nil {
		t.Fatalf("mutate CONTENT-1 schema with %q: %v", statement, err)
	}
	gateErr := VerifyMinSchemaVersion(context.Background(), tx)
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback CONTENT-1 schema mutation transaction: %v", err)
	}
	finished = true
	if gateErr == nil {
		t.Fatalf("VerifyMinSchemaVersion() error = nil after %q", statement)
	}
	if !strings.Contains(gateErr.Error(), want) {
		t.Fatalf("VerifyMinSchemaVersion() error = %v, want %q after %q", gateErr, want, statement)
	}
}
