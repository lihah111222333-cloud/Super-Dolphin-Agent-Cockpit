package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// AppendRemoteRefreshDelta 原子追加一份只相对已接受 snapshot 的刷新增量证据。
func (store *DurationLedgerStore) AppendRemoteRefreshDelta(record RemoteRefreshDeltaRecord) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if err := validateRemoteRefreshDeltaRecord(record); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "remote refresh delta", func(transaction *sql.Tx) error {
		if err := verifySQLiteRefreshDeltaAuthority(transaction, record); err != nil {
			return err
		}
		existing, found, err := loadSQLiteRefreshDelta(transaction, record.JobID, record.AttemptGeneration, record.DeltaIdentity)
		if err != nil {
			return err
		}
		if found {
			if sameRemoteRefreshDeltaRecord(existing, record) {
				return nil
			}
			return errors.New("remote refresh delta duplicate conflicts with authority record")
		}
		query := fmt.Sprintf(`INSERT INTO %s (
			job_id, attempt_generation, accepted_generation, accepted_state_sha256, accepted_snapshot_id,
			delta_identity, delta_sha256, delta_size_bytes, target_tree_sha, target_closure_sha256,
			transfer_mode, recorded_at_unix_ms, lease_singleton
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`, cicontract.RefreshDeltasTable)
		if _, err := transaction.Exec(query,
			record.JobID, strconv.FormatUint(record.AttemptGeneration, 10), strconv.FormatUint(record.AcceptedGeneration, 10), record.AcceptedStateSHA256,
			record.AcceptedSnapshotID, record.DeltaIdentity, record.DeltaSHA256, record.DeltaSizeBytes,
			record.TargetTreeSHA, record.TargetClosureSHA256, string(record.TransferMode), record.RecordedAt.UTC().UnixMilli(),
		); err != nil {
			return mapDurationLedgerSQLiteError("append remote refresh delta", err)
		}
		return compactDurationLedgerAuthority(transaction)
	})
}

// LoadRemoteRefreshDeltas 恢复并验证指定 job/attempt 的全部刷新增量证据。
func (store *DurationLedgerStore) LoadRemoteRefreshDeltas(jobID string, attemptGeneration uint64) ([]RemoteRefreshDeltaRecord, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if strings.TrimSpace(jobID) == "" || attemptGeneration == 0 {
		return nil, errors.New("remote refresh delta job ID and attempt generation are required")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin remote refresh delta read snapshot", err)
	}
	defer transaction.Rollback()
	query := fmt.Sprintf(`SELECT job_id, attempt_generation, accepted_generation, accepted_state_sha256,
		accepted_snapshot_id, delta_identity, delta_sha256, delta_size_bytes, target_tree_sha,
		target_closure_sha256, transfer_mode, recorded_at_unix_ms
		FROM %s WHERE job_id = ? AND attempt_generation = ? ORDER BY delta_identity`, cicontract.RefreshDeltasTable)
	rows, err := transaction.Query(query, jobID, strconv.FormatUint(attemptGeneration, 10))
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote refresh deltas", err)
	}
	defer rows.Close()
	var records []RemoteRefreshDeltaRecord
	for rows.Next() {
		var attemptGenerationText, acceptedGenerationText, mode string
		var recordedAtMS int64
		var record RemoteRefreshDeltaRecord
		if err := rows.Scan(&record.JobID, &attemptGenerationText, &acceptedGenerationText, &record.AcceptedStateSHA256,
			&record.AcceptedSnapshotID, &record.DeltaIdentity, &record.DeltaSHA256, &record.DeltaSizeBytes,
			&record.TargetTreeSHA, &record.TargetClosureSHA256, &mode, &recordedAtMS); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote refresh delta", err)
		}
		var parseErr error
		record.AttemptGeneration, parseErr = strconv.ParseUint(attemptGenerationText, 10, 64)
		if parseErr != nil || record.AttemptGeneration == 0 {
			return nil, errors.New("stored remote refresh delta attempt generation is invalid")
		}
		record.AcceptedGeneration, parseErr = strconv.ParseUint(acceptedGenerationText, 10, 64)
		if parseErr != nil || record.AcceptedGeneration == 0 {
			return nil, errors.New("stored remote refresh delta accepted generation is invalid")
		}
		record.TransferMode = cicontract.RefreshTransferMode(mode)
		record.RecordedAt = unixMilliUTC(recordedAtMS)
		if err := validateRemoteRefreshDeltaRecord(record); err != nil {
			return nil, fmt.Errorf("stored remote refresh delta: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote refresh deltas", err)
	}
	if len(records) == 0 {
		return nil, errors.New("remote refresh delta is missing")
	}
	if err := transaction.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit remote refresh delta read snapshot", err)
	}
	return records, nil
}

