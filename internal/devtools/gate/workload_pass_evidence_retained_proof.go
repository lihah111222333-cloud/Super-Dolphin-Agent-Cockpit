package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// verifySQLiteRetainedWorkloadPassProof 验证单个持久化 consumer 的不可变 proof，绝不回退到 v15 origin 行。
func verifySQLiteRetainedWorkloadPassProof(tx *sql.Tx, consumerJobID string, result RemoteCIWorkloadResult) error {
	evidence, consumerGeneration, executionJSON, err := loadSQLiteRetainedWorkloadPassProof(tx, consumerJobID, result)
	if err != nil {
		return err
	}
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return fmt.Errorf("load retained workload pass proof accepted generation: %w", err)
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(currentGeneration, consumerGeneration); err != nil {
		return fmt.Errorf("retained workload pass proof consumer generation: %w", err)
	}
	if evidence.OriginJobID != result.OriginJobID || evidence.OriginAcceptedGeneration != result.OriginAcceptedGeneration || evidence.EvidenceSHA256 != result.EvidenceSHA256 {
		return errors.New("retained workload pass proof does not match reused result origin")
	}
	if err := decodeStoredWorkloadPassExecutionJSON(executionJSON, &evidence.OriginExecution); err != nil {
		return fmt.Errorf("decode retained workload pass proof execution: %w", err)
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("validate retained workload pass proof: %w", err)
	}
	return nil
}

// loadSQLiteRetainedWorkloadPassProof 解码 proof 行和两代整数编码，保留缺失与篡改的严格错误语义。
func loadSQLiteRetainedWorkloadPassProof(tx *sql.Tx, consumerJobID string, result RemoteCIWorkloadResult) (WorkloadPassEvidence, uint64, string, error) {
	if strings.TrimSpace(consumerJobID) == "" {
		return WorkloadPassEvidence{}, 0, "", errors.New("retained workload pass proof consumer job ID is required")
	}
	var consumerGenerationText, executionJSON, originGenerationText string
	evidence := WorkloadPassEvidence{Identity: result.Identity}
	err := tx.QueryRow(`SELECT consumer.accepted_generation, proof.origin_job_id,
		proof.origin_accepted_generation, proof.origin_source_tree_sha,
		proof.origin_receipt_set_sha256, proof.origin_execution_json, proof.evidence_sha256
		FROM ci_retained_workload_pass_proofs AS proof
		JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id
		WHERE proof.consumer_job_id = ? AND proof.workload_id = ? AND proof.identity_digest = ?`,
		consumerJobID, string(result.Identity.WorkloadID), result.Identity.IdentityDigest,
	).Scan(&consumerGenerationText, &evidence.OriginJobID, &originGenerationText,
		&evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkloadPassEvidence{}, 0, "", fmt.Errorf("retained workload pass proof for consumer %q and workload %q is missing", consumerJobID, result.Identity.WorkloadID)
	}
	if err != nil {
		return WorkloadPassEvidence{}, 0, "", mapDurationLedgerSQLiteError("load retained workload pass proof", err)
	}
	consumerGeneration, err := strconv.ParseUint(consumerGenerationText, 10, 64)
	if err != nil || consumerGeneration == 0 {
		return WorkloadPassEvidence{}, 0, "", errors.New("retained workload pass proof consumer generation is invalid")
	}
	evidence.OriginAcceptedGeneration, err = strconv.ParseUint(originGenerationText, 10, 64)
	if err != nil || evidence.OriginAcceptedGeneration == 0 {
		return WorkloadPassEvidence{}, 0, "", errors.New("retained workload pass proof origin generation is invalid")
	}
	return evidence, consumerGeneration, executionJSON, nil
}
