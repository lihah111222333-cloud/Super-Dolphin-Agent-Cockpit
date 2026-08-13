package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LoadAuthoritativeRemoteCIRunWorkloadCoverage 从同一 SQLite 快照恢复一次完整权威运行的 workload PASS 身份。
func (store *DurationLedgerStore) LoadAuthoritativeRemoteCIRunWorkloadCoverage(
	entrypoint CIEntrypointID,
	profile Profile,
	catalogDigest string,
	sourceTreeSHA string,
	acceptedSnapshotID string,
) ([]WorkloadPassIdentity, bool, error) {
	if err := validateAuthoritativeRemoteCIRunCoverageRequest(store, entrypoint, profile, catalogDigest, sourceTreeSHA, acceptedSnapshotID); err != nil {
		return nil, false, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, false, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, mapDurationLedgerSQLiteError("begin authoritative remote CI workload coverage read", err)
	}
	defer tx.Rollback()
	coverage, found, err := loadAuthoritativeRemoteCIRunWorkloadCoverage(tx, entrypoint, profile, catalogDigest, sourceTreeSHA, acceptedSnapshotID)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, mapDurationLedgerSQLiteError("commit authoritative remote CI workload coverage read", err)
	}
	return coverage, found, nil
}

func validateAuthoritativeRemoteCIRunCoverageRequest(store *DurationLedgerStore, entrypoint CIEntrypointID, profile Profile, catalogDigest, sourceTreeSHA, acceptedSnapshotID string) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if err := entrypoint.Validate(); err != nil {
		return fmt.Errorf("authoritative remote CI workload coverage entrypoint: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("authoritative remote CI workload coverage profile: %w", err)
	}
	if !isPrefixedSHA256Digest(catalogDigest) {
		return errors.New("authoritative remote CI workload coverage catalog digest is invalid")
	}
	if !validCalibrationOID(sourceTreeSHA) {
		return errors.New("authoritative remote CI workload coverage source tree is invalid")
	}
	if strings.TrimSpace(acceptedSnapshotID) == "" {
		return errors.New("authoritative remote CI workload coverage accepted snapshot is required")
	}
	return nil
}

func loadAuthoritativeRemoteCIRunWorkloadCoverage(tx *sql.Tx, entrypoint CIEntrypointID, profile Profile, catalogDigest, sourceTreeSHA, acceptedSnapshotID string) ([]WorkloadPassIdentity, bool, error) {
	var jobID string
	err := tx.QueryRow(`SELECT job_id FROM ci_runs
		WHERE entrypoint = ? AND profile = ? AND catalog_digest = ? AND source_tree_sha = ?
			AND image_cache_snapshot_id = ? AND status = ? AND authoritative = 1
			AND cleanup_complete = 1 AND error_text = ''
		ORDER BY completed_at_unix_ms DESC, job_id DESC LIMIT 1`,
		string(entrypoint), string(profile), catalogDigest, sourceTreeSHA, acceptedSnapshotID, string(ResultStatusPassed),
	).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, mapDurationLedgerSQLiteError("query authoritative remote CI workload coverage run", err)
	}
	coverage, err := validateAuthoritativeRemoteCIRunWorkloadCoverage(tx, jobID, catalogDigest)
	if err != nil {
		return nil, false, err
	}
	return coverage, true, nil
}

func validateAuthoritativeRemoteCIRunWorkloadCoverage(tx *sql.Tx, jobID, catalogDigest string) ([]WorkloadPassIdentity, error) {
	record, err := loadAuthoritativeRemoteCIRunCoverageRecord(tx, jobID)
	if err != nil {
		return nil, err
	}
	if _, err := workloadReceiptSetSHA256(tx, record); err != nil {
		return nil, fmt.Errorf("validate authoritative remote CI workload coverage receipts: %w", err)
	}
	catalog, err := loadSQLiteWorkloadCatalog(tx, catalogDigest)
	if err != nil {
		return nil, fmt.Errorf("load authoritative remote CI workload coverage catalog: %w", err)
	}
	identities, executed, err := authoritativeRemoteCIRunWorkloadPassIdentities(record, catalog.Catalog)
	if err != nil {
		return nil, err
	}
	if err := validateAuthoritativeRemoteCIRunCoverageProofs(tx, record, executed); err != nil {
		return nil, err
	}
	return identities, nil
}

