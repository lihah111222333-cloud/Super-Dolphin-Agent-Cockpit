package gate

import (
	"database/sql"
	"errors"
	"strconv"
)

var ErrRemoteBaselineStateNotFound = errors.New("remote baseline state is not initialized")
var ErrRemoteBaselineStateMigrationRequired = errors.New("remote baseline SQLite migration required")

type RemoteBaselineStateRecord struct {
	Generation  uint64
	StateJSON   []byte
	StateSHA256 string
}

func (store *DurationLedgerStore) LoadRemoteBaselineState() (RemoteBaselineStateRecord, error) {
	db, err := store.openSQLiteAuthority(false)
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	defer db.Close()
	return loadRemoteBaselineStateRecord(db)
}

func loadRemoteBaselineStateRecord(db *sql.DB) (RemoteBaselineStateRecord, error) {
	var generation string
	var schemaVersion uint32
	var record RemoteBaselineStateRecord
	err := db.QueryRow(`SELECT schema_version,generation,state_json,state_sha256 FROM ci_remote_baseline_state WHERE singleton=1`).Scan(&schemaVersion, &generation, &record.StateJSON, &record.StateSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteBaselineStateRecord{}, ErrRemoteBaselineStateNotFound
	}
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	value, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || value == 0 {
		return RemoteBaselineStateRecord{}, errors.New("remote baseline stored generation is invalid")
	}
	record.Generation = value
	if schemaVersion != 3 {
		return RemoteBaselineStateRecord{}, ErrRemoteBaselineStateMigrationRequired
	}
	if len(record.StateJSON) == 0 || record.StateSHA256 == "" {
		return RemoteBaselineStateRecord{}, errors.New("remote baseline stored record is invalid")
	}
	return record, nil
}
