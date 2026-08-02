package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"testing"
)

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
