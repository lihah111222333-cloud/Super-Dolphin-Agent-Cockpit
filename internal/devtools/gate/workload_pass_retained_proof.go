package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// validateRetainedWorkloadPassProof 验证 consumer-owned v16 proof；它绝不回退到
// 已可能被 compaction 删除的 v15 origin evidence 行。
func validateRetainedWorkloadPassProof(tx *sql.Tx, evidence WorkloadPassEvidence, currentGeneration uint64) error {
	if tx == nil {
		return errors.New("retained workload pass proof transaction is nil")
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("retained workload pass proof evidence: %w", err)
	}
	row, err := loadRetainedWorkloadPassProofRow(tx, evidence, currentGeneration)
	if err != nil {
		return err
	}
	if err := row.validate(evidence, currentGeneration); err != nil {
		return err
	}
	return validateRetainedWorkloadPassProofConsumer(tx, row.consumerID, row.consumerGeneration)
}

type retainedWorkloadPassProofRow struct {
	consumerID, consumerGeneration, workloadID, identityDigest, executionDigest, inputDigest, environmentDigest string
	disposition, originID, originGeneration, resultDigest, proofIdentityDigest, proofOriginID                   string
	proofOriginGeneration, sourceTreeSHA, receiptSHA, executionJSON, proofDigest                                string
}

