package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRemoteBaselineStateStoreInitializesEmptySQLiteAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	store, err := NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("LoadRemoteBaselineState() error = %v, want ErrRemoteBaselineStateNotFound", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat initialized SQLite authority: %v", err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, name := range []string{
		"idx_ci_workload_pass_evidence_origin_job",
		"idx_ci_workload_executions_shard_fk",
	} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("initialized SQLite authority index %q count = %d, want 1", name, count)
		}
	}
}

func TestRemoteBaselineStateStoreLoadsSeededState(t *testing.T) {
	data := []byte(`{"image_cache_id":"imc-accepted"}`)
	sum := sha256.Sum256(data)
	store, err := NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	seedRemoteBaselineState(t, store, RemoteBaselineStateRecord{Generation: 1, StateJSON: data, StateSHA256: "sha256:" + hex.EncodeToString(sum[:])})
	record, err := store.LoadRemoteBaselineState()
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 1 || string(record.StateJSON) != string(data) || record.StateSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("loaded seeded baseline record = %#v", record)
	}
}

func seedRemoteBaselineState(t *testing.T, store *DurationLedgerStore, record RemoteBaselineStateRecord) {
	t.Helper()
	if store == nil || record.Generation == 0 || len(record.StateJSON) == 0 || record.StateSHA256 == "" {
		t.Fatal("remote baseline SQL fixture is invalid")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO ci_remote_baseline_state(singleton,schema_version,generation,state_json,state_sha256,updated_at_unix_ms) VALUES(1,3,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET schema_version=excluded.schema_version,generation=excluded.generation,state_json=excluded.state_json,state_sha256=excluded.state_sha256,updated_at_unix_ms=excluded.updated_at_unix_ms`, strconv.FormatUint(record.Generation, 10), string(record.StateJSON), record.StateSHA256, store.nowFunc().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}
