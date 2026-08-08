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
	receipts, err := loadSQLiteCheckReceiptRecords(transaction, jobID)
	if err != nil {
		return nil, err
	}
	var catalogDigest string
	if err := transaction.QueryRow(`SELECT catalog_digest FROM ci_runs WHERE job_id = ?`, jobID).Scan(&catalogDigest); err != nil {
		return nil, mapDurationLedgerSQLiteError("load check receipt workload catalog identity", err)
	}
	if err := validateSQLiteWorkloadCatalogPassingCheckReceipts(transaction, catalogDigest, receipts); err != nil {
		return nil, fmt.Errorf("stored check receipts: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit check receipt read snapshot", err)
	}
	return receipts, nil
}

// loadSQLiteCheckReceiptRecords 在同一 SQLite 快照读取 canonical 顺序的回执，拒绝不完整或漂移字段。
func loadSQLiteCheckReceiptRecords(transaction *sql.Tx, jobID string) ([]CheckReceiptRecord, error) {
	query := fmt.Sprintf(`SELECT run_id, job_id, candidate_tree_sha, agent_token_digest, accepted_generation, accepted_snapshot_id,
		required_check, executed, reused, reuse_proof_sha256, passed, force, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256
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
		receipt, err := scanSQLiteCheckReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate check receipts", err)
	}
	return receipts, nil
}

// scanSQLiteCheckReceipt 将单行回执转换为完整记录，禁止无效 generation 或布尔编码进入验证层。
func scanSQLiteCheckReceipt(rows *sql.Rows) (CheckReceiptRecord, error) {
	var check, acceptedGenerationText string
	var executed, reused, passed, force int
	var startedAtMS, completedAtMS, durationMS int64
	var receipt CheckReceiptRecord
	if err := rows.Scan(&receipt.RunID, &receipt.JobID, &receipt.CandidateTreeSHA, &receipt.AgentTokenDigest, &acceptedGenerationText,
		&receipt.AcceptedSnapshotID, &check, &executed, &reused, &receipt.ReuseProofSHA256, &passed, &force, &startedAtMS, &completedAtMS, &durationMS, &receipt.ReceiptSHA256); err != nil {
		return CheckReceiptRecord{}, mapDurationLedgerSQLiteError("scan check receipt", err)
	}
	generation, err := strconv.ParseUint(acceptedGenerationText, 10, 64)
	if err != nil || generation == 0 {
		return CheckReceiptRecord{}, errors.New("stored check receipt accepted generation is invalid")
	}
	receipt.AcceptedGeneration = generation
	receipt.RequiredCheck = cicontract.RequiredCheck(check)
	receipt.Executed, receipt.Reused, receipt.Passed = executed == 1, reused == 1, passed == 1
	if force != 0 && force != 1 {
		return CheckReceiptRecord{}, errors.New("stored check receipt force identity is invalid")
	}
	receipt.Force = force == 1
	receipt.StartedAt, receipt.CompletedAt = unixMilliUTC(startedAtMS), unixMilliUTC(completedAtMS)
	receipt.Duration = time.Duration(durationMS) * time.Millisecond
	return receipt, nil
}

func verifySQLiteCheckReceiptAuthority(transaction *sql.Tx, receipts []CheckReceiptRecord) error {
	first := receipts[0]
	var tree, imageCacheSnapshotID, agentTokenDigest string
	var force int
	if err := transaction.QueryRow(`SELECT runs.source_tree_sha, runs.image_cache_snapshot_id, runs.force, identities.agent_token_digest FROM ci_runs AS runs INNER JOIN ci_run_agent_identities AS identities ON identities.job_id = runs.job_id WHERE runs.job_id = ?`, first.JobID).Scan(&tree, &imageCacheSnapshotID, &force, &agentTokenDigest); err != nil {
		return mapDurationLedgerSQLiteError("load check receipt run authority", err)
	}
	if tree != first.CandidateTreeSHA {
		return errors.New("check receipt candidate tree does not match run authority")
	}
	if imageCacheSnapshotID != first.AcceptedSnapshotID {
		return errors.New("check receipt accepted snapshot does not match run authority")
	}
	if force != 0 && force != 1 {
		return errors.New("check receipt run force identity is invalid")
	}
	if (force == 1) != first.Force {
		return errors.New("check receipt force mode does not match run authority")
	}
	return verifySQLiteCheckReceiptAgentIdentity(agentTokenDigest, first.AgentTokenDigest)
}

// verifySQLiteCheckReceiptAgentIdentity 拒绝回执携带与同一 SQLite run 不一致的 agent digest。
func verifySQLiteCheckReceiptAgentIdentity(storedDigest, receiptDigest string) error {
	if storedDigest != receiptDigest {
		return errors.New("check receipt agent token digest does not match run authority")
	}
	return nil
}

func unixMilliUTC(value int64) time.Time { return time.UnixMilli(value).UTC() }