// AppendCheckReceipts 原子追加一个完整、成功且不可重复的必跑检查回执集合。
func (store *DurationLedgerStore) AppendCheckReceipts(receipts []CheckReceiptRecord) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if err := validateCompletePassingCheckReceipts(receipts); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "check receipts", func(transaction *sql.Tx) error {
		if err := verifySQLiteCheckReceiptAuthority(transaction, receipts); err != nil {
			return err
		}
		query := fmt.Sprintf(`INSERT INTO %s (
			run_id, job_id, candidate_tree_sha, accepted_generation, accepted_snapshot_id, required_check,
			executed, passed, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256
		) VALUES (?, ?, ?, ?, ?, ?, 1, 1, ?, ?, ?, ?)`, cicontract.CheckReceiptsTable)
		for _, receipt := range receipts {
			if _, err := transaction.Exec(query, receipt.RunID, receipt.JobID, receipt.CandidateTreeSHA,
				strconv.FormatUint(receipt.AcceptedGeneration, 10), receipt.AcceptedSnapshotID, string(receipt.RequiredCheck),
				receipt.StartedAt.UTC().UnixMilli(), receipt.CompletedAt.UTC().UnixMilli(), receipt.Duration.Milliseconds(), receipt.ReceiptSHA256,
			); err != nil {
				return mapDurationLedgerSQLiteError("append check receipt", err)
			}
		}
		return compactDurationLedgerAuthority(transaction)
	})
}

// LoadCheckReceipts 恢复并验证一个 job 的完整、成功必跑检查集合。
func (store *DurationLedgerStore) LoadCheckReceipts(jobID string) ([]CheckReceiptRecord, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if strings.TrimSpace(jobID) == "" {
		return nil, errors.New("check receipt job ID is required")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin check receipt read snapshot", err)
	}
	defer transaction.Rollback()
	query := fmt.Sprintf(`SELECT run_id, job_id, candidate_tree_sha, accepted_generation, accepted_snapshot_id,
		required_check, executed, passed, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256
		FROM %s WHERE job_id = ?
		ORDER BY CASE required_check
			WHEN 'gate' THEN 1 WHEN 'normal' THEN 2 WHEN 'e2e' THEN 3
			WHEN 'race' THEN 4 WHEN 'frontend' THEN 5 WHEN 'dependency' THEN 6
			ELSE 7 END`, cicontract.CheckReceiptsTable)
	rows, err := transaction.Query(query, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query check receipts", err)
	}
	defer rows.Close()
	var receipts []CheckReceiptRecord
	for rows.Next() {
		var check, acceptedGenerationText string
		var executed, passed int
		var startedAtMS, completedAtMS, durationMS int64
		var receipt CheckReceiptRecord
		if err := rows.Scan(&receipt.RunID, &receipt.JobID, &receipt.CandidateTreeSHA, &acceptedGenerationText,
			&receipt.AcceptedSnapshotID, &check, &executed, &passed, &startedAtMS, &completedAtMS, &durationMS, &receipt.ReceiptSHA256); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan check receipt", err)
		}
		var parseErr error
		receipt.AcceptedGeneration, parseErr = strconv.ParseUint(acceptedGenerationText, 10, 64)
		if parseErr != nil || receipt.AcceptedGeneration == 0 {
			return nil, errors.New("stored check receipt accepted generation is invalid")
		}
		receipt.RequiredCheck = cicontract.RequiredCheck(check)
		receipt.Executed = executed == 1
		receipt.Passed = passed == 1
		receipt.StartedAt = unixMilliUTC(startedAtMS)
		receipt.CompletedAt = unixMilliUTC(completedAtMS)
		receipt.Duration = time.Duration(durationMS) * time.Millisecond
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate check receipts", err)
	}
	if err := validateCompletePassingCheckReceipts(receipts); err != nil {
		return nil, fmt.Errorf("stored check receipts: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit check receipt read snapshot", err)
	}
	return receipts, nil
}

