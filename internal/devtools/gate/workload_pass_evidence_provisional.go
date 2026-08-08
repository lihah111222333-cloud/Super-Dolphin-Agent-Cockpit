package gate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// promoteSQLiteRemoteCIProvisionalWorkloadPassEvidence 在失败运行仍完整清理后，
// 只提升逐 workload 已证明通过的执行；运行本身始终保持 authoritative=false。
// 该函数必须在 provisional run 投影的同一 SQLite 事务内调用，避免 evidence 脱离
// 对应的 job、shard、report 和 ECI 终态。
func promoteSQLiteRemoteCIProvisionalWorkloadPassEvidence(tx *sql.Tx, record RemoteCIRunRecord) error {
	eligible, err := provisionalWorkloadEvidenceRunEligible(tx, record)
	if err != nil {
		return err
	}
	if !eligible {
		return nil
	}
	stored, catalog, err := loadProvisionalWorkloadProjection(tx, record.JobID)
	if err != nil {
		return err
	}
	candidates, skipped, firstSkip := collectProvisionalWorkloadEvidenceCandidates(stored, catalog.Catalog)
	if skipped != 0 {
		if err := appendProvisionalWorkloadEvidenceDiagnostic(tx, &stored, skipped, firstSkip); err != nil {
			return err
		}
	}
	return replaceProvisionalWorkloadPassEvidence(tx, stored, candidates)
}

// provisionalWorkloadEvidenceRunEligible 检查 provisional evidence 只允许当前 accepted 代的 cleaned failure。
func provisionalWorkloadEvidenceRunEligible(tx *sql.Tx, record RemoteCIRunRecord) (bool, error) {
	if record.Authoritative || !record.CleanupComplete || !remoteCIProvisionalFailureStatus(record.Status) {
		return false, nil
	}
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return false, err
	}
	// 旧 accepted 代仍保留审计投影，但不能生成新的可复用 evidence。
	return record.AcceptedGeneration == currentGeneration && len(record.WorkloadResults) != 0, nil
}

// appendProvisionalWorkloadEvidenceDiagnostic 将省略候选的原因写回当前 SQLite run 并重载投影。
func appendProvisionalWorkloadEvidenceDiagnostic(tx *sql.Tx, stored *RemoteCIRunRecord, skipped int, firstSkip error) error {
	diagnostic := fmt.Sprintf("partial workload PASS evidence omitted=%d: %v", skipped, firstSkip)
	if err := appendRemoteCIProjectionDiagnostic(tx, stored.JobID, diagnostic); err != nil {
		return err
	}
	reloaded, err := loadRemoteCIRunRow(tx, stored.JobID)
	if err != nil {
		return err
	}
	if err := loadRemoteCIRunDetails(tx, stored.JobID, &reloaded); err != nil {
		return err
	}
	*stored = reloaded
	return nil
}

// replaceProvisionalWorkloadPassEvidence 用当前 projection digest 替换同一 job 的旧候选 evidence。
func replaceProvisionalWorkloadPassEvidence(tx *sql.Tx, stored RemoteCIRunRecord, candidates []provisionalWorkloadEvidenceCandidate) error {
	if _, err := tx.Exec(`DELETE FROM ci_workload_pass_evidence WHERE origin_job_id = ?`, stored.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear provisional workload pass evidence", err)
	}
	receiptDigest, err := provisionalWorkloadProjectionSHA256(tx, stored)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := insertWorkloadPassEvidence(tx, stored, receiptDigest, candidate.identity, candidate.execution); err != nil {
			return err
		}
	}
	return nil
}

// loadProvisionalWorkloadProjection 从同一 SQLite 事务回读完整 run、child rows 和 catalog。
func loadProvisionalWorkloadProjection(tx *sql.Tx, jobID string) (RemoteCIRunRecord, WorkloadCatalogRecord, error) {
	stored, err := loadRemoteCIRunRow(tx, jobID)
	if err != nil {
		return RemoteCIRunRecord{}, WorkloadCatalogRecord{}, err
	}
	if err := loadRemoteCIRunDetails(tx, jobID, &stored); err != nil {
		return RemoteCIRunRecord{}, WorkloadCatalogRecord{}, err
	}
	catalog, err := loadSQLiteWorkloadCatalog(tx, stored.CatalogDigest)
	if err != nil {
		return RemoteCIRunRecord{}, WorkloadCatalogRecord{}, fmt.Errorf("load provisional workload pass catalog: %w", err)
	}
	return stored, catalog, nil
}

// collectProvisionalWorkloadEvidenceCandidates 按 SQLite workload result 收集独立 PASS 候选。
func collectProvisionalWorkloadEvidenceCandidates(record RemoteCIRunRecord, catalog WorkloadCatalog) ([]provisionalWorkloadEvidenceCandidate, int, error) {
	executions := indexWorkloadExecutions(record.WorkloadExecutions)
	canonical := indexCanonicalWorkloadPassCatalog(catalog)
	candidates := make([]provisionalWorkloadEvidenceCandidate, 0, len(record.WorkloadResults))
	skipped := 0
	var firstSkip error
	for _, result := range record.WorkloadResults {
		candidate, err := provisionalWorkloadEvidenceCandidateForResult(record, result, executions, canonical)
		if err == nil {
			if candidate != nil {
				candidates = append(candidates, *candidate)
			}
			continue
		}
		skipped++
		if firstSkip == nil {
			firstSkip = err
		}
	}
	return candidates, skipped, firstSkip
}

