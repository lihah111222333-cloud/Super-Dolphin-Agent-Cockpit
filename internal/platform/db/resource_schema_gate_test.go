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

func TestResourceSchemaGate(t *testing.T) {
	t.Run("columns match migration 133 bidirectionally", testResourceSchemaGateColumnsMatchMigration133Bidirectionally)
	t.Run("column difference reports missing and stale", testResourceSchemaGateColumnDifferenceReportsMissingAndStale)
	t.Run("triggers match migration 133 bidirectionally", testResourceSchemaGateTriggersMatchMigration133Bidirectionally)
	t.Run("rejects dropped tables columns and triggers", testResourceSchemaGateRejectsDroppedTablesColumnsAndTriggers)
	t.Run("rejects blob and sensitive columns", testResourceSchemaGateRejectsBlobAndSensitiveColumns)
	t.Run("allows resource token metadata", testResourceSchemaGateAllowsResourceTokenMetadata)
}

func testResourceSchemaGateColumnsMatchMigration133Bidirectionally(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	for _, table := range sortedResourceSchemaTables() {
		table := table
		t.Run(table, func(t *testing.T) {
			migrated, err := sqliteTableColumns(context.Background(), database, table)
			if err != nil {
				t.Fatalf("read migrated RESOURCE columns for %s: %v", table, err)
			}
			registered := resourceRegisteredColumnSet(t, table)
			if missing := resourceSchemaColumnDifference(migrated, registered); len(missing) > 0 {
				t.Fatalf("migration 133 has unregistered RESOURCE column(s) for %s: %s", table, strings.Join(missing, ", "))
			}
			if stale := resourceSchemaColumnDifference(registered, migrated); len(stale) > 0 {
				t.Fatalf("RESOURCE schema gate has stale column(s) for %s: %s", table, strings.Join(stale, ", "))
			}
		})
	}
}

func testResourceSchemaGateColumnDifferenceReportsMissingAndStale(t *testing.T) {
	t.Parallel()
	producer := map[string]struct{}{"actual_column": {}}
	registry := map[string]struct{}{"stale_column": {}}
	if got := resourceSchemaColumnDifference(producer, registry); strings.Join(got, ",") != "actual_column" {
		t.Fatalf("missing difference = %v, want [actual_column]", got)
	}
	if got := resourceSchemaColumnDifference(registry, producer); strings.Join(got, ",") != "stale_column" {
		t.Fatalf("stale difference = %v, want [stale_column]", got)
	}
}

func testResourceSchemaGateTriggersMatchMigration133Bidirectionally(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	migrated := resourceMigratedTriggers(t, database)
	registered := resourceRegisteredTriggerSet(t)
	if missing := resourceSchemaColumnDifference(migrated, registered); len(missing) > 0 {
		t.Fatalf("migration 133 has unregistered RESOURCE trigger(s): %s", strings.Join(missing, ", "))
	}
	if stale := resourceSchemaColumnDifference(registered, migrated); len(stale) > 0 {
		t.Fatalf("RESOURCE schema gate has stale trigger(s): %s", strings.Join(stale, ", "))
	}
	if len(registered) != 9 {
		t.Fatalf("RESOURCE schema trigger count = %d, want 9", len(registered))
	}
}

func testResourceSchemaGateRejectsDroppedTablesColumnsAndTriggers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		statement string
		want      string
	}{
		{
			name:      "table",
			statement: "DROP TABLE orchestration_resource_usage_ledger",
			want:      "orchestration_resource_usage_ledger.usage_entry_id",
		},
		{
			name: "column",
			statement: "ALTER TABLE orchestration_resource_effective_policy_snapshots " +
				"RENAME COLUMN policy_owner_proof_ref TO policy_owner_proof_ref_corrupt",
			want: "orchestration_resource_effective_policy_snapshots.policy_owner_proof_ref",
		},
		{
			name:      "trigger",
			statement: "DROP TRIGGER trg_orchestration_resource_reservation_no_update",
			want:      "trg_orchestration_resource_reservation_no_update",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			database := openMigratedSchemaVersionDB(t)
			assertResourceSchemaMutationRejected(t, database, test.statement, test.want)
		})
	}
}

func testResourceSchemaGateRejectsBlobAndSensitiveColumns(t *testing.T) {
	t.Parallel()
	for _, table := range sortedResourceSchemaTables() {
		table := table
		t.Run("blob_"+table, func(t *testing.T) {
			database := openMigratedSchemaVersionDB(t)
			const column = "raw_resource_bytes"
			assertResourceSchemaMutationRejected(
				t,
				database,
				"ALTER TABLE "+table+" ADD COLUMN "+column+" BLOB",
				table+"."+column,
			)
		})
	}

	for _, column := range []string{"raw_payload", "api_credential", "client_secret"} {
		column := column
		t.Run(column, func(t *testing.T) {
			database := openMigratedSchemaVersionDB(t)
			const table = "orchestration_resource_usage_ledger"
			assertResourceSchemaMutationRejected(
				t,
				database,
				"ALTER TABLE "+table+" ADD COLUMN "+column+" TEXT",
				table+"."+column,
			)
		})
	}
}

