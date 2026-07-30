package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE schema_migrations (version INTEGER NOT NULL, name TEXT NOT NULL, filename TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertVersion(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO schema_migrations(version,name,filename,applied_at) VALUES (?,?,?,?)`,
		version, "test", "test.sql", 0); err != nil {
		t.Fatal(err)
	}
}

func TestMinRequiredSchemaVersion(t *testing.T) {
	t.Parallel()
	if MinRequiredSchemaVersion != 123 {
		t.Fatalf("MinRequiredSchemaVersion = %d, want 123", MinRequiredSchemaVersion)
	}
}

func TestQuerySchemaVersion_AcceptsAtOrAboveMinimum(t *testing.T) {
	t.Parallel()
	for _, v := range []int{MinRequiredSchemaVersion, MinRequiredSchemaVersion + 1, MinRequiredSchemaVersion + 50} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			insertVersion(t, db, v)
			var got int
			if err := querySchemaVersion(context.Background(), db, &got); err != nil {
				t.Fatalf("querySchemaVersion error = %v", err)
			}
			if got != v {
				t.Fatalf("got version %d, want %d", got, v)
			}
		})
	}
}

func TestVerifyMinSchemaVersion_RejectsBelowMinimum(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertVersion(t, db, MinRequiredSchemaVersion-1)
	var got int
	if err := querySchemaVersion(context.Background(), db, &got); err != nil {
		t.Fatalf("querySchemaVersion error = %v", err)
	}
	// Confirm the error message template is bilingual.
	msg := fmt.Sprintf(
		"数据库 migration 版本 < %d (当前=%d)，请先 apply 后再启动；database migration version below %d (current=%d), apply pending migrations before starting",
		MinRequiredSchemaVersion, got, MinRequiredSchemaVersion, got)
	for _, want := range []string{"migration", "apply", "数据库", "database migration version"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

func TestSchemaGateRejectsMissingRequiredColumns(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertVersion(t, db, MinRequiredSchemaVersion)
	createMarkerBaselineTables(t, db)

	err := VerifyMinSchemaVersion(context.Background(), db)
	if err == nil {
		t.Fatal("VerifyMinSchemaVersion err = nil, want missing required column error")
	}
	for _, want := range []string{
		"agent_provider_binding.provider_recovery_home",
		"agent_threads.prompt_snapshot",
		"hook_pending_reviews.thread_id",
		"hook_pending_reviews.turn_id",
		"hook_pending_reviews.payload",
		"shared_files.content_location",
		"turn_dedupe_registry.terminal_at",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("VerifyMinSchemaVersion err = %v, want missing %s", err, want)
		}
	}
}

func TestVerifyMinSchemaVersionAcceptsCombinedV123(t *testing.T) {
	t.Parallel()
	db := openMigratedSchemaVersionDB(t)
	columns, err := sqliteTableColumns(context.Background(), db, "agent_provider_binding")
	if err != nil {
		t.Fatalf("read agent_provider_binding columns: %v", err)
	}
	if _, ok := columns["provider_recovery_home"]; !ok {
		t.Fatal("agent_provider_binding.provider_recovery_home missing after v123 migrations")
	}
	if err := VerifyMinSchemaVersion(context.Background(), db); err != nil {
		t.Fatalf("VerifyMinSchemaVersion() error = %v, want valid combined v123", err)
	}
}

func TestVerifyMinSchemaVersionRejectsMissingTerminalProtocolTables(t *testing.T) {
	t.Parallel()
	for _, table := range []string{
		"terminal_outcome_current_heads",
		"public_terminal_outcome_history",
		"terminal_outcome_private_dag_payloads",
		"terminal_outcome_outbox_v2",
	} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()
			db := openMigratedSchemaVersionDB(t)
			execSchemaVersionMutation(t, db, "DROP TABLE "+table)
			assertSchemaVersionRejected(t, db, table)
		})
	}
}

func TestVerifyMinSchemaVersionRejectsMissingTerminalProtocolColumns(t *testing.T) {
	t.Parallel()
	for _, required := range []requiredSQLiteColumn{
		{table: "terminal_outcome_current_heads", column: "capability"},
		{table: "terminal_outcome_current_heads", column: "version"},
		{table: "terminal_outcome_current_heads", column: "terminal_identity"},
		{table: "public_terminal_outcome_history", column: "head_version"},
		{table: "public_terminal_outcome_history", column: "public_outcome_json"},
		{table: "terminal_outcome_private_dag_payloads", column: "payload_json"},
		{table: "terminal_outcome_outbox_v2", column: "public_payload_json"},
		{table: "terminal_outcome_outbox_v2", column: "claim_token"},
	} {
		t.Run(required.table+"_"+required.column, func(t *testing.T) {
			t.Parallel()
			db := openMigratedSchemaVersionDB(t)
			execSchemaVersionMutation(t, db, fmt.Sprintf(
				"ALTER TABLE %s RENAME COLUMN %s TO %s_corrupt",
				required.table,
				required.column,
				required.column,
			))
			assertSchemaVersionRejected(t, db, required.table+"."+required.column)
		})
	}
}

func TestVerifyMinSchemaVersionRejectsInvalidTerminalProtocolViews(t *testing.T) {
	t.Parallel()
	for _, view := range []string{
		"terminal_outcome_heads",
		"public_terminal_outcomes",
		"terminal_outcome_outbox",
	} {
		for _, mutation := range []struct {
			name string
			sql  string
		}{
			{name: "missing", sql: "DROP VIEW " + view},
			{name: "writable_table", sql: "DROP VIEW " + view + "; CREATE TABLE " + view + " (id INTEGER)"},
		} {
			t.Run(view+"_"+mutation.name, func(t *testing.T) {
				t.Parallel()
				db := openMigratedSchemaVersionDB(t)
				execSchemaVersionMutation(t, db, mutation.sql)
				assertSchemaVersionRejected(t, db, view)
			})
		}
	}
}

func openMigratedSchemaVersionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open migrated schema version DB: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := runFixtureMigrations(t, db); err != nil {
		t.Fatalf("runFixtureMigrations() error = %v", err)
	}
	return db
}

func execSchemaVersionMutation(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), statement); err != nil {
		t.Fatalf("mutate migrated schema with %q: %v", statement, err)
	}
}

func assertSchemaVersionRejected(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	err := VerifyMinSchemaVersion(context.Background(), db)
	if err == nil {
		t.Fatalf("VerifyMinSchemaVersion() error = nil, want rejection for %s", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("VerifyMinSchemaVersion() error = %v, want %q", err, want)
	}
}

func TestVerifySQLiteRequiredColumnsRejectsQueryerWithoutQueryContext(t *testing.T) {
	t.Parallel()

	err := verifySQLiteRequiredColumns(context.Background(), queryRowOnly{})
	if err == nil {
		t.Fatal("verifySQLiteRequiredColumns() error = nil, want unsupported QueryContext error")
	}
	if !strings.Contains(err.Error(), "unsupported SQLite required column queryer") {
		t.Fatalf("verifySQLiteRequiredColumns() error = %v, want unsupported queryer", err)
	}
}

type queryRowOnly struct{}

func (queryRowOnly) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func createMarkerBaselineTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range requiredBaselineTables {
		if _, err := db.ExecContext(context.Background(), fmt.Sprintf(`CREATE TABLE %s (id INTEGER)`, table)); err != nil {
			t.Fatalf("create marker table %s: %v", table, err)
		}
	}
}

func TestQuerySchemaVersion_PropagatesQueryError(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got int
	if err := querySchemaVersion(context.Background(), db, &got); err == nil {
		t.Fatal("expected error from missing table, got nil")
	}
}
