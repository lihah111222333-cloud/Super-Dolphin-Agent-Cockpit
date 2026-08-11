package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDurationLedgerSQLiteFreshV14InitializesLocalAuthority(t *testing.T) {
	path := t.TempDir() + "/fresh.sqlite"
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("fresh authority path exists before initialization: %v", err)
	}
	store, err := NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("fresh authority user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	assertLocalAuthorityStateGeneration(t, database, "1")
	for _, table := range []string{"ci_local_authority_state", "ci_local_workload_origins", "ci_local_workload_executions", "ci_local_workload_pass_evidence"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("fresh authority table %q count = %d, want 1", table, count)
		}
	}
}

func TestDurationLedgerSQLiteV13AdditiveMigrationPreservesRemoteAuthority(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v13-additive.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV13Fixture(t, database)
	insertRemotePassFixture(t, database)
	if _, err := database.Exec(`PRAGMA user_version = 13`); err != nil {
		t.Fatal(err)
	}
	beforeSchema, err := loadDurationLedgerSQLiteSchemaObjects(database)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []string{
		"index:idx_ci_run_workload_results_retention",
		"index:idx_ci_workload_pass_evidence_environment_replay",
	} {
		if _, ok := beforeSchema[object]; ok {
			t.Fatalf("frozen v13 fixture unexpectedly contains %q", object)
		}
	}
	beforeRemoteEvidenceFK := sqliteForeignKeyFingerprint(t, database, "ci_workload_pass_evidence")
	beforeEvidenceCount, beforeEvidenceDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`)
	beforeRunCount, beforeRunDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_runs ORDER BY job_id`)

	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("migrated user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	assertRemoteAuthorityPreserved(t, database, beforeSchema, beforeRemoteEvidenceFK, beforeEvidenceCount, beforeEvidenceDigest, beforeRunCount, beforeRunDigest)
	assertLocalAuthorityStateGeneration(t, database, "1")
}

func TestDurationLedgerSQLiteV14ToV15AddsOnlyExecutionScopeObjects(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "v14-execution-scope.sqlite")
	defer database.Close()
	createDurationLedgerSQLiteV14Fixture(t, database)
	before := snapshotDurationLedgerSQLiteSchema(t, database)

	migrateDurationLedgerSQLiteSchema(t, database)
	after := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	assertDurationLedgerSQLiteSchemaObjectsUnchanged(t, before.schema, after, "v14 object", "v15 migration")
	assertSQLiteRowsFingerprintUnchanged(t, database, `SELECT * FROM ci_runs ORDER BY job_id`, before.runCount, before.runDigest, "v14 ci_runs", "v15 migration")
	assertDurationLedgerSQLiteSchemaObjectsPresent(t, after, []string{"table:ci_remote_run_execution_scopes", "index:idx_ci_remote_run_execution_scopes_job", "index:idx_ci_remote_run_execution_scopes_generation"}, "v15 execution-scope")
}

type durationLedgerSQLiteSchemaSnapshot struct {
	schema    map[string]string
	runCount  int
	runDigest string
}

func createDurationLedgerSQLiteV14Fixture(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range durationLedgerSQLiteSchemaStatementsV14() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create v14 schema: %v", err)
		}
	}
	connection, err := database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := initializeLocalAuthorityStateOnConnection(connection, func() time.Time {
		return time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	}); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	insertRemotePassFixture(t, database)
	if _, err := database.Exec(`PRAGMA user_version = 14`); err != nil {
		t.Fatal(err)
	}
}

