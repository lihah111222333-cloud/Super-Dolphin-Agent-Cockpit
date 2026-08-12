package gate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// workloadPassEnvironmentReplayBatchSize 限制一次 authority hint 查询中的 OR 项数量。
const workloadPassEnvironmentReplayBatchSize = 200

// WorkloadPassEnvironmentReplayHint 是尚未验证来源 authority 的环境复用提示。
// 调用方只能读取它做语义筛选；只有 ValidateWorkloadPassEnvironmentReplayHint
// 返回的 WorkloadPassEvidence 才能进入 reused/proof。
type WorkloadPassEnvironmentReplayHint struct {
	untrustedCandidate WorkloadPassEvidence
}

// UntrustedCandidate 返回仅供语义筛选的未授权候选副本。
func (hint WorkloadPassEnvironmentReplayHint) UntrustedCandidate() WorkloadPassEvidence {
	return hint.untrustedCandidate
}

// LookupWorkloadPassEnvironmentReplayHints 返回当前代中 workload 与执行身份相同的未授权提示。
// 它只校验行自身和请求绑定，绝不加载 origin run、receipt 或 provisional projection。
func (store *DurationLedgerStore) LookupWorkloadPassEnvironmentReplayHints(identities []WorkloadPassIdentity) (map[GateID][]WorkloadPassEnvironmentReplayHint, error) {
	return store.lookupWorkloadPassEnvironmentReplayHintsWithStats(identities, nil)
}

// lookupWorkloadPassEnvironmentReplayHintsWithStats 在单只读事务中批量加载未授权提示，
// 并为规模测试保留“未读取 origin authority”的计数边界。
func (store *DurationLedgerStore) lookupWorkloadPassEnvironmentReplayHintsWithStats(
	identities []WorkloadPassIdentity,
	stats *workloadPassEvidenceLookupStats,
) (map[GateID][]WorkloadPassEnvironmentReplayHint, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if err := validateWorkloadPassIdentities(identities); err != nil {
		return nil, err
	}
	result := make(map[GateID][]WorkloadPassEnvironmentReplayHint)
	if len(identities) == 0 {
		return result, nil
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin workload PASS environment replay hint lookup", err)
	}
	defer tx.Rollback()
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return nil, fmt.Errorf("load workload PASS environment replay generation: %w", err)
	}
	for start := 0; start < len(identities); start += workloadPassEnvironmentReplayBatchSize {
		end := min(start+workloadPassEnvironmentReplayBatchSize, len(identities))
		if err := appendWorkloadPassEnvironmentReplayHintBatch(tx, identities[start:end], currentGeneration, result); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit workload PASS environment replay hint lookup", err)
	}
	_ = stats // 计数器仅证明此阶段没有 origin authority 加载。
	return result, nil
}

// appendWorkloadPassEnvironmentReplayHintBatch 查询一批当前代候选并追加通过行级校验的提示。
func appendWorkloadPassEnvironmentReplayHintBatch(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	currentGeneration uint64,
	result map[GateID][]WorkloadPassEnvironmentReplayHint,
) error {
	requested, err := workloadPassEnvironmentReplayRequested(identities)
	if err != nil {
		return err
	}
	query, args := workloadPassEnvironmentReplayQuery(identities, currentGeneration)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return mapDurationLedgerSQLiteError("query workload PASS environment replay hints", err)
	}
	defer rows.Close()
	for rows.Next() {
		evidence, err := scanWorkloadPassEnvironmentReplayHint(rows)
		if err != nil {
			if isWorkloadPassReplayLegacyMaterial(err) {
				continue
			}
			return err
		}
		if err := appendWorkloadPassEnvironmentReplayHint(evidence, requested, result); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate workload PASS environment replay hints", err)
	}
	return nil
}

func workloadPassEnvironmentReplayRequested(identities []WorkloadPassIdentity) (map[GateID]WorkloadPassIdentity, error) {
	requested := make(map[GateID]WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		if _, duplicate := requested[identity.WorkloadID]; duplicate {
			return nil, fmt.Errorf("workload PASS environment replay workload %q is duplicated", identity.WorkloadID)
		}
		requested[identity.WorkloadID] = identity
	}
	return requested, nil
}

