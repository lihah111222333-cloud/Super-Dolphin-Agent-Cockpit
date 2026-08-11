package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// remoteCIExecutionScopeRow is the store-owned side-table projection used by
// the SQLite field guard. JSON and digest are derived from RemoteCIExecutionScope.
type remoteCIExecutionScopeRow struct {
	JobID              string
	AcceptedGeneration string
	ScopeJSON          string
	ScopeDigest        string
	ScopeCount         int
}

func loadRemoteCIExecutionScope(queryer sqliteRowQueryer, jobID string, acceptedGeneration uint64) (*RemoteCIExecutionScope, error) {
	var row remoteCIExecutionScopeRow
	err := queryer.QueryRow(`
		SELECT job_id, accepted_generation, scope_json, scope_digest, scope_count
		FROM ci_remote_run_execution_scopes
		WHERE job_id = ?
	`, jobID).Scan(&row.JobID, &row.AcceptedGeneration, &row.ScopeJSON, &row.ScopeDigest, &row.ScopeCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("load remote CI execution scope", err)
	}
	if row.JobID != jobID {
		return nil, errors.New("remote CI execution scope job binding is invalid")
	}
	if row.AcceptedGeneration != strconv.FormatUint(acceptedGeneration, 10) {
		return nil, errors.New("remote CI execution scope accepted generation does not match run")
	}
	scope, err := decodeRemoteCIExecutionScope(row.ScopeJSON, row.ScopeDigest)
	if err != nil {
		return nil, fmt.Errorf("validate stored remote CI execution scope: %w", err)
	}
	if row.ScopeCount != len(scope.selectedGateIDs) {
		return nil, errors.New("remote CI execution scope count does not match content")
	}
	return &scope, nil
}

func verifySQLiteRemoteCIExecutionScopeIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	stored, err := loadRemoteCIExecutionScope(transaction, record.JobID, record.AcceptedGeneration)
	if err != nil {
		return err
	}
	if stored == nil {
		if record.Scope != nil && record.Scope.IsSubset() {
			return fmt.Errorf("remote CI job %q execution scope row is missing", record.JobID)
		}
		return nil
	}
	if record.Scope == nil || record.Scope.IsFull() {
		return fmt.Errorf("remote CI job %q has a persisted subset execution scope but caller supplied full scope", record.JobID)
	}
	if !remoteCIExecutionScopesEqual(record.Scope, stored) {
		return fmt.Errorf("remote CI job %q conflicts with immutable execution scope", record.JobID)
	}
	return nil
}

// insertSQLiteRemoteCIExecutionScope writes only subset scope rows. A plain
// INSERT collision is accepted only when every proof column is byte-identical.
func insertSQLiteRemoteCIExecutionScope(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if record.Scope == nil || record.Scope.IsFull() {
		return rejectPersistedRemoteCIExecutionScope(transaction, record)
	}
	return insertSQLiteSubsetRemoteCIExecutionScope(transaction, record)
}

func rejectPersistedRemoteCIExecutionScope(transaction *sql.Tx, record RemoteCIRunRecord) error {
	stored, err := loadRemoteCIExecutionScope(transaction, record.JobID, record.AcceptedGeneration)
	if err != nil {
		return err
	}
	if stored != nil {
		return fmt.Errorf("remote CI job %q has an immutable subset execution scope", record.JobID)
	}
	return nil
}

func insertSQLiteSubsetRemoteCIExecutionScope(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if err := record.Scope.Validate(); err != nil {
		return err
	}
	scopeJSON, err := record.Scope.CanonicalJSON()
	if err != nil {
		return err
	}
	scopeDigest, err := record.Scope.Digest()
	if err != nil {
		return err
	}
	generation := strconv.FormatUint(record.AcceptedGeneration, 10)
	scopeCount := len(record.Scope.selectedGateIDs)
	_, err = transaction.Exec(`
		INSERT INTO ci_remote_run_execution_scopes (job_id, accepted_generation, scope_json, scope_digest, scope_count)
		VALUES (?, ?, ?, ?, ?)
	`, record.JobID, generation, scopeJSON, scopeDigest, scopeCount)
	if err == nil {
		return nil
	}
	if !isSQLiteConstraintError(err) {
		return mapDurationLedgerSQLiteError("store remote CI execution scope", err)
	}
	return verifySQLiteRemoteCIExecutionScopeCollision(transaction, record.JobID, generation, scopeJSON, scopeDigest, scopeCount)
}

func verifySQLiteRemoteCIExecutionScopeCollision(transaction *sql.Tx, jobID, generation, scopeJSON, scopeDigest string, scopeCount int) error {
	var stored remoteCIExecutionScopeRow
	err := transaction.QueryRow(`
		SELECT job_id, accepted_generation, scope_json, scope_digest, scope_count
		FROM ci_remote_run_execution_scopes WHERE job_id = ?
	`, jobID).Scan(&stored.JobID, &stored.AcceptedGeneration, &stored.ScopeJSON, &stored.ScopeDigest, &stored.ScopeCount)
	if err != nil {
		return mapDurationLedgerSQLiteError("reload conflicting remote CI execution scope", err)
	}
	if stored.JobID != jobID || stored.AcceptedGeneration != generation || stored.ScopeJSON != scopeJSON || stored.ScopeDigest != scopeDigest || stored.ScopeCount != scopeCount {
		return errors.New("conflicting remote CI execution scope proof")
	}
	if _, err := decodeRemoteCIExecutionScope(stored.ScopeJSON, stored.ScopeDigest); err != nil {
		return fmt.Errorf("validate conflicting remote CI execution scope: %w", err)
	}
	return nil
}
