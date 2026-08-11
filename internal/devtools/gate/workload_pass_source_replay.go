package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const workloadPassSourceReplayBatchSize = 200

type workloadPassSourceReplayPayload struct {
	Domain                   string               `json:"domain"`
	TargetIdentity           WorkloadPassIdentity `json:"target_identity"`
	SourceIdentity           WorkloadPassIdentity `json:"source_identity"`
	SourceOriginJobID        string               `json:"source_origin_job_id"`
	SourceAcceptedGeneration uint64               `json:"source_accepted_generation"`
	SourceTreeSHA            string               `json:"source_tree_sha"`
	SourceReceiptSetSHA256   string               `json:"source_receipt_set_sha256"`
	SourceEvidenceSHA256     string               `json:"source_evidence_sha256"`
}

// WorkloadPassSourceReplaySHA256 把当前精确 identity 绑定到一条已验证的直接来源 PASS。
func WorkloadPassSourceReplaySHA256(target WorkloadPassIdentity, source WorkloadPassEvidence) (string, error) {
	if err := validateWorkloadPassSourceReplayPair(target, source); err != nil {
		return "", err
	}
	payload, err := json.Marshal(workloadPassSourceReplayPayload{
		Domain:                   cicontract.WorkloadPassSourceReplayDomain,
		TargetIdentity:           target,
		SourceIdentity:           source.Identity,
		SourceOriginJobID:        source.OriginJobID,
		SourceAcceptedGeneration: source.OriginAcceptedGeneration,
		SourceTreeSHA:            source.OriginSourceTreeSHA,
		SourceReceiptSetSHA256:   source.OriginReceiptSetSHA256,
		SourceEvidenceSHA256:     source.EvidenceSHA256,
	})
	if err != nil {
		return "", fmt.Errorf("encode workload PASS source replay: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// validateWorkloadPassSourceReplayPair 只允许输入摘要变化，执行、环境与 workload 必须逐项相同。
func validateWorkloadPassSourceReplayPair(target WorkloadPassIdentity, source WorkloadPassEvidence) error {
	if err := validateWorkloadPassIdentity(target); err != nil {
		return fmt.Errorf("validate workload PASS source replay target: %w", err)
	}
	if err := validateWorkloadPassEvidence(source); err != nil {
		return fmt.Errorf("validate workload PASS source replay origin: %w", err)
	}
	if target.WorkloadID != source.Identity.WorkloadID ||
		target.ExecutionDigest != source.Identity.ExecutionDigest ||
		target.EnvironmentDigest != source.Identity.EnvironmentDigest {
		return errors.New("workload PASS source replay changes workload execution or environment identity")
	}
	if target.IdentityDigest == source.Identity.IdentityDigest || target.InputDigest == source.Identity.InputDigest {
		return errors.New("workload PASS source replay requires a distinct input identity")
	}
	return nil
}

// validateReusableWorkloadEvidenceBinding 接受直接同一 identity，或严格绑定的来源树重算证明。
func validateReusableWorkloadEvidenceBinding(source WorkloadPassEvidence, result RemoteCIWorkloadResult) error {
	if workloadPassIdentityMatches(source.Identity, result.Identity) {
		if source.EvidenceSHA256 != result.EvidenceSHA256 {
			return fmt.Errorf("reused workload result %q evidence does not match promoted evidence", result.Identity.WorkloadID)
		}
		return nil
	}
	expected, err := WorkloadPassSourceReplaySHA256(result.Identity, source)
	if err == nil {
		if result.EvidenceSHA256 != expected {
			return fmt.Errorf("reused workload result %q source replay digest does not match proof", result.Identity.WorkloadID)
		}
		return nil
	}
	environmentReplay, replayErr := WorkloadPassEnvironmentReplaySHA256(result.Identity, source)
	if replayErr != nil {
		return fmt.Errorf("reused workload result %q environment replay: %w", result.Identity.WorkloadID, replayErr)
	}
	if result.EvidenceSHA256 != environmentReplay {
		return fmt.Errorf("reused workload result %q environment replay digest does not match proof", result.Identity.WorkloadID)
	}
	return nil
}

// LookupWorkloadPassSourceReplayCandidates 返回保留窗口内执行和环境相同的直接 PASS 候选。
// 它不宣称输入相同；调用方必须从每条候选的 OriginSourceTreeSHA 重算当前 workload 指纹。
func (store *DurationLedgerStore) LookupWorkloadPassSourceReplayCandidates(identities []WorkloadPassIdentity) (map[GateID][]WorkloadPassEvidence, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if err := validateWorkloadPassIdentities(identities); err != nil {
		return nil, err
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin workload PASS source replay lookup", err)
	}
	defer tx.Rollback()
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return nil, fmt.Errorf("load workload PASS source replay generation: %w", err)
	}
	candidates, err := loadWorkloadPassSourceReplayCandidates(tx, identities, currentGeneration)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit workload PASS source replay lookup", err)
	}
	return candidates, nil
}

func loadWorkloadPassSourceReplayCandidates(tx *sql.Tx, identities []WorkloadPassIdentity, currentGeneration uint64) (map[GateID][]WorkloadPassEvidence, error) {
	requested := make(map[GateID]WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		if _, duplicate := requested[identity.WorkloadID]; duplicate {
			return nil, fmt.Errorf("workload PASS source replay workload %q is duplicated", identity.WorkloadID)
		}
		requested[identity.WorkloadID] = identity
	}
	result := make(map[GateID][]WorkloadPassEvidence)
	retained := retainedWorkloadPassGenerations(currentGeneration)
	for start := 0; start < len(identities); start += workloadPassSourceReplayBatchSize {
		end := min(start+workloadPassSourceReplayBatchSize, len(identities))
		if err := appendWorkloadPassSourceReplayBatch(tx, identities[start:end], requested, retained, currentGeneration, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// appendWorkloadPassSourceReplayBatch 批量读取候选并逐条交给严格来源验证，禁止未验行进入结果集。
func appendWorkloadPassSourceReplayBatch(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	requested map[GateID]WorkloadPassIdentity,
	retained [3]string,
	currentGeneration uint64,
	result map[GateID][]WorkloadPassEvidence,
) error {
	if len(identities) == 0 {
		return nil
	}
	query, args := workloadPassSourceReplayQuery(identities, retained)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return mapDurationLedgerSQLiteError("query workload PASS source replay candidates", err)
	}
	defer rows.Close()
	for rows.Next() {
		evidence, err := scanWorkloadPassSourceReplayCandidate(rows)
		if err != nil {
			return err
		}
		if err := appendValidatedWorkloadPassSourceReplayCandidate(tx, evidence, requested, currentGeneration, result); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate workload PASS source replay candidates", err)
	}
	return nil
}

// appendValidatedWorkloadPassSourceReplayCandidate 验证候选确属请求且来源 proof 完整；退休材料只降为 MISS。
func appendValidatedWorkloadPassSourceReplayCandidate(
	tx *sql.Tx,
	evidence WorkloadPassEvidence,
	requested map[GateID]WorkloadPassIdentity,
	currentGeneration uint64,
	result map[GateID][]WorkloadPassEvidence,
) error {
	target, ok := requested[evidence.Identity.WorkloadID]
	if !ok || target.ExecutionDigest != evidence.Identity.ExecutionDigest || target.EnvironmentDigest != evidence.Identity.EnvironmentDigest {
		return errors.New("workload PASS source replay query returned an unrequested identity")
	}
	if target.IdentityDigest == evidence.Identity.IdentityDigest {
		return nil
	}
	if err := validateWorkloadPassReadProof(tx, evidence, currentGeneration, nil); err != nil {
		return err
	}
	result[evidence.Identity.WorkloadID] = append(result[evidence.Identity.WorkloadID], evidence)
	return nil
}

func workloadPassSourceReplayQuery(identities []WorkloadPassIdentity, retained [3]string) (string, []any) {
	terms := make([]string, 0, len(identities))
	args := make([]any, 0, 2*(3+len(identities)*3))
	for _, generation := range retained {
		args = append(args, generation)
	}
	for _, identity := range identities {
		terms = append(terms, "(workload_id = ? AND execution_digest = ? AND environment_digest = ?)")
		args = append(args, string(identity.WorkloadID), identity.ExecutionDigest, identity.EnvironmentDigest)
	}
	for _, generation := range retained {
		args = append(args, generation)
	}
	for _, identity := range identities {
		args = append(args, string(identity.WorkloadID), identity.ExecutionDigest, identity.EnvironmentDigest)
	}
	directTerms := strings.NewReplacer("workload_id", "evidence.workload_id", "execution_digest", "evidence.execution_digest", "environment_digest", "evidence.environment_digest").Replace(strings.Join(terms, " OR "))
	proofTerms := strings.NewReplacer("workload_id", "result.workload_id", "execution_digest", "result.execution_digest", "environment_digest", "result.environment_digest").Replace(strings.Join(terms, " OR "))
	query := `SELECT evidence.identity_digest, evidence.accepted_generation, evidence.workload_id, evidence.execution_digest, evidence.input_digest, evidence.environment_digest, evidence.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, evidence.evidence_sha256
		FROM ci_workload_pass_evidence AS evidence JOIN ci_run_workload_results AS direct ON direct.job_id = evidence.origin_job_id AND direct.workload_id = evidence.workload_id AND direct.identity_digest = evidence.identity_digest JOIN ci_runs AS origin ON origin.job_id = evidence.origin_job_id
		WHERE evidence.accepted_generation IN (?, ?, ?) AND direct.disposition = 'executed' AND origin.accepted_generation = evidence.accepted_generation AND (` + directTerms + `)
		UNION ALL
		SELECT result.identity_digest, proof.origin_accepted_generation, result.workload_id, result.execution_digest, result.input_digest, result.environment_digest, proof.origin_job_id, proof.origin_source_tree_sha, proof.origin_receipt_set_sha256, proof.origin_execution_json, proof.evidence_sha256
		FROM ci_retained_workload_pass_proofs AS proof
		JOIN ci_run_workload_results AS result ON result.job_id = proof.consumer_job_id AND result.workload_id = proof.workload_id
		JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id
		WHERE consumer.accepted_generation IN (?, ?, ?) AND result.disposition = 'reused' AND result.identity_digest = proof.identity_digest AND result.origin_job_id = proof.origin_job_id AND result.origin_accepted_generation = proof.origin_accepted_generation AND result.evidence_sha256 = proof.evidence_sha256 AND (` + proofTerms + `)
		ORDER BY 3, 2 DESC, 1`
	return query, args
}

type workloadPassEvidenceScanner interface {
	Scan(dest ...any) error
}

// scanWorkloadPassSourceReplayCandidate 解码并校验候选代数，内容 proof 留给 origin-aware validator。
func scanWorkloadPassSourceReplayCandidate(rows workloadPassEvidenceScanner) (WorkloadPassEvidence, error) {
	var evidence WorkloadPassEvidence
	var generation, workloadID, executionJSON string
	if err := rows.Scan(
		&evidence.Identity.IdentityDigest, &generation, &workloadID,
		&evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest,
		&evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256,
		&executionJSON, &evidence.EvidenceSHA256,
	); err != nil {
		return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("scan workload PASS source replay candidate", err)
	}
	evidence.Identity.WorkloadID = GateID(workloadID)
	parsedGeneration, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || parsedGeneration == 0 {
		return WorkloadPassEvidence{}, errors.New("workload PASS source replay generation is invalid")
	}
	evidence.OriginAcceptedGeneration = parsedGeneration
	if err := decodeStoredWorkloadPassExecutionJSON(executionJSON, &evidence.OriginExecution); err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("decode workload PASS source replay execution: %w", err)
	}
	return evidence, nil
}

// loadSQLiteWorkloadPassReplaySource 按直接 origin run/workload 唯一定位来源证据。
func loadSQLiteWorkloadPassReplaySource(tx *sql.Tx, result RemoteCIWorkloadResult) (WorkloadPassEvidence, error) {
	rows, err := tx.Query(`SELECT identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256
		FROM ci_workload_pass_evidence
		WHERE accepted_generation = ? AND origin_job_id = ? AND workload_id = ?
		ORDER BY identity_digest
		LIMIT 2`, strconv.FormatUint(result.OriginAcceptedGeneration, 10), result.OriginJobID, string(result.Identity.WorkloadID))
	if err != nil {
		return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("query workload PASS source replay origin", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("iterate workload PASS source replay origin", err)
		}
		return WorkloadPassEvidence{}, fmt.Errorf("reused workload result %q has no promoted evidence", result.Identity.WorkloadID)
	}
	source, err := scanWorkloadPassSourceReplayCandidate(rows)
	if err != nil {
		return WorkloadPassEvidence{}, err
	}
	if rows.Next() {
		return WorkloadPassEvidence{}, fmt.Errorf("reused workload result %q has ambiguous promoted source evidence", result.Identity.WorkloadID)
	}
	if err := rows.Err(); err != nil {
		return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("iterate workload PASS source replay origin", err)
	}
	return source, nil
}