// scanWorkloadPassEnvironmentReplayHint 严格解码当前代行并验证 identity/evidence 自摘要。
func scanWorkloadPassEnvironmentReplayHint(rows workloadPassEvidenceScanner) (WorkloadPassEvidence, error) {
	var evidence WorkloadPassEvidence
	var generation, workloadID, executionJSON string
	if err := rows.Scan(
		&evidence.Identity.IdentityDigest, &generation, &workloadID,
		&evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest,
		&evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256,
		&executionJSON, &evidence.EvidenceSHA256,
	); err != nil {
		return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("scan workload PASS environment replay hint", err)
	}
	evidence.Identity.WorkloadID = GateID(workloadID)
	parsedGeneration, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || parsedGeneration == 0 || generation != strconv.FormatUint(parsedGeneration, 10) {
		return WorkloadPassEvidence{}, errors.New("workload PASS environment replay hint origin generation is invalid")
	}
	evidence.OriginAcceptedGeneration = parsedGeneration
	if err := validateWorkloadPassEnvironmentReplayExecutionProfileJSON(executionJSON); err != nil {
		return WorkloadPassEvidence{}, err
	}
	if err := decodeStoredWorkloadPassExecutionJSON(executionJSON, &evidence.OriginExecution); err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("decode workload PASS environment replay hint execution: %w", err)
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		if isWorkloadPassReplayLegacyMaterial(err) {
			return WorkloadPassEvidence{}, err
		}
		return WorkloadPassEvidence{}, fmt.Errorf("validate workload PASS environment replay hint: %w", err)
	}
	return evidence, nil
}

// validateWorkloadPassEnvironmentReplayExecutionProfileJSON 在不加载 origin run 的前提下，
// 把缺少 go_flags 的旧 execution profile 识别为自然 MISS。
func validateWorkloadPassEnvironmentReplayExecutionProfileJSON(encoded string) error {
	var executionFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &executionFields); err != nil {
		return fmt.Errorf("decode workload PASS environment replay hint execution fields: %w", err)
	}
	profileJSON, present := executionFields["execution_profile"]
	if !present || bytes.Equal(bytes.TrimSpace(profileJSON), []byte("null")) {
		return errors.New("workload PASS environment replay hint execution profile is required")
	}
	var profileFields map[string]json.RawMessage
	if err := json.Unmarshal(profileJSON, &profileFields); err != nil || profileFields == nil {
		return errors.New("workload PASS environment replay hint execution profile is invalid")
	}
	return validateStoredRemoteCIGoFlags(profileFields)
}

func appendWorkloadPassEnvironmentReplayHint(
	evidence WorkloadPassEvidence,
	requested map[GateID]WorkloadPassIdentity,
	result map[GateID][]WorkloadPassEnvironmentReplayHint,
) error {
	target, ok := requested[evidence.Identity.WorkloadID]
	if !ok || target.ExecutionDigest != evidence.Identity.ExecutionDigest {
		return errors.New("workload PASS environment replay query returned an unrequested identity")
	}
	if target.EnvironmentDigest == evidence.Identity.EnvironmentDigest {
		return nil
	}
	result[evidence.Identity.WorkloadID] = append(result[evidence.Identity.WorkloadID], WorkloadPassEnvironmentReplayHint{untrustedCandidate: evidence})
	return nil
}

// ValidateWorkloadPassEnvironmentReplayHint 在新的只读快照中精确重读并完整验证提示的 SQLite authority。
func (store *DurationLedgerStore) ValidateWorkloadPassEnvironmentReplayHint(hint WorkloadPassEnvironmentReplayHint) (WorkloadPassEvidence, error) {
	validated, err := store.validateWorkloadPassEnvironmentReplayHintsWithStats([]WorkloadPassEnvironmentReplayHint{hint}, nil)
	if err != nil {
		return WorkloadPassEvidence{}, err
	}
	return validated[0], nil
}

func (store *DurationLedgerStore) validateWorkloadPassEnvironmentReplayHintWithStats(
	hint WorkloadPassEnvironmentReplayHint,
	stats *workloadPassEvidenceLookupStats,
) (WorkloadPassEvidence, error) {
	validated, err := store.validateWorkloadPassEnvironmentReplayHintsWithStats([]WorkloadPassEnvironmentReplayHint{hint}, stats)
	if err != nil {
		return WorkloadPassEvidence{}, err
	}
	return validated[0], nil
}

// ValidateWorkloadPassEnvironmentReplayHints 在一个只读事务中批量验证全部已选 hint。
// 任一项 authority 不一致时整批失败，不返回部分证据。
func (store *DurationLedgerStore) ValidateWorkloadPassEnvironmentReplayHints(hints []WorkloadPassEnvironmentReplayHint) ([]WorkloadPassEvidence, error) {
	return store.validateWorkloadPassEnvironmentReplayHintsWithStats(hints, nil)
}

