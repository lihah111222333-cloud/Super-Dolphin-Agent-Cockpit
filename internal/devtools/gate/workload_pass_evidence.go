package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// WorkloadPassIdentity 将可复用 PASS 严格绑定到 workload、执行、输入和环境摘要。
type WorkloadPassIdentity struct {
	IdentityDigest    string `json:"identity_digest"`
	WorkloadID        GateID `json:"workload_id"`
	ExecutionDigest   string `json:"execution_digest"`
	InputDigest       string `json:"input_digest"`
	EnvironmentDigest string `json:"environment_digest"`
}

// WorkloadPassEvidence 保存已经提升的、可复查的 workload PASS 证据。
type WorkloadPassEvidence struct {
	Identity                 WorkloadPassIdentity `json:"identity"`
	OriginJobID              string               `json:"origin_job_id"`
	OriginAcceptedGeneration uint64               `json:"origin_accepted_generation"`
	OriginSourceTreeSHA      string               `json:"origin_source_tree_sha"`
	OriginReceiptSetSHA256   string               `json:"origin_receipt_set_sha256"`
	OriginExecution          PlanGateExecution    `json:"origin_execution"`
	EvidenceSHA256           string               `json:"evidence_sha256"`
}

// RemoteCIWorkloadResult 记录本次 run 的 executed 或经已提升证据复用的 workload。
type RemoteCIWorkloadResult struct {
	Identity                 WorkloadPassIdentity `json:"identity"`
	Disposition              string               `json:"disposition"`
	OriginJobID              string               `json:"origin_job_id"`
	OriginAcceptedGeneration uint64               `json:"origin_accepted_generation"`
	EvidenceSHA256           string               `json:"evidence_sha256"`
}

const (
	WorkloadDispositionExecuted = "executed"
	WorkloadDispositionReused   = "reused"
)

type workloadPassIdentityPayload struct {
	WorkloadID        GateID `json:"workload_id"`
	ExecutionDigest   string `json:"execution_digest"`
	InputDigest       string `json:"input_digest"`
	EnvironmentDigest string `json:"environment_digest"`
}

type workloadPassEvidencePayload struct {
	Identity                 WorkloadPassIdentity `json:"identity"`
	OriginJobID              string               `json:"origin_job_id"`
	OriginAcceptedGeneration uint64               `json:"origin_accepted_generation"`
	OriginSourceTreeSHA      string               `json:"origin_source_tree_sha"`
	OriginReceiptSetSHA256   string               `json:"origin_receipt_set_sha256"`
	OriginExecution          PlanGateExecution    `json:"origin_execution"`
}