func verifySQLiteRefreshDeltaAuthority(transaction *sql.Tx, record RemoteRefreshDeltaRecord) error {
	var attempt, generation, stateSHA, builderJobID, targetTree string
	var phase cicontract.RefreshPhase
	if err := transaction.QueryRow(`SELECT attempt_generation, accepted_generation, accepted_state_sha256, builder_job_id, target_tree_sha, phase
		FROM ci_remote_baseline_refresh_lease WHERE singleton = 1`).Scan(&attempt, &generation, &stateSHA, &builderJobID, &targetTree, &phase); err != nil {
		return mapDurationLedgerSQLiteError("load refresh delta authority", err)
	}
	attemptGeneration, attemptErr := strconv.ParseUint(attempt, 10, 64)
	acceptedGeneration, generationErr := strconv.ParseUint(generation, 10, 64)
	if attemptErr != nil || generationErr != nil || !cicontract.IsRefreshCandidatePhase(phase) || attemptGeneration == 0 || acceptedGeneration == 0 || attemptGeneration != record.AttemptGeneration || acceptedGeneration != record.AcceptedGeneration || stateSHA != record.AcceptedStateSHA256 || builderJobID != record.JobID || targetTree != record.TargetTreeSHA {
		return errors.New("remote refresh delta authority binding is stale or mismatched")
	}
	return nil
}

func loadSQLiteRefreshDelta(transaction *sql.Tx, jobID string, attemptGeneration uint64, deltaIdentity string) (RemoteRefreshDeltaRecord, bool, error) {
	query := fmt.Sprintf(`SELECT job_id, attempt_generation, accepted_generation, accepted_state_sha256,
		accepted_snapshot_id, delta_identity, delta_sha256, delta_size_bytes, target_tree_sha,
		target_closure_sha256, transfer_mode, recorded_at_unix_ms
		FROM %s WHERE job_id = ? AND attempt_generation = ? AND delta_identity = ?`, cicontract.RefreshDeltasTable)
	var record RemoteRefreshDeltaRecord
	var attemptText, acceptedText, mode string
	var recordedAtMS int64
	err := transaction.QueryRow(query, jobID, strconv.FormatUint(attemptGeneration, 10), deltaIdentity).Scan(
		&record.JobID, &attemptText, &acceptedText, &record.AcceptedStateSHA256, &record.AcceptedSnapshotID,
		&record.DeltaIdentity, &record.DeltaSHA256, &record.DeltaSizeBytes, &record.TargetTreeSHA,
		&record.TargetClosureSHA256, &mode, &recordedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteRefreshDeltaRecord{}, false, nil
	}
	if err != nil {
		return RemoteRefreshDeltaRecord{}, false, mapDurationLedgerSQLiteError("load remote refresh delta duplicate", err)
	}
	var parseErr error
	record.AttemptGeneration, parseErr = strconv.ParseUint(attemptText, 10, 64)
	if parseErr == nil {
		record.AcceptedGeneration, parseErr = strconv.ParseUint(acceptedText, 10, 64)
	}
	if parseErr != nil {
		return RemoteRefreshDeltaRecord{}, false, errors.New("stored remote refresh delta generation is invalid")
	}
	record.TransferMode = cicontract.RefreshTransferMode(mode)
	record.RecordedAt = unixMilliUTC(recordedAtMS)
	return record, true, nil
}

func sameRemoteRefreshDeltaRecord(left, right RemoteRefreshDeltaRecord) bool {
	return left.JobID == right.JobID && left.AttemptGeneration == right.AttemptGeneration && left.AcceptedGeneration == right.AcceptedGeneration &&
		left.AcceptedStateSHA256 == right.AcceptedStateSHA256 && left.AcceptedSnapshotID == right.AcceptedSnapshotID &&
		left.DeltaIdentity == right.DeltaIdentity && left.DeltaSHA256 == right.DeltaSHA256 && left.DeltaSizeBytes == right.DeltaSizeBytes &&
		left.TargetTreeSHA == right.TargetTreeSHA && left.TargetClosureSHA256 == right.TargetClosureSHA256 && left.TransferMode == right.TransferMode &&
		left.RecordedAt.UTC().UnixMilli() == right.RecordedAt.UTC().UnixMilli()
}

func verifySQLiteCheckReceiptAuthority(transaction *sql.Tx, receipts []CheckReceiptRecord) error {
	first := receipts[0]
	var tree string
	if err := transaction.QueryRow(`SELECT source_tree_sha FROM ci_runs WHERE job_id = ?`, first.JobID).Scan(&tree); err != nil {
		return mapDurationLedgerSQLiteError("load check receipt run authority", err)
	}
	if tree != first.CandidateTreeSHA {
		return errors.New("check receipt candidate tree does not match run authority")
	}
	return nil
}

func unixMilliUTC(value int64) time.Time { return time.UnixMilli(value).UTC() }
