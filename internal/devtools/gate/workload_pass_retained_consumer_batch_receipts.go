package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// loadRetainedConsumerCheckReceipts 批量读取并严格验证每个 consumer 的 passing receipts。
func loadRetainedConsumerCheckReceipts(tx *sql.Tx, jobIDs []string, stats *workloadPassEvidenceLookupStats) (map[string][]CheckReceiptRecord, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT run_id, job_id, candidate_tree_sha, agent_token_digest, accepted_generation, accepted_snapshot_id, required_check, executed, reused, reuse_proof_sha256, passed, force, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256 FROM `+cicontract.CheckReceiptsTable+` WHERE job_id IN (`+placeholders+") ORDER BY job_id, required_check", stringsToAny(jobIDs)...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("batch load retained consumer check receipts", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	receipts := make(map[string][]CheckReceiptRecord, len(jobIDs))
	for rows.Next() {
		receipt, err := scanRetainedConsumerCheckReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts[receipt.JobID] = append(receipts[receipt.JobID], receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate retained consumer check receipts", err)
	}
	for _, jobID := range jobIDs {
		if err := validateStoredWorkloadReceiptCollection(receipts[jobID]); err != nil {
			return nil, fmt.Errorf("validate retained consumer %q check receipts: %w", jobID, err)
		}
	}
	return receipts, nil
}

// scanRetainedConsumerCheckReceipt 复用 receipt 的规范编码和时间还原规则。
func scanRetainedConsumerCheckReceipt(rows *sql.Rows) (CheckReceiptRecord, error) {
	var receipt CheckReceiptRecord
	var generation, check string
	var executed, reused, passed, force int
	var started, completed, duration int64
	if err := rows.Scan(&receipt.RunID, &receipt.JobID, &receipt.CandidateTreeSHA, &receipt.AgentTokenDigest, &generation, &receipt.AcceptedSnapshotID, &check, &executed, &reused, &receipt.ReuseProofSHA256, &passed, &force, &started, &completed, &duration, &receipt.ReceiptSHA256); err != nil {
		return CheckReceiptRecord{}, mapDurationLedgerSQLiteError("scan retained consumer check receipt", err)
	}
	if err := validateStoredWorkloadReceiptBooleans(executed, reused, passed); err != nil {
		return CheckReceiptRecord{}, err
	}
	accepted, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || accepted == 0 {
		return CheckReceiptRecord{}, errors.New("stored retained consumer receipt generation is invalid")
	}
	if force != 0 && force != 1 {
		return CheckReceiptRecord{}, errors.New("stored retained consumer receipt force encoding is invalid")
	}
	receipt.AcceptedGeneration, receipt.RequiredCheck = accepted, cicontract.RequiredCheck(check)
	receipt.Executed, receipt.Reused, receipt.Passed, receipt.Force = executed == 1, reused == 1, passed == 1, force == 1
	receipt.StartedAt, receipt.CompletedAt, receipt.Duration = time.UnixMilli(started).UTC(), time.UnixMilli(completed).UTC(), time.Duration(duration)*time.Millisecond
	return receipt, nil
}