// WorkloadPassIdentitySHA256 返回 identity 的规范 SHA-256，避免调用方自行拼接摘要。
func WorkloadPassIdentitySHA256(identity WorkloadPassIdentity) (string, error) {
	payload, err := json.Marshal(workloadPassIdentityPayload{identity.WorkloadID, identity.ExecutionDigest, identity.InputDigest, identity.EnvironmentDigest})
	if err != nil {
		return "", fmt.Errorf("encode workload pass identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// WorkloadPassEvidenceSHA256 对全部 origin 绑定和原始 execution 计算规范摘要。
func WorkloadPassEvidenceSHA256(evidence WorkloadPassEvidence) (string, error) {
	payload, err := json.Marshal(workloadPassEvidencePayload{evidence.Identity, evidence.OriginJobID, evidence.OriginAcceptedGeneration, evidence.OriginSourceTreeSHA, evidence.OriginReceiptSetSHA256, evidence.OriginExecution})
	if err != nil {
		return "", fmt.Errorf("encode workload pass evidence: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// validateWorkloadPassIdentity 校验 workload 身份的组成摘要和内容绑定摘要。
func validateWorkloadPassIdentity(identity WorkloadPassIdentity) error {
	if strings.TrimSpace(string(identity.WorkloadID)) == "" {
		return errors.New("workload pass identity workload ID is required")
	}
	for _, value := range []string{identity.IdentityDigest, identity.ExecutionDigest, identity.InputDigest, identity.EnvironmentDigest} {
		if !isPrefixedSHA256Digest(value) {
			return errors.New("workload pass identity digest is invalid")
		}
	}
	expected, err := WorkloadPassIdentitySHA256(identity)
	if err != nil {
		return err
	}
	if identity.IdentityDigest != expected {
		return errors.New("workload pass identity digest does not match content")
	}
	return nil
}

// Validate 严格校验 workload PASS 身份的规范摘要和全部组成摘要。
func (identity WorkloadPassIdentity) Validate() error { return validateWorkloadPassIdentity(identity) }

// validateWorkloadPassEvidence 校验证据来源、原始执行和内容绑定摘要。
func validateWorkloadPassEvidence(evidence WorkloadPassEvidence) error {
	if err := validateWorkloadPassIdentity(evidence.Identity); err != nil {
		return err
	}
	if err := validateWorkloadPassEvidenceOrigin(evidence); err != nil {
		return err
	}
	if err := validateWorkloadPassEvidenceExecution(evidence); err != nil {
		return err
	}
	expected, err := WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		return err
	}
	if evidence.EvidenceSHA256 != expected {
		return errors.New("workload pass evidence SHA-256 does not match content")
	}
	return nil
}

// validateWorkloadPassEvidenceOrigin 校验证据的权威来源标识和各项摘要。
func validateWorkloadPassEvidenceOrigin(evidence WorkloadPassEvidence) error {
	if evidence.OriginAcceptedGeneration == 0 || !validCalibrationOID(evidence.OriginSourceTreeSHA) || !isPrefixedSHA256Digest(evidence.OriginReceiptSetSHA256) || !isPrefixedSHA256Digest(evidence.EvidenceSHA256) || strings.TrimSpace(evidence.OriginJobID) == "" {
		return errors.New("workload pass evidence origin is invalid")
	}
	return nil
}

// validateWorkloadPassEvidenceExecution 校验证据绑定的完整通过分片执行。
func validateWorkloadPassEvidenceExecution(evidence WorkloadPassEvidence) error {
	if evidence.OriginExecution.GateID != evidence.Identity.WorkloadID {
		return errors.New("workload pass evidence execution does not match identity")
	}
	if evidence.OriginExecution.Status != ResultStatusPassed || evidence.OriginExecution.ExitCode != 0 || strings.TrimSpace(evidence.OriginExecution.ShardIdentity) == "" || evidence.OriginExecution.CompletedAt.Sub(evidence.OriginExecution.StartedAt) <= 0 || evidence.OriginExecution.CompletedAt.Sub(evidence.OriginExecution.StartedAt)%time.Millisecond != 0 {
		return errors.New("workload pass evidence requires a complete passing shard execution")
	}
	return validateRemoteCIRunWorkloadExecutions([]PlanGateExecution{evidence.OriginExecution})
}

// Validate 严格校验 workload PASS 证据的来源、执行记录和规范摘要。
func (evidence WorkloadPassEvidence) Validate() error { return validateWorkloadPassEvidence(evidence) }

// validateRemoteCIWorkloadResults 校验结果集合的身份唯一性及每项来源约束。
func validateRemoteCIWorkloadResults(results []RemoteCIWorkloadResult) error {
	seen := make(map[GateID]struct{}, len(results))
	for _, result := range results {
		if err := validateRemoteCIWorkloadResult(result); err != nil {
			return err
		}
		if _, exists := seen[result.Identity.WorkloadID]; exists {
			return fmt.Errorf("remote CI workload result %q is duplicated", result.Identity.WorkloadID)
		}
		seen[result.Identity.WorkloadID] = struct{}{}
	}
	return nil
}

// validateRemoteCIWorkloadResult 校验单项结果的身份、来源和执行或复用语义。
func validateRemoteCIWorkloadResult(result RemoteCIWorkloadResult) error {
	if err := validateWorkloadPassIdentity(result.Identity); err != nil {
		return err
	}
	if result.Disposition != WorkloadDispositionExecuted && result.Disposition != WorkloadDispositionReused {
		return fmt.Errorf("remote CI workload result %q disposition is invalid", result.Identity.WorkloadID)
	}
	if strings.TrimSpace(result.OriginJobID) == "" || result.OriginAcceptedGeneration == 0 {
		return errors.New("remote CI workload result origin is required")
	}
	if result.Disposition == WorkloadDispositionExecuted && result.EvidenceSHA256 != "" {
		return errors.New("executed workload result must not carry evidence SHA-256")
	}
	if result.Disposition == WorkloadDispositionReused && !isPrefixedSHA256Digest(result.EvidenceSHA256) {
		return errors.New("reused workload result evidence SHA-256 is invalid")
	}
	return nil
}

// Validate 严格校验单个远程 workload 结果；run 归属由写入路径继续核验。
func (result RemoteCIWorkloadResult) Validate() error {
	return validateRemoteCIWorkloadResults([]RemoteCIWorkloadResult{result})
}

// promoteSQLiteRemoteCIWorkloadPassEvidence 在同一写事务内验证并提升本次执行结果。
func promoteSQLiteRemoteCIWorkloadPassEvidence(tx *sql.Tx, jobID string) error {
	record, err := loadPromotableRemoteCIRun(tx, jobID)
	if err != nil {
		return err
	}
	receiptDigest, err := workloadReceiptSetSHA256(tx, record)
	if err != nil {
		return err
	}
	executions := indexWorkloadExecutions(record.WorkloadExecutions)
	if err := promoteExecutedWorkloadPassEvidence(tx, record, receiptDigest, executions); err != nil {
		return err
	}
	return nil
}

// loadPromotableRemoteCIRun 读取并验证可提升证据所需的完整远程运行记录。
func loadPromotableRemoteCIRun(tx *sql.Tx, jobID string) (RemoteCIRunRecord, error) {
	record, err := loadRemoteCIRunRow(tx, jobID)
	if err != nil {
		return RemoteCIRunRecord{}, err
	}
	if err := loadRemoteCIRunDetails(tx, jobID, &record); err != nil {
		return RemoteCIRunRecord{}, err
	}
	if record.Status != ResultStatusPassed || !record.Authoritative || !record.CleanupComplete {
		return RemoteCIRunRecord{}, errors.New("workload pass evidence promotion requires passed authoritative cleaned run")
	}
	if err := validateRemoteCIRunRecord(record); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("promote workload pass evidence run: %w", err)
	}
	return record, nil
}

// indexWorkloadExecutions 按 workload 标识建立本次运行的执行记录索引。
func indexWorkloadExecutions(executions []PlanGateExecution) map[GateID]PlanGateExecution {
	indexed := make(map[GateID]PlanGateExecution, len(executions))
	for _, execution := range executions {
		indexed[execution.GateID] = execution
	}
	return indexed
}

// promoteExecutedWorkloadPassEvidence 仅将本次真实执行的 workload 结果写入提升证据表。
func promoteExecutedWorkloadPassEvidence(
	tx *sql.Tx,
	record RemoteCIRunRecord,
	receiptDigest string,
	executions map[GateID]PlanGateExecution,
) error {
	for _, result := range record.WorkloadResults {
		if result.Disposition != WorkloadDispositionExecuted {
			continue
		}
		execution, ok := executions[result.Identity.WorkloadID]
		if !ok {
			return fmt.Errorf("executed workload result %q lacks execution", result.Identity.WorkloadID)
		}
		if err := insertWorkloadPassEvidence(tx, record, receiptDigest, result.Identity, execution); err != nil {
			return err
		}
	}
	return nil
}

// insertWorkloadPassEvidence 构造、摘要并以幂等方式写入单个提升证据。
func insertWorkloadPassEvidence(
	tx *sql.Tx,
	record RemoteCIRunRecord,
	receiptDigest string,
	identity WorkloadPassIdentity,
	execution PlanGateExecution,
) error {
	evidence := WorkloadPassEvidence{Identity: identity, OriginJobID: record.JobID, OriginAcceptedGeneration: record.AcceptedGeneration, OriginSourceTreeSHA: record.SourceTreeSHA, OriginReceiptSetSHA256: receiptDigest, OriginExecution: execution}
	var err error
	evidence.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(execution)
	if err != nil {
		return fmt.Errorf("encode origin workload execution: %w", err)
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, evidence.Identity.IdentityDigest, strconv.FormatUint(evidence.OriginAcceptedGeneration, 10), string(evidence.Identity.WorkloadID), evidence.Identity.ExecutionDigest, evidence.Identity.InputDigest, evidence.Identity.EnvironmentDigest, evidence.OriginJobID, evidence.OriginSourceTreeSHA, evidence.OriginReceiptSetSHA256, string(encoded), evidence.EvidenceSHA256); err != nil {
		return mapDurationLedgerSQLiteError("promote workload pass evidence", err)
	}
	return nil
}

// LookupWorkloadPassEvidence 仅返回当前 accepted 代及前两代中的最新保留证据。
func (store *DurationLedgerStore) LookupWorkloadPassEvidence(identities []WorkloadPassIdentity) ([]WorkloadPassEvidence, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if err := validateWorkloadPassIdentities(identities); err != nil {
		return nil, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin workload pass evidence lookup", err)
	}
	defer tx.Rollback()
	currentGeneration, err := loadCurrentAcceptedGenerationForWorkloadEvidence(tx)
	if err != nil {
		return nil, err
	}
	result, err := loadWorkloadPassEvidenceForIdentities(tx, identities, currentGeneration)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit workload pass evidence lookup", err)
	}
	return result, nil
}

// validateWorkloadPassIdentities 逐项校验查询请求中的内容绑定身份。
func validateWorkloadPassIdentities(identities []WorkloadPassIdentity) error {
	for _, identity := range identities {
		if err := validateWorkloadPassIdentity(identity); err != nil {
			return err
		}
	}
	return nil
}

// loadWorkloadPassEvidenceForIdentities 在固定 accepted 代窗口内读取每个身份的最新证据。
func loadWorkloadPassEvidenceForIdentities(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	currentGeneration uint64,
) ([]WorkloadPassEvidence, error) {
	result := make([]WorkloadPassEvidence, 0, len(identities))
	for _, identity := range identities {
		evidence, found, err := loadLatestWorkloadPassEvidence(tx, identity, currentGeneration)
		if err != nil {
			return nil, err
		}
		if found {
			result = append(result, evidence)
		}
	}
	return result, nil
}

// loadCurrentAcceptedGenerationForWorkloadEvidence 在同一只读事务内读取并验证 accepted 基线代。
func loadCurrentAcceptedGenerationForWorkloadEvidence(tx *sql.Tx) (uint64, error) {
	var schemaVersion uint32
	var storedGeneration, stateJSON, stateSHA256 string
	err := tx.QueryRow(`SELECT schema_version, generation, state_json, state_sha256 FROM ci_remote_baseline_state WHERE singleton = 1`).Scan(&schemaVersion, &storedGeneration, &stateJSON, &stateSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrRemoteBaselineStateNotFound
	}
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("load workload evidence accepted baseline generation", err)
	}
	current, parseErr := strconv.ParseUint(storedGeneration, 10, 64)
	if parseErr != nil || current == 0 || storedGeneration != strconv.FormatUint(current, 10) || schemaVersion != 3 {
		return 0, errors.New("accepted baseline generation authority is invalid")
	}
	if _, err := validateAcceptedBaselineStateProjection(stateJSON, stateSHA256, current); err != nil {
		return 0, err
	}
	return current, nil
}

// retainedWorkloadPassGenerations 返回当前 accepted 代及最多两个连续前代的规范存储值。
func retainedWorkloadPassGenerations(current uint64) [3]string {
	return [3]string{
		strconv.FormatUint(current, 10),
		strconv.FormatUint(current-min(current-1, 1), 10),
		strconv.FormatUint(current-min(current-1, 2), 10),
	}
}

// workloadReceiptSetSHA256 对完整 current check receipt 集合重算规范摘要。
func workloadReceiptSetSHA256(tx *sql.Tx, record RemoteCIRunRecord) (string, error) {
	receipts, err := loadCheckReceiptsForEvidence(tx, record.JobID)
	if err != nil {
		return "", err
	}
	if err := validateCompletePassingCheckReceipts(receipts); err != nil {
		return "", fmt.Errorf("stored complete check receipts: %w", err)
	}
	if receipts[0].JobID != record.JobID || receipts[0].AgentTokenDigest != record.AgentTokenDigest || receipts[0].CandidateTreeSHA != record.SourceTreeSHA || receipts[0].AcceptedGeneration != record.AcceptedGeneration || receipts[0].AcceptedSnapshotID != record.ImageCacheSnapshotID {
		return "", errors.New("check receipt set does not bind promotion run")
	}
	digests := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		digests = append(digests, receipt.ReceiptSHA256)
	}
	sort.Strings(digests)
	payload, err := json.Marshal(digests)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// loadCheckReceiptsForEvidence 从 SQLite 读取指定运行的全部 current check receipts。
func loadCheckReceiptsForEvidence(tx *sql.Tx, jobID string) ([]CheckReceiptRecord, error) {
	rows, err := tx.Query(`SELECT run_id, job_id, candidate_tree_sha, agent_token_digest, accepted_generation, accepted_snapshot_id, required_check, executed, reused, reuse_proof_sha256, passed, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256 FROM ci_check_receipts WHERE job_id = ? ORDER BY required_check`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query workload evidence check receipts", err)
	}
	defer rows.Close()
	var receipts []CheckReceiptRecord
	for rows.Next() {
		var receipt CheckReceiptRecord
		var generation, check string
		var executed, reused, passed int
		var started, completed, duration int64
		if err := rows.Scan(&receipt.RunID, &receipt.JobID, &receipt.CandidateTreeSHA, &receipt.AgentTokenDigest, &generation, &receipt.AcceptedSnapshotID, &check, &executed, &reused, &receipt.ReuseProofSHA256, &passed, &started, &completed, &duration, &receipt.ReceiptSHA256); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan workload evidence check receipt", err)
		}
		var parseErr error
		receipt.AcceptedGeneration, parseErr = strconv.ParseUint(generation, 10, 64)
		if parseErr != nil {
			return nil, errors.New("stored workload evidence receipt generation is invalid")
		}
		receipt.RequiredCheck = cicontract.RequiredCheck(check)
		receipt.Executed = executed == 1
		receipt.Reused = reused == 1
		receipt.Passed = passed == 1
		receipt.StartedAt = unixMilliUTC(started)
		receipt.CompletedAt = unixMilliUTC(completed)
		receipt.Duration = time.Duration(duration) * time.Millisecond
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate workload evidence check receipts", err)
	}
	return receipts, nil
}

// replaceSQLiteRemoteCIWorkloadResults 原子替换运行的 executed 或 reused workload 结果。
func replaceSQLiteRemoteCIWorkloadResults(tx *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := tx.Exec(`DELETE FROM ci_run_workload_results WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI workload results", err)
	}
	for _, result := range record.WorkloadResults {
		if result.Disposition == WorkloadDispositionExecuted && (result.OriginJobID != record.JobID || result.OriginAcceptedGeneration != record.AcceptedGeneration) {
			return fmt.Errorf("executed workload result %q must originate from this run", result.Identity.WorkloadID)
		}
		if result.Disposition == WorkloadDispositionReused {
			if err := verifySQLiteReusableWorkloadEvidence(tx, result); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO ci_run_workload_results (job_id, workload_id, identity_digest, execution_digest, input_digest, environment_digest, disposition, origin_job_id, origin_accepted_generation, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.JobID, string(result.Identity.WorkloadID), result.Identity.IdentityDigest, result.Identity.ExecutionDigest, result.Identity.InputDigest, result.Identity.EnvironmentDigest, result.Disposition, result.OriginJobID, strconv.FormatUint(result.OriginAcceptedGeneration, 10), result.EvidenceSHA256); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI workload result", err)
		}
	}
	return nil
}

// verifySQLiteReusableWorkloadEvidence 严格核对复用结果与已提升 SQLite 证据的身份。
func verifySQLiteReusableWorkloadEvidence(tx *sql.Tx, result RemoteCIWorkloadResult) error {
	var evidenceSHA, workloadID, executionDigest, inputDigest, environmentDigest string
	err := tx.QueryRow(`SELECT evidence_sha256, workload_id, execution_digest, input_digest, environment_digest FROM ci_workload_pass_evidence WHERE identity_digest = ? AND accepted_generation = ? AND origin_job_id = ?`, result.Identity.IdentityDigest, strconv.FormatUint(result.OriginAcceptedGeneration, 10), result.OriginJobID).Scan(&evidenceSHA, &workloadID, &executionDigest, &inputDigest, &environmentDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("reused workload result %q has no promoted evidence", result.Identity.WorkloadID)
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load reused workload evidence", err)
	}
	if evidenceSHA != result.EvidenceSHA256 {
		return fmt.Errorf("reused workload result %q evidence does not match promoted evidence", result.Identity.WorkloadID)
	}
	if workloadID != string(result.Identity.WorkloadID) || executionDigest != result.Identity.ExecutionDigest || inputDigest != result.Identity.InputDigest || environmentDigest != result.Identity.EnvironmentDigest {
		return fmt.Errorf("reused workload result %q identity does not match promoted evidence", result.Identity.WorkloadID)
	}
	return nil
}

// loadRemoteCIWorkloadResults 从 SQLite 恢复一个运行持久化的 workload 结果。
func loadRemoteCIWorkloadResults(tx *sql.Tx, jobID string) ([]RemoteCIWorkloadResult, error) {
	rows, err := tx.Query(`SELECT workload_id, identity_digest, execution_digest, input_digest, environment_digest, disposition, origin_job_id, origin_accepted_generation, evidence_sha256 FROM ci_run_workload_results WHERE job_id = ? ORDER BY workload_id`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI workload results", err)
	}
	defer rows.Close()
	var results []RemoteCIWorkloadResult
	for rows.Next() {
		var result RemoteCIWorkloadResult
		var workloadID, generation string
		if err := rows.Scan(&workloadID, &result.Identity.IdentityDigest, &result.Identity.ExecutionDigest, &result.Identity.InputDigest, &result.Identity.EnvironmentDigest, &result.Disposition, &result.OriginJobID, &generation, &result.EvidenceSHA256); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI workload result", err)
		}
		result.Identity.WorkloadID = GateID(workloadID)
		if result.OriginAcceptedGeneration, err = strconv.ParseUint(generation, 10, 64); err != nil || result.OriginAcceptedGeneration == 0 {
			return nil, errors.New("stored remote CI workload result generation is invalid")
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI workload results", err)
	}
	return results, nil
}

// loadLatestWorkloadPassEvidence 仅从当前 accepted 代三代窗口读取严格匹配身份的最新证据。
func loadLatestWorkloadPassEvidence(tx *sql.Tx, identity WorkloadPassIdentity, currentGeneration uint64) (WorkloadPassEvidence, bool, error) {
	retainedGenerations := retainedWorkloadPassGenerations(currentGeneration)
	row := tx.QueryRow(`SELECT evidence.accepted_generation, evidence.workload_id, evidence.execution_digest, evidence.input_digest, evidence.environment_digest, evidence.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, evidence.evidence_sha256 FROM ci_workload_pass_evidence AS evidence INNER JOIN ci_runs AS runs ON runs.job_id = evidence.origin_job_id AND runs.accepted_generation = evidence.accepted_generation AND runs.source_tree_sha = evidence.origin_source_tree_sha WHERE evidence.identity_digest = ? AND evidence.accepted_generation IN (?, ?, ?) AND runs.status = 'passed' AND runs.authoritative = 1 AND runs.cleanup_complete = 1 ORDER BY length(evidence.accepted_generation) DESC, evidence.accepted_generation DESC LIMIT 1`, identity.IdentityDigest, retainedGenerations[0], retainedGenerations[1], retainedGenerations[2])
	var evidence WorkloadPassEvidence
	var generation, workloadID, executionJSON string
	err := row.Scan(&generation, &workloadID, &evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest, &evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkloadPassEvidence{}, false, nil
	}
	if err != nil {
		return WorkloadPassEvidence{}, false, mapDurationLedgerSQLiteError("load workload pass evidence", err)
	}
	evidence.Identity.IdentityDigest = identity.IdentityDigest
	evidence.Identity.WorkloadID = GateID(workloadID)
	if !workloadPassIdentityMatches(evidence.Identity, identity) {
		return WorkloadPassEvidence{}, false, errors.New("stored workload pass evidence identity does not match lookup request")
	}
	evidence.OriginAcceptedGeneration, err = strconv.ParseUint(generation, 10, 64)
	if err != nil || evidence.OriginAcceptedGeneration == 0 {
		return WorkloadPassEvidence{}, false, errors.New("stored workload pass evidence generation is invalid")
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(currentGeneration, evidence.OriginAcceptedGeneration); err != nil {
		return WorkloadPassEvidence{}, false, err
	}
	if err = json.Unmarshal([]byte(executionJSON), &evidence.OriginExecution); err != nil {
		return WorkloadPassEvidence{}, false, fmt.Errorf("decode stored workload pass execution: %w", err)
	}
	if err := validateStoredWorkloadPassEvidence(tx, evidence); err != nil {
		return WorkloadPassEvidence{}, false, err
	}
	return evidence, true, nil
}

// validateStoredWorkloadPassEvidence 复核持久化证据的内容、运行来源和 current receipt 集合。
func validateStoredWorkloadPassEvidence(tx *sql.Tx, evidence WorkloadPassEvidence) error {
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("stored workload pass evidence: %w", err)
	}
	record, err := loadRemoteCIRunRow(tx, evidence.OriginJobID)
	if err != nil {
		return err
	}
	if evidence.OriginAcceptedGeneration != record.AcceptedGeneration || evidence.OriginSourceTreeSHA != record.SourceTreeSHA || record.ImageCacheSnapshotID == "" {
		return errors.New("stored workload pass evidence origin does not match origin run")
	}
	receiptDigest, err := workloadReceiptSetSHA256(tx, record)
	if err != nil || receiptDigest != evidence.OriginReceiptSetSHA256 {
		return errors.New("stored workload pass evidence receipt set is missing or tampered")
	}
	return nil
}

// workloadPassIdentityMatches 比较持久化身份和查询身份的全部内容绑定字段。
func workloadPassIdentityMatches(stored WorkloadPassIdentity, requested WorkloadPassIdentity) bool {
	return stored.WorkloadID == requested.WorkloadID &&
		stored.ExecutionDigest == requested.ExecutionDigest &&
		stored.InputDigest == requested.InputDigest &&
		stored.EnvironmentDigest == requested.EnvironmentDigest
}

// RemoteCIRunAuthorityIdentity 将最终化操作绑定到不可变的 CI 运行记录。
type RemoteCIRunAuthorityIdentity struct {
	JobID                        string
	AgentTokenDigest             string
	Entrypoint                   CIEntrypointID
	Profile                      Profile
	PlanDigest                   string
	CatalogDigest                string
	AcceptedGeneration           uint64
	ImageCacheSnapshotID         string
	SourceTreeSHA                string
	CandidateGateSourceSHA256    string
	CandidateGateToolchainSHA256 string
	RunnerImage                  string
	StartedAt                    time.Time
}

// FinalizeRemoteCIRunAuthorityWithSamples 在同一 SQLite 事务中追加样本与回执、验证提升资格、提升新鲜证据并完成最终 CAS。
func (store *DurationLedgerStore) FinalizeRemoteCIRunAuthorityWithSamples(identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample, promoteFresh bool) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if err := validateRemoteCIRunAuthorityFinalization(identity, receipts, samples); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "finalize remote CI run authority", func(tx *sql.Tx) error {
		return finalizeSQLiteRemoteCIRunAuthority(tx, store, identity, receipts, samples, promoteFresh, store.finalizeFault)
	})
}

// validateRemoteCIRunAuthorityFinalization 在事务开始前拒绝不完整或不匹配的最终化输入。
func validateRemoteCIRunAuthorityFinalization(identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample) error {
	if err := validateCompletePassingCheckReceipts(receipts); err != nil {
		return err
	}
	if len(samples) != 0 {
		if err := ValidateDurationLedger(DurationLedger{Version: durationLedgerVersion, Samples: samples}); err != nil {
			return fmt.Errorf("validate finalized duration samples: %w", err)
		}
	}
	if receipts[0].JobID != identity.JobID || receipts[0].CandidateTreeSHA != identity.SourceTreeSHA || receipts[0].AgentTokenDigest != identity.AgentTokenDigest || receipts[0].AcceptedGeneration != identity.AcceptedGeneration || receipts[0].AcceptedSnapshotID != identity.ImageCacheSnapshotID {
		return errors.New("check receipts do not match provisional remote CI run identity")
	}
	return nil
}

// finalizeSQLiteRemoteCIRunAuthority 在同一事务中按固定顺序完成权威提升及保留清理。
func finalizeSQLiteRemoteCIRunAuthority(tx *sql.Tx, store *DurationLedgerStore, identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample, promoteFresh bool, fault durationLedgerFinalizeFault) error {
	if store == nil {
		return errors.New("duration ledger store is required for finalization")
	}
	if err := verifySQLiteProvisionalRemoteCIRunForAuthority(tx, identity); err != nil {
		return err
	}
	if err := appendSQLiteRemoteCIRunAuthorityArtifacts(tx, identity, receipts, samples, fault); err != nil {
		return err
	}
	if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepCAS); err != nil {
		return err
	}
	if err := promoteSQLiteRemoteCIRunAuthorityCAS(tx, identity.JobID); err != nil {
		return err
	}
	if promoteFresh {
		if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepPromotion); err != nil {
			return err
		}
		if err := promoteSQLiteRemoteCIWorkloadPassEvidence(tx, identity.JobID); err != nil {
			return err
		}
	}
	if err := store.appendDurationLedgerObservationEvent(
		tx,
		durationLedgerObservationEventRemoteRunFinalize,
		identity.JobID,
		strconv.FormatUint(identity.AcceptedGeneration, 10),
		map[string]any{"identity": identity, "receipts": receipts, "samples": samples, "promote_fresh": promoteFresh},
	); err != nil {
		return err
	}
	return compactDurationLedgerAuthority(tx)
}

// verifySQLiteProvisionalRemoteCIRunForAuthority 在提升前于事务内重新验证 provisional 运行记录。
func verifySQLiteProvisionalRemoteCIRunForAuthority(tx *sql.Tx, identity RemoteCIRunAuthorityIdentity) error {
	record := RemoteCIRunRecord{JobID: identity.JobID, AgentTokenDigest: identity.AgentTokenDigest, Entrypoint: identity.Entrypoint, Profile: identity.Profile, PlanDigest: identity.PlanDigest, CatalogDigest: identity.CatalogDigest, AcceptedGeneration: identity.AcceptedGeneration, ImageCacheSnapshotID: identity.ImageCacheSnapshotID, SourceTreeSHA: identity.SourceTreeSHA, CandidateGateSourceSHA256: identity.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: identity.CandidateGateToolchainSHA256, RunnerImage: identity.RunnerImage, StartedAt: identity.StartedAt}
	if err := verifySQLiteRemoteCIRunIdentity(tx, record); err != nil {
		return err
	}
	stored, err := loadRemoteCIRunRow(tx, identity.JobID)
	if err != nil {
		return err
	}
	if err := loadRemoteCIRunDetails(tx, identity.JobID, &stored); err != nil {
		return err
	}
	stored.Authoritative = true
	if err := validateRemoteCIRunRecord(stored); err != nil {
		return fmt.Errorf("validate provisional remote CI run for authority promotion: %w", err)
	}
	return nil
}

// appendSQLiteRemoteCIRunAuthorityArtifacts 在 CAS 前按顺序写入样本并验证、写入和重载回执。
func appendSQLiteRemoteCIRunAuthorityArtifacts(tx *sql.Tx, identity RemoteCIRunAuthorityIdentity, receipts []CheckReceiptRecord, samples []DurationSample, fault durationLedgerFinalizeFault) error {
	if len(samples) != 0 {
		if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepAppendSamples); err != nil {
			return err
		}
		if _, err := appendSQLiteDurationSamplesInTransaction(tx, identity.AcceptedGeneration, samples); err != nil {
			return err
		}
	}
	if err := verifySQLiteCheckReceiptAuthority(tx, receipts); err != nil {
		return err
	}
	if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepAppendReceipts); err != nil {
		return err
	}
	if err := appendSQLiteCheckReceipts(tx, receipts); err != nil {
		return err
	}
	if err := invokeDurationLedgerFinalizeFault(fault, durationLedgerFinalizeStepReloadReceipts); err != nil {
		return err
	}
	return verifySQLiteCheckReceiptReload(tx, identity.JobID, receipts)
}

func invokeDurationLedgerFinalizeFault(fault durationLedgerFinalizeFault, step durationLedgerFinalizeStep) error {
	if fault == nil {
		return nil
	}
	if err := fault(step); err != nil {
		return fmt.Errorf("finalize remote CI run authority %q: %w", step, err)
	}
	return nil
}

// promoteSQLiteRemoteCIRunAuthorityCAS 仅将满足终态条件的 provisional 记录原子提升为权威记录。
func promoteSQLiteRemoteCIRunAuthorityCAS(tx *sql.Tx, jobID string) error {
	updated, err := tx.Exec(`UPDATE ci_runs SET authoritative = 1 WHERE job_id = ? AND authoritative = 0 AND status = ? AND cleanup_complete = 1`, jobID, string(ResultStatusPassed))
	if err != nil {
		return mapDurationLedgerSQLiteError("promote remote CI run authority", err)
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return errors.New("remote CI run authority CAS did not update exactly one provisional run")
	}
	return nil
}

func appendSQLiteCheckReceipts(tx *sql.Tx, receipts []CheckReceiptRecord) error {
	query := fmt.Sprintf(`INSERT INTO %s (run_id, job_id, candidate_tree_sha, agent_token_digest, accepted_generation, accepted_snapshot_id, required_check, executed, reused, reuse_proof_sha256, passed, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`, cicontract.CheckReceiptsTable)
	for _, receipt := range receipts {
		if _, err := tx.Exec(query, receipt.RunID, receipt.JobID, receipt.CandidateTreeSHA, receipt.AgentTokenDigest, strconv.FormatUint(receipt.AcceptedGeneration, 10), receipt.AcceptedSnapshotID, string(receipt.RequiredCheck), boolToSQLite(receipt.Executed), boolToSQLite(receipt.Reused), receipt.ReuseProofSHA256, receipt.StartedAt.UTC().UnixMilli(), receipt.CompletedAt.UTC().UnixMilli(), receipt.Duration.Milliseconds(), receipt.ReceiptSHA256); err != nil {
			return mapDurationLedgerSQLiteError("append check receipt", err)
		}
	}
	return nil
}

// verifySQLiteCheckReceiptReload 逐项核对本次事务回读的回执摘要，拒绝缺失、额外或漂移记录。
func verifySQLiteCheckReceiptReload(tx *sql.Tx, jobID string, want []CheckReceiptRecord) error {
	rows, err := tx.Query(fmt.Sprintf(`SELECT required_check, receipt_sha256 FROM %s WHERE job_id = ?`, cicontract.CheckReceiptsTable), jobID)
	if err != nil {
		return mapDurationLedgerSQLiteError("reload check receipts", err)
	}
	defer rows.Close()
	wantByCheck := make(map[cicontract.RequiredCheck]string, len(want))
	for _, receipt := range want {
		wantByCheck[receipt.RequiredCheck] = receipt.ReceiptSHA256
	}
	for rows.Next() {
		var check cicontract.RequiredCheck
		var digest string
		if err := rows.Scan(&check, &digest); err != nil {
			return mapDurationLedgerSQLiteError("scan reloaded check receipt", err)
		}
		if wantByCheck[check] != digest {
			return errors.New("reloaded check receipt does not exactly match this invocation")
		}
		delete(wantByCheck, check)
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate reloaded check receipts", err)
	}
	if len(wantByCheck) != 0 {
		return errors.New("reloaded check receipt collection is incomplete")
	}
	return nil
}