// provisionalWorkloadEvidenceCandidateForResult 验证一个 executed result 的 canonical identity 和完整执行。
func provisionalWorkloadEvidenceCandidateForResult(record RemoteCIRunRecord, result RemoteCIWorkloadResult, executions map[GateID]PlanGateExecution, canonical map[GateID]Workload) (*provisionalWorkloadEvidenceCandidate, error) {
	if result.Disposition != WorkloadDispositionExecuted {
		return nil, nil
	}
	execution, ok := executions[result.Identity.WorkloadID]
	if !ok {
		return nil, fmt.Errorf("workload %q has no persisted execution", result.Identity.WorkloadID)
	}
	if err := validateCanonicalWorkloadPassIdentity(result.Identity, canonical); err != nil {
		return nil, fmt.Errorf("workload %q identity: %w", result.Identity.WorkloadID, err)
	}
	if err := validateProvisionalWorkloadPassCandidate(record, result, execution); err != nil {
		return nil, err
	}
	return &provisionalWorkloadEvidenceCandidate{identity: result.Identity, execution: execution}, nil
}

type provisionalWorkloadEvidenceCandidate struct {
	identity  WorkloadPassIdentity
	execution PlanGateExecution
}

// validateProvisionalWorkloadPassCandidate 是逐 workload 的权威谓词：通过状态、
// exit/timing/profile、分片归属、run generation 和 identity 必须同时成立。
func validateProvisionalWorkloadPassCandidate(record RemoteCIRunRecord, result RemoteCIWorkloadResult, execution PlanGateExecution) error {
	if result.OriginJobID != record.JobID || result.OriginAcceptedGeneration != record.AcceptedGeneration {
		return fmt.Errorf("workload %q origin run identity does not match provisional run", result.Identity.WorkloadID)
	}
	if execution.GateID != result.Identity.WorkloadID {
		return fmt.Errorf("workload %q execution does not match identity", result.Identity.WorkloadID)
	}
	evidence := WorkloadPassEvidence{Identity: result.Identity, OriginJobID: record.JobID, OriginAcceptedGeneration: record.AcceptedGeneration, OriginSourceTreeSHA: record.SourceTreeSHA, OriginReceiptSetSHA256: "sha256:" + strings.Repeat("0", 64), OriginExecution: execution, EvidenceSHA256: "sha256:" + strings.Repeat("0", 64)}
	if err := validateWorkloadPassEvidenceExecution(evidence); err != nil {
		return fmt.Errorf("workload %q is not a complete passing execution: %w", result.Identity.WorkloadID, err)
	}
	shardIdentity, err := remoteCIRunWorkloadShardIdentity(record.Shards, execution.GateID)
	if err != nil {
		return fmt.Errorf("workload %q shard binding: %w", result.Identity.WorkloadID, err)
	}
	if execution.ShardIdentity != shardIdentity {
		return fmt.Errorf("workload %q execution shard identity drifted", result.Identity.WorkloadID)
	}
	return nil
}

// provisionalWorkloadProjectionSHA256 对 SQLite 回读的完整 provisional run
// projection 计算规范摘要。它不是 JSON truth source：JSON 仅是稳定编码，真相仍是
// 当前事务中的 ci_runs/ci_shards/ci_gate_executions/ci_workload_executions/
// ci_run_workload_results/ci_timing_observations 行。
func provisionalWorkloadProjectionSHA256(tx *sql.Tx, record RemoteCIRunRecord) (string, error) {
	stored, err := loadRemoteCIRunRow(tx, record.JobID)
	if err != nil {
		return "", err
	}
	if err := loadRemoteCIRunDetails(tx, record.JobID, &stored); err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		SchemaVersion string            `json:"schema_version"`
		Run           RemoteCIRunRecord `json:"run"`
	}{SchemaVersion: "remote-ci-provisional-workload-evidence/v1", Run: stored})
	if err != nil {
		return "", fmt.Errorf("encode provisional workload evidence projection: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// appendRemoteCIProjectionDiagnostic 保持失败 run 的字段诊断有界，且不改变
// run 的 authoritative 状态。
func appendRemoteCIProjectionDiagnostic(tx *sql.Tx, jobID, diagnostic string) error {
	const maxErrorTextBytes = 4 << 10
	var existing string
	if err := tx.QueryRow(`SELECT error_text FROM ci_runs WHERE job_id = ?`, jobID).Scan(&existing); err != nil {
		return mapDurationLedgerSQLiteError("load remote CI projection diagnostic", err)
	}
	merged := strings.TrimSpace(existing)
	if merged != "" {
		merged += "; "
	}
	merged += strings.TrimSpace(diagnostic)
	if len(merged) > maxErrorTextBytes {
		digest := sha256.Sum256([]byte(merged))
		marker := fmt.Sprintf("\n...[remote CI diagnostic truncated bytes=%d sha256:%x]", len(merged), digest)
		budget := max(0, maxErrorTextBytes-len(marker))
		merged = merged[:budget] + marker
	}
	if _, err := tx.Exec(`UPDATE ci_runs SET error_text = ? WHERE job_id = ?`, merged, jobID); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI projection diagnostic", err)
	}
	return nil
}
