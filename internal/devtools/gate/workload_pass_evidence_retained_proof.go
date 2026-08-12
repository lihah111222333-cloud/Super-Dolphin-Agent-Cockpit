package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// verifySQLiteRetainedWorkloadPassProofs 批量验证 consumer proof；仍存活的直接
// origin 必须在 CAS 前重新通过完整投影校验，已压缩 origin 则只消费 v16 proof。
func verifySQLiteRetainedWorkloadPassProofs(tx *sql.Tx, consumer RemoteCIRunRecord) error {
	current, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return fmt.Errorf("load retained workload pass proof accepted generation: %w", err)
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(current, consumer.AcceptedGeneration); err != nil {
		return fmt.Errorf("retained workload pass proof consumer generation: %w", err)
	}
	evidence, err := loadSQLiteRetainedWorkloadPassProofs(tx, consumer.JobID, consumer.WorkloadResults)
	if err != nil || len(evidence) == 0 {
		return err
	}
	origins := make(map[string]workloadPassEvidenceOriginContext, len(evidence))
	present := make(map[string]struct{}, len(evidence))
	if err := loadDirectWorkloadPassOriginBatchesWithPresence(tx, evidence, current, origins, present, nil); err != nil {
		return err
	}
	return validateLiveRetainedWorkloadPassOrigins(tx, evidence, origins, present)
}

// loadSQLiteRetainedWorkloadPassProofs 以一次 consumer 查询恢复全部 proof，并拒绝
// 缺失、多余或与 reused result 不一致的行。
func loadSQLiteRetainedWorkloadPassProofs(tx *sql.Tx, consumerJobID string, results []RemoteCIWorkloadResult) ([]WorkloadPassEvidence, error) {
	expected, err := retainedWorkloadPassResults(consumerJobID, results)
	if err != nil || len(expected) == 0 {
		return nil, err
	}
	rows, err := tx.Query(`SELECT workload_id, identity_digest, origin_job_id,
		origin_accepted_generation, origin_source_tree_sha, origin_receipt_set_sha256,
		origin_execution_json, evidence_sha256
		FROM ci_retained_workload_pass_proofs WHERE consumer_job_id = ? ORDER BY workload_id`, consumerJobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("load retained workload pass proofs", err)
	}
	defer rows.Close()
	loaded := make([]WorkloadPassEvidence, 0, len(expected))
	for rows.Next() {
		evidence, err := scanSQLiteRetainedWorkloadPassProof(rows, expected)
		if err != nil {
			return nil, err
		}
		loaded = append(loaded, evidence)
		delete(expected, evidence.Identity.WorkloadID)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate retained workload pass proofs", err)
	}
	if len(expected) != 0 {
		return nil, errors.New("retained workload pass proof set is incomplete")
	}
	return loaded, nil
}

// retainedWorkloadPassResults 建立 reused result 索引；非 reused 行不得拥有 proof。
func retainedWorkloadPassResults(consumerJobID string, results []RemoteCIWorkloadResult) (map[GateID]RemoteCIWorkloadResult, error) {
	if strings.TrimSpace(consumerJobID) == "" {
		return nil, errors.New("retained workload pass proof consumer job ID is required")
	}
	expected := make(map[GateID]RemoteCIWorkloadResult)
	for _, result := range results {
		if result.Disposition != WorkloadDispositionReused {
			continue
		}
		if _, duplicate := expected[result.Identity.WorkloadID]; duplicate {
			return nil, errors.New("retained workload pass proof result is duplicated")
		}
		expected[result.Identity.WorkloadID] = result
	}
	return expected, nil
}

// scanSQLiteRetainedWorkloadPassProof 解码单行 proof，并与同 workload reused result 严格绑定。
func scanSQLiteRetainedWorkloadPassProof(rows interface{ Scan(...any) error }, expected map[GateID]RemoteCIWorkloadResult) (WorkloadPassEvidence, error) {
	var workloadID, identityDigest, originGeneration, executionJSON string
	var evidence WorkloadPassEvidence
	if err := rows.Scan(&workloadID, &identityDigest, &evidence.OriginJobID, &originGeneration,
		&evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256); err != nil {
		return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("scan retained workload pass proof", err)
	}
	result, ok := expected[GateID(workloadID)]
	if !ok {
		return WorkloadPassEvidence{}, errors.New("retained workload pass proof set contains an unexpected row")
	}
	value, err := strconv.ParseUint(originGeneration, 10, 64)
	if err != nil || value == 0 {
		return WorkloadPassEvidence{}, errors.New("retained workload pass proof origin generation is invalid")
	}
	evidence.OriginAcceptedGeneration = value
	identity, execution, err := decodeRetainedWorkloadPassOriginJSON(executionJSON, result.Identity)
	if err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("decode retained workload pass proof origin: %w", err)
	}
	if identityDigest != identity.IdentityDigest {
		return WorkloadPassEvidence{}, errors.New("retained workload pass proof identity does not match origin payload")
	}
	evidence.Identity = identity
	evidence.OriginExecution = execution
	if err := bindSQLiteRetainedWorkloadPassProof(&evidence, result); err != nil {
		return WorkloadPassEvidence{}, err
	}
	return evidence, nil
}

// bindSQLiteRetainedWorkloadPassProof 校验 proof/result 绑定并解码规范执行载荷。
func bindSQLiteRetainedWorkloadPassProof(evidence *WorkloadPassEvidence, result RemoteCIWorkloadResult) error {
	if evidence.OriginJobID != result.OriginJobID || evidence.OriginAcceptedGeneration != result.OriginAcceptedGeneration {
		return errors.New("retained workload pass proof does not match reused result origin")
	}
	if err := validateWorkloadPassEvidence(*evidence); err != nil {
		return fmt.Errorf("validate retained workload pass proof: %w", err)
	}
	return validateReusableWorkloadEvidenceBinding(*evidence, result)
}

// validateLiveRetainedWorkloadPassOrigins 只重验仍存在的直接 origin；存在但不再
// 是直接 executed 投影时必须报错，不能伪装成已压缩来源。
func validateLiveRetainedWorkloadPassOrigins(tx *sql.Tx, evidence []WorkloadPassEvidence, origins map[string]workloadPassEvidenceOriginContext, present map[string]struct{}) error {
	for _, item := range evidence {
		origin, direct := origins[item.OriginJobID]
		if direct {
			if err := validateStoredWorkloadPassEvidenceWithOriginContext(tx, origin, item); err != nil {
				return fmt.Errorf("retained workload pass proof origin proof: %w", err)
			}
			continue
		}
		if _, exists := present[item.OriginJobID]; exists {
			return errors.New("retained workload pass proof origin proof is no longer a direct executed projection")
		}
	}
	return nil
}
