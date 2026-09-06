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
	if MinRequiredSchemaVersion != 134 {
		t.Fatalf("MinRequiredSchemaVersion = %d, want 134", MinRequiredSchemaVersion)
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

func TestVerifyMinSchemaVersionRejectsActiveLegacyWakeups(t *testing.T) {
	for _, status := range []string{"pending", "dispatching", "sent"} {
		t.Run(status, func(t *testing.T) {
			db := openMigratedSchemaVersionDB(t)
			if _, err := db.ExecContext(context.Background(), `
				INSERT INTO task_dag_runs
				(run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, events, metadata, created_at, updated_at)
				VALUES ('schema-gate-legacy-run', 'schema-gate-legacy-dag', 1, 'manual', 'running', 1, '[]', '{}', 1, 1)`); err != nil {
				t.Fatalf("insert run: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), `
				INSERT INTO task_dag_nodes (dag_key, node_key, run_id, status, created_at, updated_at)
				VALUES ('schema-gate-legacy-dag', 'legacy-node', 1, 'ready', 1, 1)`); err != nil {
				t.Fatalf("insert node: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), `
				INSERT INTO task_dag_wakeups
				(dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, payload_schema_version, idempotency_key, status, next_retry_at, created_at, updated_at)
				VALUES ('schema-gate-legacy-dag', 'legacy-node', 1, 'node_start', 'legacy-agent',
				        '{"wakeup_payload_kind":"dag_downstream","schema_version":1,"agent_id":"legacy-agent"}', 1,
				        'schema-gate-legacy-wakeup', 'pending', 1, 1, 1)`); err != nil {
				t.Fatalf("insert versioned wakeup: %v", err)
			}
			// The migration trigger normally makes the marker immutable. Remove
			// only that guard here to model a pre-B09 row observed by startup.
			if _, err := db.ExecContext(context.Background(), "DROP TRIGGER trg_task_dag_wakeups_v134_version_immutable"); err != nil {
				t.Fatalf("drop version immutability trigger: %v", err)
			}
			if _, err := db.ExecContext(context.Background(), "UPDATE task_dag_wakeups SET payload_schema_version=0,status=? WHERE idempotency_key='schema-gate-legacy-wakeup'", status); err != nil {
				t.Fatalf("make active legacy wakeup: %v", err)
			}

			err := VerifyMinSchemaVersion(context.Background(), db)
			if err == nil {
				t.Fatal("VerifyMinSchemaVersion() error = nil, want active legacy wakeup rejection")
			}
			message := err.Error()
			for _, want := range []string{
				"payload_schema_version=0",
				"explicit operator migration/quarantine",
				"id=1",
				"dag_key=\"schema-gate-legacy-dag\"",
				"node_key=\"legacy-node\"",
				"wakeup_kind=\"node_start\"",
				"status=\"" + status + "\"",
			} {
				if !strings.Contains(message, want) {
					t.Fatalf("VerifyMinSchemaVersion() error = %q, want %q", message, want)
				}
			}
		})
	}
}

func TestVerifyNoActiveLegacyWakeupsIgnoresTerminalStatuses(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE task_dag_wakeups (
			id INTEGER NOT NULL,
			dag_key TEXT NOT NULL,
			node_key TEXT NOT NULL,
			wakeup_kind TEXT NOT NULL,
			status TEXT NOT NULL,
			payload_schema_version INTEGER NOT NULL
		)`); err != nil {
		t.Fatalf("create wakeup fixture: %v", err)
	}
	for id, status := range map[int]string{1: "failed", 2: "cancelled", 3: "succeeded"} {
		if _, err := db.ExecContext(context.Background(),
			`INSERT INTO task_dag_wakeups(id,dag_key,node_key,wakeup_kind,status,payload_schema_version) VALUES (?,?,?,?,?,0)`,
			id, "dag", "node", "node_start", status); err != nil {
			t.Fatalf("insert %s wakeup: %v", status, err)
		}
	}
	if err := verifyNoActiveLegacyWakeups(context.Background(), db); err != nil {
		t.Fatalf("verifyNoActiveLegacyWakeups() error = %v, want terminal statuses ignored", err)
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
		"orchestration_command_intents.caller_principal_ref",
		"orchestration_command_intents.scope_ref",
		"orchestration_command_intent_profiles.profile_kind",
		"orchestration_command_intent_profiles.caller_auth_context",
		"orchestration_command_intent_profiles.authorization_grant_revision",
		"orchestration_command_intent_profiles.source_binding_digest",
		"orchestration_command_intent_profiles.target_binding_digest",
		"orchestration_command_intent_profiles.complete_intent_digest",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("VerifyMinSchemaVersion err = %v, want missing %s", err, want)
		}
	}
}

func TestVerifyMinSchemaVersionRejectsMissingCommandIntentProfileColumns(t *testing.T) {
	t.Parallel()
	for _, column := range []string{
		"profile_kind", "caller_auth_context", "authorization_grant_revision",
		"source_active_process_receipt_ref", "target_binding_updated_at", "complete_intent_digest",
	} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()
			db := openMigratedSchemaVersionDB(t)
			execSchemaVersionMutation(t, db, fmt.Sprintf(
				"ALTER TABLE orchestration_command_intent_profiles RENAME COLUMN %s TO %s_corrupt",
				column,
				column,
			))
			assertSchemaVersionRejected(t, db, "orchestration_command_intent_profiles."+column)
		})
	}
}

func TestVerifyMinSchemaVersionRejectsMissingCommandIntentProfileTriggers(t *testing.T) {
	t.Parallel()
	for _, trigger := range requiredCommandIntentProfileTriggers() {
		t.Run(trigger, func(t *testing.T) {
			t.Parallel()
			db := openMigratedSchemaVersionDB(t)
			execSchemaVersionMutation(t, db, "DROP TRIGGER "+trigger)
			assertSchemaVersionRejected(t, db, trigger)
		})
	}
}

func TestRequiredCommandIntentProfileColumnsMatchMigratedSchema(t *testing.T) {
	t.Parallel()
	db := openMigratedSchemaVersionDB(t)
	got, err := sqliteTableColumns(context.Background(), db, "orchestration_command_intent_profiles")
	if err != nil {
		t.Fatalf("read command intent profile columns: %v", err)
	}
	want := make(map[string]struct{}, len(requiredCommandIntentProfileColumns()))
	for _, column := range requiredCommandIntentProfileColumns() {
		want[column] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("profile schema gate columns = %v, migrated columns = %v", want, got)
	}
	for column := range want {
		if _, ok := got[column]; !ok {
			t.Fatalf("profile schema gate is missing migrated column %s", column)
		}
	}
}

func TestVerifyMinSchemaVersionRejectsMissingCommandIntentIdentityColumns(t *testing.T) {
	t.Parallel()
	for _, column := range []string{"caller_principal_ref", "scope_ref"} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()
			db := openMigratedSchemaVersionDB(t)
			execSchemaVersionMutation(t, db, fmt.Sprintf(
				"ALTER TABLE orchestration_command_intents RENAME COLUMN %s TO %s_corrupt",
				column,
				column,
			))
			assertSchemaVersionRejected(t, db, "orchestration_command_intents."+column)
		})
	}
}

func TestVerifyMinSchemaVersionAcceptsCombinedV132(t *testing.T) {
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
		t.Fatalf("VerifyMinSchemaVersion() error = %v, want valid combined v131", err)
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
	for _, table := range requiredBaselineTables() {
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