// loadRetainedWorkloadPassProofRow 按来源证据唯一读取 retained proof；
// 缺失或歧义都必须阻断读取。
func loadRetainedWorkloadPassProofRow(tx *sql.Tx, evidence WorkloadPassEvidence, currentGeneration uint64) (retainedWorkloadPassProofRow, error) {
	retained := retainedWorkloadPassGenerations(currentGeneration)
	rows, err := tx.Query(`SELECT consumer.job_id, consumer.accepted_generation, results.workload_id, results.identity_digest, results.execution_digest, results.input_digest, results.environment_digest, results.disposition, results.origin_job_id, results.origin_accepted_generation, results.evidence_sha256,
		proof.identity_digest, proof.origin_job_id, proof.origin_accepted_generation, proof.origin_source_tree_sha, proof.origin_receipt_set_sha256, proof.origin_execution_json, proof.evidence_sha256
		FROM ci_retained_workload_pass_proofs AS proof
		JOIN ci_run_workload_results AS results ON results.job_id = proof.consumer_job_id AND results.workload_id = proof.workload_id
		JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id
		WHERE proof.identity_digest = ? AND proof.origin_job_id = ? AND proof.origin_accepted_generation = ? AND proof.evidence_sha256 = ?
			AND consumer.accepted_generation IN (?, ?, ?)
		LIMIT 2`, evidence.Identity.IdentityDigest, evidence.OriginJobID, strconv.FormatUint(evidence.OriginAcceptedGeneration, 10), evidence.EvidenceSHA256,
		retained[0], retained[1], retained[2])
	if err != nil {
		return retainedWorkloadPassProofRow{}, mapDurationLedgerSQLiteError("query retained workload pass proof", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return retainedWorkloadPassProofRow{}, errors.New("retained workload pass proof is missing")
	}
	var row retainedWorkloadPassProofRow
	if err := rows.Scan(&row.consumerID, &row.consumerGeneration, &row.workloadID, &row.identityDigest, &row.executionDigest, &row.inputDigest, &row.environmentDigest, &row.disposition, &row.originID, &row.originGeneration, &row.resultDigest, &row.proofIdentityDigest, &row.proofOriginID, &row.proofOriginGeneration, &row.sourceTreeSHA, &row.receiptSHA, &row.executionJSON, &row.proofDigest); err != nil {
		return retainedWorkloadPassProofRow{}, mapDurationLedgerSQLiteError("scan retained workload pass proof", err)
	}
	if rows.Next() {
		return retainedWorkloadPassProofRow{}, errors.New("retained workload pass proof is ambiguous")
	}
	if err := rows.Err(); err != nil {
		return retainedWorkloadPassProofRow{}, mapDurationLedgerSQLiteError("iterate retained workload pass proof", err)
	}
	return row, nil
}

// validate 同时验证来源 proof、当前 consumer replay 结果和代际绑定。
func (row retainedWorkloadPassProofRow) validate(evidence WorkloadPassEvidence, currentGeneration uint64) error {
	if err := row.validateConsumerGeneration(currentGeneration); err != nil {
		return err
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("retained workload pass proof canonical evidence: %w", err)
	}
	if !row.matches(evidence) {
		return errors.New("retained workload pass proof consumer, origin, identity, result, or canonical digest drifted")
	}
	storedIdentity, storedExecution, err := decodeRetainedWorkloadPassOriginJSON(row.executionJSON, row.resultIdentity())
	if err != nil {
		return fmt.Errorf("retained workload pass proof origin JSON: %w", err)
	}
	if !reflect.DeepEqual(storedIdentity, evidence.Identity) || !reflect.DeepEqual(storedExecution, evidence.OriginExecution) {
		return errors.New("retained workload pass proof origin JSON drifted")
	}
	return validateReusableWorkloadEvidenceBinding(evidence, row.result())
}

func (row retainedWorkloadPassProofRow) validateConsumerGeneration(current uint64) error {
	value, err := strconv.ParseUint(row.consumerGeneration, 10, 64)
	if err != nil || row.consumerGeneration != strconv.FormatUint(value, 10) {
		return errors.New("retained workload pass proof consumer generation is invalid")
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(current, value); err != nil {
		return fmt.Errorf("retained workload pass proof consumer generation: %w", err)
	}
	return nil
}

func (row retainedWorkloadPassProofRow) matches(e WorkloadPassEvidence) bool {
	return row.matchesIdentity(e) && row.matchesOrigin(e) && row.matchesProof(e)
}

func (row retainedWorkloadPassProofRow) matchesIdentity(e WorkloadPassEvidence) bool {
	return row.disposition == string(WorkloadDispositionReused) && row.workloadID == string(e.Identity.WorkloadID) && row.proofIdentityDigest == e.Identity.IdentityDigest
}

func (row retainedWorkloadPassProofRow) matchesOrigin(e WorkloadPassEvidence) bool {
	return row.originID == e.OriginJobID && row.originGeneration == strconv.FormatUint(e.OriginAcceptedGeneration, 10)
}

// matchesProof 比较 proof 的来源 run、执行载荷和直接证据摘要。
func (row retainedWorkloadPassProofRow) matchesProof(e WorkloadPassEvidence) bool {
	return row.proofOriginID == e.OriginJobID && row.proofOriginGeneration == row.originGeneration && row.sourceTreeSHA == e.OriginSourceTreeSHA && row.receiptSHA == e.OriginReceiptSetSHA256 && row.executionJSON != "" && row.proofDigest == e.EvidenceSHA256
}

func (row retainedWorkloadPassProofRow) resultIdentity() WorkloadPassIdentity {
	return WorkloadPassIdentity{WorkloadID: GateID(row.workloadID), IdentityDigest: row.identityDigest, ExecutionDigest: row.executionDigest, InputDigest: row.inputDigest, EnvironmentDigest: row.environmentDigest}
}

func (row retainedWorkloadPassProofRow) result() RemoteCIWorkloadResult {
	generation, _ := strconv.ParseUint(row.originGeneration, 10, 64)
	return RemoteCIWorkloadResult{Identity: row.resultIdentity(), Disposition: row.disposition, OriginJobID: row.originID, OriginAcceptedGeneration: generation, EvidenceSHA256: row.resultDigest}
}

// validateRetainedWorkloadPassProofConsumer 重新加载完整 consumer 聚合并要求权威清理 PASS。
func validateRetainedWorkloadPassProofConsumer(tx *sql.Tx, jobID, generation string) error {
	consumer, err := loadRemoteCIRunRow(tx, jobID)
	if err != nil {
		return fmt.Errorf("load retained workload pass proof consumer: %w", err)
	}
	if err := loadRemoteCIRunDetails(tx, jobID, &consumer); err != nil {
		return fmt.Errorf("load retained workload pass proof consumer details: %w", err)
	}
	if err := validateRemoteCIRunRecord(consumer); err != nil {
		return fmt.Errorf("validate retained workload pass proof consumer: %w", err)
	}
	if !consumer.Authoritative || consumer.Status != ResultStatusPassed || !consumer.CleanupComplete || generation != strconv.FormatUint(consumer.AcceptedGeneration, 10) {
		return errors.New("retained workload pass proof consumer is not an authoritative cleaned PASS")
	}
	return nil
}