// validateWorkloadPassEnvironmentReplayHintsWithStats 在同一只读快照中批量重读并完整验证提示，
// 任一 authority 漂移都会拒绝整批结果。
func (store *DurationLedgerStore) validateWorkloadPassEnvironmentReplayHintsWithStats(
	hints []WorkloadPassEnvironmentReplayHint,
	stats *workloadPassEvidenceLookupStats,
) ([]WorkloadPassEvidence, error) {
	if len(hints) == 0 {
		return []WorkloadPassEvidence{}, nil
	}
	identities := make([]WorkloadPassIdentity, len(hints))
	for index, hint := range hints {
		candidate := hint.untrustedCandidate
		if err := validateWorkloadPassEvidence(candidate); err != nil {
			return nil, fmt.Errorf("validate workload PASS environment replay hint before authority lookup: %w", err)
		}
		identities[index] = candidate.Identity
	}
	validated, err := store.lookupWorkloadPassEvidenceWithStats(identities, nil, stats)
	if err != nil {
		return nil, err
	}
	if len(validated) != len(hints) {
		return nil, errors.New("workload PASS environment replay hint has no current SQLite authority")
	}
	for index, evidence := range validated {
		equal, err := canonicalWorkloadPassEvidenceEqual(hints[index].untrustedCandidate, evidence)
		if err != nil {
			return nil, err
		}
		if !equal {
			return nil, errors.New("workload PASS environment replay hint no longer matches SQLite authority")
		}
	}
	return validated, nil
}

func canonicalWorkloadPassEvidenceEqual(left, right WorkloadPassEvidence) (bool, error) {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false, fmt.Errorf("encode workload PASS environment replay hint: %w", err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false, fmt.Errorf("encode workload PASS environment replay authority: %w", err)
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func isWorkloadPassReplayLegacyMaterial(err error) bool {
	return errors.Is(err, errLegacyRemoteCIExecutionProfile) || errors.Is(err, errLegacyWorkloadPassIdentityDomain)
}

// workloadPassEnvironmentReplayQuery 只扫描当前代，且故意不把 environment_digest 作为查询键。
func workloadPassEnvironmentReplayQuery(identities []WorkloadPassIdentity, currentGeneration uint64) (string, []any) {
	rows := make([]string, 0, len(identities))
	args := make([]any, 0, len(identities)*2+2)
	for _, identity := range identities {
		rows = append(rows, "(?, ?)")
		args = append(args, string(identity.WorkloadID), identity.ExecutionDigest)
	}
	args = append(args, strconv.FormatUint(currentGeneration, 10))
	args = append(args, strconv.FormatUint(currentGeneration, 10))
	query := `WITH requested(workload_id, execution_digest) AS (VALUES ` + strings.Join(rows, ", ") + `)
		SELECT evidence.identity_digest, evidence.accepted_generation, evidence.workload_id, evidence.execution_digest, evidence.input_digest, evidence.environment_digest, evidence.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, evidence.evidence_sha256
		FROM requested JOIN ci_workload_pass_evidence AS evidence INDEXED BY idx_ci_workload_pass_evidence_source_replay ON evidence.workload_id = requested.workload_id AND evidence.execution_digest = requested.execution_digest JOIN ci_run_workload_results AS direct ON direct.job_id = evidence.origin_job_id AND direct.workload_id = evidence.workload_id AND direct.identity_digest = evidence.identity_digest JOIN ci_runs AS origin ON origin.job_id = evidence.origin_job_id
		WHERE evidence.accepted_generation = ? AND direct.disposition = 'executed' AND origin.accepted_generation = evidence.accepted_generation
		UNION ALL
		SELECT proof.identity_digest, proof.origin_accepted_generation, proof.workload_id,
			COALESCE(json_extract(proof.origin_execution_json, '$.source_identity.execution_digest'), result.execution_digest),
			COALESCE(json_extract(proof.origin_execution_json, '$.source_identity.input_digest'), result.input_digest),
			COALESCE(json_extract(proof.origin_execution_json, '$.source_identity.environment_digest'), result.environment_digest),
			proof.origin_job_id, proof.origin_source_tree_sha, proof.origin_receipt_set_sha256,
			CASE WHEN json_type(proof.origin_execution_json, '$.schema_version') IS NOT NULL THEN json_extract(proof.origin_execution_json, '$.execution') ELSE proof.origin_execution_json END,
			proof.evidence_sha256
		FROM requested CROSS JOIN ci_retained_workload_pass_proofs AS proof INDEXED BY idx_ci_retained_workload_pass_proofs_source_replay ON proof.workload_id = requested.workload_id
		CROSS JOIN ci_run_workload_results AS result ON result.job_id = proof.consumer_job_id AND result.workload_id = proof.workload_id
		CROSS JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id
		WHERE consumer.accepted_generation = ? AND result.disposition = 'reused'
			AND (consumer.authoritative = 1 OR EXISTS (SELECT 1 FROM ci_check_receipts AS receipt WHERE receipt.job_id = proof.consumer_job_id))
			AND result.origin_job_id = proof.origin_job_id AND result.origin_accepted_generation = proof.origin_accepted_generation
			AND COALESCE(json_extract(proof.origin_execution_json, '$.source_identity.execution_digest'), result.execution_digest) = requested.execution_digest
		ORDER BY 3, 1`
	return query, args
}