func loadAuthoritativeRemoteCIRunCoverageRecord(tx *sql.Tx, jobID string) (RemoteCIRunRecord, error) {
	record, err := loadRemoteCIRunRow(tx, jobID)
	if err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("load authoritative remote CI workload coverage run: %w", err)
	}
	record.Scope, err = loadRemoteCIExecutionScope(tx, jobID, record.AcceptedGeneration)
	if err != nil {
		return RemoteCIRunRecord{}, err
	}
	if record.Scope != nil {
		return RemoteCIRunRecord{}, errors.New("authoritative remote CI calibration coverage requires a full run")
	}
	record.WorkloadResults, err = loadRemoteCIWorkloadResults(tx, jobID)
	if err != nil {
		return RemoteCIRunRecord{}, err
	}
	if err := validateAuthoritativeRemoteCIRunCoverageRecord(record); err != nil {
		return RemoteCIRunRecord{}, err
	}
	return record, nil
}

func validateAuthoritativeRemoteCIRunCoverageRecord(record RemoteCIRunRecord) error {
	if record.Status != ResultStatusPassed || !record.Authoritative || !record.CleanupComplete || record.ErrorText != "" {
		return errors.New("authoritative remote CI calibration coverage requires a passed authoritative cleaned run")
	}
	if err := validateRemoteCIRunIdentity(record); err != nil {
		return fmt.Errorf("validate authoritative remote CI workload coverage identity: %w", err)
	}
	if err := validateRemoteCIWorkloadResults(record.WorkloadResults); err != nil {
		return fmt.Errorf("validate authoritative remote CI workload coverage results: %w", err)
	}
	return nil
}

func validateAuthoritativeRemoteCIRunCoverageProofs(tx *sql.Tx, record RemoteCIRunRecord, executed []WorkloadPassIdentity) error {
	if err := verifySQLiteRetainedWorkloadPassProofs(tx, record); err != nil {
		return fmt.Errorf("validate authoritative remote CI workload coverage reuse proofs: %w", err)
	}
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return fmt.Errorf("load authoritative remote CI workload coverage accepted generation: %w", err)
	}
	evidence, err := loadWorkloadPassEvidenceForIdentitiesWithStats(tx, executed, currentGeneration, nil)
	if err != nil {
		return fmt.Errorf("validate authoritative remote CI workload coverage PASS proofs: %w", err)
	}
	if len(evidence) != len(executed) {
		return fmt.Errorf("authoritative remote CI workload coverage executed PASS proofs are incomplete: identities=%d proofs=%d", len(executed), len(evidence))
	}
	return nil
}

func authoritativeRemoteCIRunWorkloadPassIdentities(record RemoteCIRunRecord, catalog WorkloadCatalog) ([]WorkloadPassIdentity, []WorkloadPassIdentity, error) {
	canonical := indexCanonicalWorkloadPassCatalog(catalog)
	expected := make(map[GateID]struct{}, len(canonical))
	for workloadID, workload := range canonical {
		if workload.Shardable {
			expected[workloadID] = struct{}{}
		}
	}
	identities := make([]WorkloadPassIdentity, 0, len(record.WorkloadResults))
	executed := make([]WorkloadPassIdentity, 0, len(record.WorkloadExecutions))
	for _, result := range record.WorkloadResults {
		if err := validateAuthoritativeRemoteCIRunCoverageResult(record, result); err != nil {
			return nil, nil, err
		}
		if err := validateCanonicalWorkloadPassIdentity(result.Identity, canonical); err != nil {
			return nil, nil, fmt.Errorf("authoritative remote CI workload coverage identity: %w", err)
		}
		if result.Disposition == WorkloadDispositionExecuted {
			executed = append(executed, result.Identity)
		}
		delete(expected, result.Identity.WorkloadID)
		identities = append(identities, result.Identity)
	}
	if len(expected) != 0 {
		return nil, nil, errors.New("authoritative remote CI workload coverage results are incomplete")
	}
	return identities, executed, nil
}

func validateAuthoritativeRemoteCIRunCoverageResult(record RemoteCIRunRecord, result RemoteCIWorkloadResult) error {
	if result.Disposition == WorkloadDispositionExecuted && (result.OriginJobID != record.JobID || result.OriginAcceptedGeneration != record.AcceptedGeneration) {
		return fmt.Errorf("authoritative remote CI workload coverage executed result %q origin does not match run", result.Identity.WorkloadID)
	}
	return nil
}
