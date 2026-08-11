package gate

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalAuthorityRecordDoesNotRequireRemoteBaseline(t *testing.T) {
	store, batch, identity := localPassAuthorityFixture(t)
	database := openWorkloadPassDatabase(t, store)
	execLocalAuthorityRowsAffectedOne(t, database, "remote baseline delete", `DELETE FROM ci_remote_baseline_state`)
	execLocalAuthorityRowsAffectedOne(t, database, "local origin delete", `DELETE FROM ci_local_workload_origins`)
	database.Close()
	batch.Origin.RunID = "local-only-run"
	projection, err := LocalWorkloadPassProjectionDigest(batch.Origin, batch.Entries)
	if err != nil {
		t.Fatal(err)
	}
	batch.Origin.ProjectionDigest = projection
	if err := store.RecordLocalWorkloadPassBatch(batch); err != nil {
		t.Fatalf("local record without remote baseline: %v", err)
	}
	assertLocalAuthorityLookupHit(t, store, identity)
}

func TestLocalAuthorityStateMissingOrTamperedFailsFast(t *testing.T) {
	store, _, identity := localPassAuthorityFixture(t)
	database := openWorkloadPassDatabase(t, store)
	execLocalAuthorityRowsAffectedOne(t, database, "local authority delete", `DELETE FROM ci_local_authority_state`)
	database.Close()
	assertLocalAuthorityLookupError(t, store, identity, "missing local authority state", "local authority state")

	store, _, identity = localPassAuthorityFixture(t)
	database = openWorkloadPassDatabase(t, store)
	execLocalAuthorityRowsAffectedOne(t, database, "local authority tamper", `UPDATE ci_local_authority_state SET state_json = ?`, `{"domain":"local-authority-state/v1","generation":2}`)
	database.Close()
	assertLocalAuthorityLookupError(t, store, identity, "tampered local authority state", "projection digest")
}

func TestInitializeLocalAuthorityCreatesMissingCurrentAuthority(t *testing.T) {
	store := newLocalAuthorityStore(t, filepath.Join(t.TempDir(), "local.sqlite"))
	generation, err := store.InitializeLocalAuthority()
	if err != nil {
		t.Fatalf("initialize missing local authority: %v", err)
	}
	if generation != 1 {
		t.Fatalf("initialized local authority generation = %d, want 1", generation)
	}
	if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("clean local authority remote baseline error = %v, want missing", err)
	}
}

func TestInitializeLocalAuthorityRejectsMissingParent(t *testing.T) {
	_, err := NewDurationLedgerStore(filepath.Join(t.TempDir(), "missing", "local.sqlite"))
	if err == nil || !strings.Contains(err.Error(), "resolve duration ledger store parent") {
		t.Fatalf("create missing parent authority error = %v", err)
	}
}

func TestInitializeLocalAuthorityRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.sqlite")
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newLocalAuthorityStore(t, path)
	if _, err := store.InitializeLocalAuthority(); err == nil || !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("initialize corrupt authority error = %v", err)
	}
}

func TestInitializeLocalAuthorityRejectsUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.sqlite")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 999`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store := newLocalAuthorityStore(t, path)
	if _, err := store.InitializeLocalAuthority(); err == nil || !strings.Contains(err.Error(), "schema version 999 is unsupported") {
		t.Fatalf("initialize unknown schema error = %v", err)
	}
}

func newLocalAuthorityStore(t *testing.T, path string) *DurationLedgerStore {
	t.Helper()
	store, err := NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func execLocalAuthorityRowsAffectedOne(t *testing.T, database *sql.DB, label, statement string, args ...any) {
	t.Helper()
	result, err := database.Exec(statement, args...)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		database.Close()
		t.Fatalf("%s rows = %d err=%v, want 1", label, rows, err)
	}
}

func assertLocalAuthorityLookupHit(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	t.Helper()
	hits, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil || len(hits) != 1 {
		t.Fatalf("local lookup without remote baseline = %d err=%v", len(hits), err)
	}
}

func assertLocalAuthorityLookupError(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity, label, want string) {
	t.Helper()
	if _, err := store.LookupLocalWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s error = %v", label, err)
	}
}
