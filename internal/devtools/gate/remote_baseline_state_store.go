package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func (store *DurationLedgerStore) CompareAndSwapRemoteBaselineState(expected uint64, record RemoteBaselineStateRecord) (uint64, error) {
	if store == nil || record.Generation == 0 || len(record.StateJSON) == 0 || record.StateSHA256 == "" {
		return 0, errors.New("remote baseline record is invalid")
	}
	db, err := store.openSQLiteAuthority(true)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var currentText string
	err = tx.QueryRow("SELECT generation FROM ci_remote_baseline_state WHERE singleton=1").Scan(&currentText)
	if errors.Is(err, sql.ErrNoRows) {
		if expected != 0 {
			return 0, durationLedgerConflict(expected, 0)
		}
	} else if err != nil {
		return 0, err
	} else {
		current, e := strconv.ParseUint(currentText, 10, 64)
		if e != nil {
			return 0, fmt.Errorf("decode remote baseline generation: %w", e)
		}
		if current != expected {
			return 0, durationLedgerConflict(expected, current)
		}
	}
	if _, err = tx.Exec(`INSERT INTO ci_remote_baseline_state(singleton,schema_version,generation,state_json,state_sha256,updated_at_unix_ms) VALUES(1,2,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET schema_version=excluded.schema_version,generation=excluded.generation,state_json=excluded.state_json,state_sha256=excluded.state_sha256,updated_at_unix_ms=excluded.updated_at_unix_ms`, strconv.FormatUint(record.Generation, 10), string(record.StateJSON), record.StateSHA256, store.nowFunc().UTC().UnixMilli()); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return record.Generation, nil
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
	if schemaVersion != 2 {
		return RemoteBaselineStateRecord{}, ErrRemoteBaselineStateMigrationRequired
	}
	if len(record.StateJSON) == 0 || record.StateSHA256 == "" {
		return RemoteBaselineStateRecord{}, errors.New("remote baseline stored record is invalid")
	}
	return record, nil
}
