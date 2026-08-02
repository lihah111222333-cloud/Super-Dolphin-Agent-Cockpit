package gate

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRemoteBaselineStateV1OCIRecordRequiresExplicitMigration(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	createRemoteBaselineStateV1(t, database)
	if _, err := database.Exec(`
		INSERT INTO ci_remote_baseline_state(
			singleton,schema_version,generation,state_json,state_sha256,legacy_json,updated_at_unix_ms
		) VALUES(1,1,'7','{"oci":true}','sha256:state','',123)
	`); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = store.LoadRemoteBaselineState()
	if !errors.Is(err, ErrRemoteBaselineStateMigrationRequired) {
		t.Fatalf("LoadRemoteBaselineState() error = %v, want migration-required", err)
	}
	database, err = sql.Open("sqlite", durationLedgerSQLiteDSN(store.path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	columns := sqliteTableColumns(t, database, "ci_remote_baseline_state")
	if len(columns) != 7 || columns[5] != "legacy_json" {
		t.Fatalf("OCI v1 rejection changed table: %v", columns)
	}
	var schemaVersion uint32
	err = database.QueryRow(`SELECT schema_version FROM ci_remote_baseline_state WHERE singleton=1`).Scan(&schemaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 1 {
		t.Fatalf("OCI v1 rejection changed schema version to %d", schemaVersion)
	}
}

func TestRemoteBaselineStateV1LegacyRecordRequiresExplicitMigration(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	createRemoteBaselineStateV1(t, database)
	if _, err := database.Exec(`
		INSERT INTO ci_remote_baseline_state(
			singleton,schema_version,generation,state_json,state_sha256,legacy_json,updated_at_unix_ms
		) VALUES(1,1,'7','','','{"legacy":true}',123)
	`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = store.LoadRemoteBaselineState()
	if !errors.Is(err, ErrRemoteBaselineStateMigrationRequired) {
		t.Fatalf("LoadRemoteBaselineState() error = %v, want migration-required", err)
	}

	database, err = sql.Open("sqlite", durationLedgerSQLiteDSN(store.path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	columns := sqliteTableColumns(t, database, "ci_remote_baseline_state")
	if len(columns) != 7 || columns[5] != "legacy_json" {
		t.Fatalf("legacy migration changed v1 table despite rollback: %v", columns)
	}
}

func TestRemoteBaselineStateSQLiteFieldGuard(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	contract := remoteBaselineStateSQLiteProjectionContract()
	if err := validateSQLiteProjectionContract(contract, sqliteTableColumns(t, database, contract.table)); err != nil {
		t.Fatal(err)
	}

	delete(contract.columns, "StateSHA256")
	err = validateSQLiteProjectionContract(contract, sqliteTableColumns(t, database, contract.table))
	if err == nil || !strings.Contains(err.Error(), "StateSHA256") {
		t.Fatalf("field guard error = %v", err)
	}
}

func remoteBaselineStateSQLiteProjectionContract() sqliteProjectionContract {
	return sqliteProjectionContract{
		table:    "ci_remote_baseline_state",
		producer: reflect.TypeFor[RemoteBaselineStateRecord](),
		columns: map[string]string{
			"Generation":  "generation",
			"StateJSON":   "state_json",
			"StateSHA256": "state_sha256",
		},
		synthetic: map[string]string{
			"singleton":          "single-record authority key",
			"schema_version":     "OCI-only v2 contract marker",
			"updated_at_unix_ms": "persistence timestamp",
		},
	}
}

func createRemoteBaselineStateV1(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`DROP TABLE ci_remote_baseline_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE ci_remote_baseline_state (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			schema_version INTEGER NOT NULL CHECK (schema_version = 1),
			generation TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '',
			state_sha256 TEXT NOT NULL DEFAULT '',
			legacy_json TEXT NOT NULL DEFAULT '',
			updated_at_unix_ms INTEGER NOT NULL,
			CHECK (
				(state_json <> '' AND state_sha256 <> '' AND legacy_json = '') OR
				(state_json = '' AND state_sha256 = '' AND legacy_json <> '')
			)
		)
	`); err != nil {
		t.Fatal(err)
	}
}