func snapshotDurationLedgerSQLiteSchema(t *testing.T, database *sql.DB) durationLedgerSQLiteSchemaSnapshot {
	t.Helper()
	schema := snapshotDurationLedgerSQLiteSchemaObjects(t, database)
	runCount, runDigest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_runs ORDER BY job_id`)
	return durationLedgerSQLiteSchemaSnapshot{schema: schema, runCount: runCount, runDigest: runDigest}
}

func snapshotDurationLedgerSQLiteSchemaObjects(t *testing.T, database *sql.DB) map[string]string {
	t.Helper()
	schema, err := loadDurationLedgerSQLiteSchemaObjects(database)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func migrateDurationLedgerSQLiteSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("migrated user_version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
}

func assertDurationLedgerSQLiteSchemaObjectsUnchanged(t *testing.T, before, after map[string]string, objectDescription, migration string) {
	t.Helper()
	for object, ddl := range before {
		if got := after[object]; got != ddl {
			t.Fatalf("%s %s changed during %s: before=%q after=%q", objectDescription, object, migration, ddl, got)
		}
	}
}

func assertSQLiteRowsFingerprintUnchanged(t *testing.T, database *sql.DB, query string, wantCount int, wantDigest, rowDescription, migration string) {
	t.Helper()
	if got, digest := sqliteRowsFingerprint(t, database, query); got != wantCount || digest != wantDigest {
		t.Fatalf("%s changed during %s: before=(%d,%s) after=(%d,%s)", rowDescription, migration, wantCount, wantDigest, got, digest)
	}
}

func assertDurationLedgerSQLiteSchemaObjectsPresent(t *testing.T, schema map[string]string, objects []string, description string) {
	t.Helper()
	for _, object := range objects {
		if got := schema[object]; got == "" {
			t.Fatalf("%s object %q is missing", description, object)
		}
	}
}

func assertRemoteAuthorityPreserved(t *testing.T, database *sql.DB, beforeSchema map[string]string, beforeForeignKey string, beforeEvidenceCount int, beforeEvidenceDigest string, beforeRunCount int, beforeRunDigest string) {
	t.Helper()
	afterSchema, err := loadDurationLedgerSQLiteSchemaObjects(database)
	if err != nil {
		t.Fatal(err)
	}
	for object, ddl := range beforeSchema {
		if got := afterSchema[object]; got != ddl {
			t.Fatalf("remote schema object %s changed during additive migration: before=%q after=%q", object, ddl, got)
		}
	}
	if got := sqliteForeignKeyFingerprint(t, database, "ci_workload_pass_evidence"); got != beforeForeignKey {
		t.Fatalf("remote PASS evidence foreign keys changed during additive migration: before=%q after=%q", beforeForeignKey, got)
	}
	if got, digest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_workload_pass_evidence ORDER BY identity_digest, accepted_generation`); got != beforeEvidenceCount || digest != beforeEvidenceDigest {
		t.Fatalf("remote PASS evidence changed during additive migration: before=(%d,%s) after=(%d,%s)", beforeEvidenceCount, beforeEvidenceDigest, got, digest)
	}
	if got, digest := sqliteRowsFingerprint(t, database, `SELECT * FROM ci_runs ORDER BY job_id`); got != beforeRunCount || digest != beforeRunDigest {
		t.Fatalf("remote ci_runs changed during additive migration: before=(%d,%s) after=(%d,%s)", beforeRunCount, beforeRunDigest, got, digest)
	}
}

func TestDurationLedgerSQLiteV14MissingLocalStateFailsFastWithoutRebuild(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "missing-local-state.sqlite")
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM ci_local_authority_state`); err != nil {
		t.Fatal(err)
	}
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil || !strings.Contains(err.Error(), "local authority state is missing") {
		t.Fatalf("missing local state error = %v, want fail-fast", err)
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("missing local state refusal changed sqlite_master")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_local_authority_state`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing local state refusal rebuilt state row: count=%d", count)
	}
}

func TestDurationLedgerSQLiteUnknownSchemaVersionFailsFastWithoutRebuild(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "unknown-version.sqlite")
	defer database.Close()
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, time.Now, newDurationLedgerSQLiteSchemaValidator()); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown schema version error = %v, want fail-fast", err)
	}
	if got := durationLedgerSQLiteMasterForTest(t, database); got != beforeMaster {
		t.Fatalf("unknown schema version refusal changed sqlite_master")
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != 99 {
		t.Fatalf("unknown schema version refusal changed user_version to %d", got)
	}
}

func createDurationLedgerSQLiteV13Fixture(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range durationLedgerSQLiteLegacySchemaStatementsV13() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create v13 schema: %v", err)
		}
	}
}

func insertRemotePassFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO ci_runs(job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text) VALUES ('remote-fixture-job', 0, 'test', 'medium', 'sha256:plan', 'sha256:catalog', '1', 'snapshot-fixture', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', '', '', 'eci-fixture', 'passed', 1, 1700000000000, 1700000001000, 1, '')`); err != nil {
		t.Fatalf("insert remote run fixture: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO ci_workload_pass_evidence(identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES ('sha256:identity-fixture', '1', 'backend:fixture', 'sha256:execution-fixture', 'sha256:input-fixture', 'sha256:environment-fixture', 'remote-fixture-job', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'sha256:receipt-fixture', '{}', 'sha256:evidence-fixture')`); err != nil {
		t.Fatalf("insert remote evidence fixture: %v", err)
	}
}

func assertLocalAuthorityStateGeneration(t *testing.T, database *sql.DB, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow(`SELECT generation FROM ci_local_authority_state WHERE singleton = 1`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("local authority generation = %q, want %q", got, want)
	}
}

func sqliteRowsFingerprint(t *testing.T, database *sql.DB, query string) (int, string) {
	t.Helper()
	rows, err := database.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for index, value := range values {
			fmt.Fprintf(hash, "%d=%s\x00", index, sqliteFingerprintValue(value))
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count, "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sqliteForeignKeyFingerprint(t *testing.T, database *sql.DB, table string) string {
	t.Helper()
	rows, err := database.Query(`PRAGMA foreign_key_list('` + table + `')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for index, value := range values {
			fmt.Fprintf(hash, "%d=%s\x00", index, sqliteFingerprintValue(value))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sqliteFingerprintValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case []byte:
		return string(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}