func testResourceSchemaGateAllowsResourceTokenMetadata(t *testing.T) {
	t.Parallel()
	database := openMigratedSchemaVersionDB(t)
	if err := verifyResourceSchemaColumnPolicy(context.Background(), database); err != nil {
		t.Fatalf("verifyResourceSchemaColumnPolicy() rejected migration 133 metadata: %v", err)
	}
	for _, column := range []string{"limit_input_tokens", "limit_output_tokens", "reserved_input_tokens", "reserved_output_tokens"} {
		if forbiddenResourceColumnName(column) {
			t.Fatalf("forbiddenResourceColumnName(%q) = true for legal RESOURCE metadata", column)
		}
	}
}

func sortedResourceSchemaTables() []string {
	tables := make([]string, 0, len(requiredResourceSchemaColumns()))
	for table := range requiredResourceSchemaColumns() {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

func resourceRegisteredColumnSet(t *testing.T, table string) map[string]struct{} {
	t.Helper()
	columns := requiredResourceSchemaColumns()[table]
	if len(columns) == 0 {
		t.Fatalf("RESOURCE schema gate table has no required columns: %s", table)
	}
	registered := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if _, duplicate := registered[column]; duplicate {
			t.Fatalf("RESOURCE schema gate registers duplicate column %s.%s", table, column)
		}
		registered[column] = struct{}{}
	}
	return registered
}

func resourceRegisteredTriggerSet(t *testing.T) map[string]struct{} {
	t.Helper()
	registered := make(map[string]struct{}, len(requiredResourceSchemaTriggers()))
	for _, trigger := range requiredResourceSchemaTriggers() {
		if _, duplicate := registered[trigger]; duplicate {
			t.Fatalf("RESOURCE schema gate registers duplicate trigger %s", trigger)
		}
		registered[trigger] = struct{}{}
	}
	return registered
}

func resourceMigratedTriggers(t *testing.T, database *sql.DB) map[string]struct{} {
	t.Helper()
	rows, err := database.QueryContext(
		context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'trigger' AND tbl_name LIKE 'orchestration_resource_%'",
	)
	if err != nil {
		t.Fatalf("read migrated RESOURCE triggers: %v", err)
	}
	t.Cleanup(func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close migrated RESOURCE trigger rows: %v", err)
		}
	})
	triggers := make(map[string]struct{})
	for rows.Next() {
		var trigger string
		if err := rows.Scan(&trigger); err != nil {
			t.Fatalf("scan migrated RESOURCE trigger: %v", err)
		}
		triggers[trigger] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated RESOURCE triggers: %v", err)
	}
	return triggers
}

func resourceSchemaColumnDifference(left, right map[string]struct{}) []string {
	difference := make([]string, 0)
	for name := range left {
		if _, ok := right[name]; !ok {
			difference = append(difference, name)
		}
	}
	sort.Strings(difference)
	return difference
}

func verifyResourceSchemaTestGate(ctx context.Context, q any) error {
	queryer, ok := q.(sqlContextQuery)
	if !ok {
		return fmt.Errorf("unsupported SQLite RESOURCE test queryer %T", q)
	}
	for _, table := range sortedResourceSchemaTables() {
		actual, err := sqliteTableColumns(ctx, queryer, table)
		if err != nil {
			return fmt.Errorf("read RESOURCE table %s: %w", table, err)
		}
		for _, column := range requiredResourceSchemaColumns()[table] {
			if _, exists := actual[column]; !exists {
				return fmt.Errorf("missing required RESOURCE column: %s.%s", table, column)
			}
		}
	}

	queryRow, ok := q.(sqlContextQueryRow)
	if !ok {
		return fmt.Errorf("unsupported SQLite RESOURCE trigger queryer %T", q)
	}
	for _, trigger := range requiredResourceSchemaTriggers() {
		var exists int
		if err := queryRow.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?",
			trigger,
		).Scan(&exists); err != nil {
			return fmt.Errorf("verify RESOURCE trigger %s: %w", trigger, err)
		}
		if exists != 1 {
			return fmt.Errorf("missing required RESOURCE trigger: %s", trigger)
		}
	}
	return verifyResourceSchemaColumnPolicy(ctx, q)
}

func assertResourceSchemaMutationRejected(t *testing.T, database *sql.DB, statement, want string) {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin RESOURCE schema mutation transaction: %v", err)
	}
	finished := false
	t.Cleanup(func() {
		if finished {
			return
		}
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("rollback RESOURCE schema mutation transaction: %v", err)
		}
	})
	if _, err := tx.ExecContext(context.Background(), statement); err != nil {
		t.Fatalf("mutate RESOURCE schema with %q: %v", statement, err)
	}
	gateErr := verifyResourceSchemaTestGate(context.Background(), tx)
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback RESOURCE schema mutation transaction: %v", err)
	}
	finished = true
	if gateErr == nil {
		t.Fatalf("verifyResourceSchemaTestGate() error = nil after %q", statement)
	}
	if !strings.Contains(gateErr.Error(), want) {
		t.Fatalf("verifyResourceSchemaTestGate() error = %v, want %q after %q", gateErr, want, statement)
	}
}
