package gate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// workloadPassEvidenceLookupBatchSize 限制单次 SQLite 身份查询的绑定变量数量。
const workloadPassEvidenceLookupBatchSize = 400

// workloadPassEvidenceLookupStats 记录规模回归需要的查询与来源加载计数。
// 生产调用传入 nil，不把任何事实缓存到本次 SQLite 事务之外。
type workloadPassEvidenceLookupStats struct {
	identityBatchQueries        int
	originRunLoads              int
	originReceiptSetValidations int
}

// workloadPassEvidenceOriginContext 保存一个来源 run 在当前只读事务内的一次加载结果。
type workloadPassEvidenceOriginContext struct {
	record        RemoteCIRunRecord
	receiptDigest string
	canonical     map[GateID]Workload
	executionByID map[GateID]PlanGateExecution
}

// loadWorkloadPassEvidenceForIdentities 批量读取身份的最新证据，并按 origin_job_id
// 一次加载和验证来源 run、回执集合及 provisional 的 catalog/execution 索引。
func loadWorkloadPassEvidenceForIdentities(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	currentGeneration uint64,
) ([]WorkloadPassEvidence, error) {
	return loadWorkloadPassEvidenceForIdentitiesWithStats(tx, identities, currentGeneration, nil)
}

// loadWorkloadPassEvidenceForIdentitiesWithStats 执行分块查询并复用来源验证上下文。
func loadWorkloadPassEvidenceForIdentitiesWithStats(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	currentGeneration uint64,
	stats *workloadPassEvidenceLookupStats,
) ([]WorkloadPassEvidence, error) {
	if tx == nil {
		return nil, errors.New("workload pass evidence lookup transaction is nil")
	}
	if err := validateUniqueWorkloadPassLookupIdentities(identities); err != nil {
		return nil, err
	}
	retainedGenerations := retainedWorkloadPassGenerations(currentGeneration)
	found, err := loadWorkloadPassEvidenceBatches(tx, identities, retainedGenerations, currentGeneration, stats)
	if err != nil {
		return nil, err
	}
	return validateAndOrderWorkloadPassEvidence(tx, identities, found, stats)
}

// validateUniqueWorkloadPassLookupIdentities 拒绝同一身份在一次查询中的重复请求。
func validateUniqueWorkloadPassLookupIdentities(identities []WorkloadPassIdentity) error {
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		if _, duplicate := seen[identity.IdentityDigest]; duplicate {
			return fmt.Errorf("workload pass evidence lookup identity %q is duplicated", identity.WorkloadID)
		}
		seen[identity.IdentityDigest] = struct{}{}
	}
	return nil
}

// loadWorkloadPassEvidenceBatches 执行有界数量的身份分块查询并合并结果。
func loadWorkloadPassEvidenceBatches(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	retainedGenerations [3]string,
	currentGeneration uint64,
	stats *workloadPassEvidenceLookupStats,
) (map[string]WorkloadPassEvidence, error) {
	found := make(map[string]WorkloadPassEvidence, len(identities))
	for start := 0; start < len(identities); start += workloadPassEvidenceLookupBatchSize {
		end := start + workloadPassEvidenceLookupBatchSize
		if end > len(identities) {
			end = len(identities)
		}
		batch, err := loadWorkloadPassEvidenceBatch(tx, identities[start:end], retainedGenerations, currentGeneration, stats)
		if err != nil {
			return nil, err
		}
		for identityDigest, evidence := range batch {
			found[identityDigest] = evidence
		}
	}
	return found, nil
}

// validateAndOrderWorkloadPassEvidence 按请求顺序验证证据，并按来源 run 复用上下文。
func validateAndOrderWorkloadPassEvidence(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	found map[string]WorkloadPassEvidence,
	stats *workloadPassEvidenceLookupStats,
) ([]WorkloadPassEvidence, error) {
	result := make([]WorkloadPassEvidence, 0, len(found))
	origins := make(map[string]workloadPassEvidenceOriginContext)
	for _, identity := range identities {
		evidence, ok := found[identity.IdentityDigest]
		if !ok {
			continue
		}
		originKey := evidence.OriginJobID
		origin, ok := origins[originKey]
		if !ok {
			var err error
			origin, err = loadWorkloadPassEvidenceOriginContext(tx, evidence, stats)
			if err != nil {
				if errors.Is(err, errLegacyRemoteCIExecutionProfile) {
					// The origin predates the current execution semantic identity. It
					// is a strict MISS, never a compatibility decode or fallback hit.
					continue
				}
				return nil, err
			}
			origins[originKey] = origin
		}
		if err := validateStoredWorkloadPassEvidenceWithOriginContext(origin, evidence); err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	return result, nil
}

// loadWorkloadPassEvidenceBatch 按 identity_digest 分块查询，每个身份只保留
// 与原有 LIMIT 1 查询相同的最新代行，并使用既有复合索引。
func loadWorkloadPassEvidenceBatch(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	retainedGenerations [3]string,
	currentGeneration uint64,
	stats *workloadPassEvidenceLookupStats,
) (map[string]WorkloadPassEvidence, error) {
	if len(identities) == 0 {
		return nil, nil
	}
	query, args := workloadPassEvidenceBatchQuery(identities, retainedGenerations)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("batch query workload pass evidence", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.identityBatchQueries++
	}
	requested := make(map[string]WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		requested[identity.IdentityDigest] = identity
	}
	return scanWorkloadPassEvidenceBatchRows(rows, requested, currentGeneration)
}

// workloadPassEvidenceBatchQuery 构造使用 identity/generation 复合索引的分块 SQL。
func workloadPassEvidenceBatchQuery(identities []WorkloadPassIdentity, retainedGenerations [3]string) (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(identities)), ",")
	query := `SELECT evidence.identity_digest, evidence.accepted_generation, evidence.workload_id, evidence.execution_digest, evidence.input_digest, evidence.environment_digest, evidence.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, evidence.evidence_sha256
		FROM ci_workload_pass_evidence AS evidence
		INNER JOIN ci_runs AS runs
			ON runs.job_id = evidence.origin_job_id
			AND runs.accepted_generation = evidence.accepted_generation
			AND runs.source_tree_sha = evidence.origin_source_tree_sha
		WHERE evidence.identity_digest IN (` + placeholders + `)
			AND evidence.accepted_generation IN (?, ?, ?)
			AND (
				(runs.status = 'passed' AND runs.authoritative = 1 AND runs.cleanup_complete = 1)
				OR (runs.authoritative = 0 AND runs.status IN ('failed', 'cancelled', 'timeout', 'infra_failed') AND runs.cleanup_complete = 1)
			)
		ORDER BY evidence.identity_digest, length(evidence.accepted_generation) DESC, evidence.accepted_generation DESC`
	args := make([]any, 0, len(identities)+len(retainedGenerations))
	for _, identity := range identities {
		args = append(args, identity.IdentityDigest)
	}
	for _, generation := range retainedGenerations {
		args = append(args, generation)
	}
	return query, args
}

// scanWorkloadPassEvidenceBatchRows 读取每个身份最新行并完成严格 JSON/代际校验。
func scanWorkloadPassEvidenceBatchRows(
	rows *sql.Rows,
	requested map[string]WorkloadPassIdentity,
	currentGeneration uint64,
) (map[string]WorkloadPassEvidence, error) {
	found := make(map[string]WorkloadPassEvidence, len(requested))
	for rows.Next() {
		identityDigest, evidence, skip, err := decodeWorkloadPassEvidenceBatchRow(rows, requested, found, currentGeneration)
		if err != nil {
			return nil, err
		}
		if skip {
			continue
		}
		found[identityDigest] = evidence
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate batch workload pass evidence", err)
	}
	return found, nil
}

// decodeWorkloadPassEvidenceBatchRow 解码一行最新证据；旧代重复行只跳过不解码。
func decodeWorkloadPassEvidenceBatchRow(
	rows *sql.Rows,
	requested map[string]WorkloadPassIdentity,
	found map[string]WorkloadPassEvidence,
	currentGeneration uint64,
) (string, WorkloadPassEvidence, bool, error) {
	var (
		identityDigest, generation, workloadID, executionJSON string
		evidence                                              WorkloadPassEvidence
	)
	if err := rows.Scan(&identityDigest, &generation, &workloadID, &evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest, &evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256); err != nil {
		return "", WorkloadPassEvidence{}, false, mapDurationLedgerSQLiteError("scan batch workload pass evidence", err)
	}
	identity, ok := requested[identityDigest]
	if !ok {
		return "", WorkloadPassEvidence{}, false, errors.New("batch workload pass evidence returned an unrequested identity")
	}
	// ORDER BY 使同一身份的首行与旧的 LIMIT 1 查询选择完全一致；旧代行不解码。
	if _, alreadyFound := found[identityDigest]; alreadyFound {
		return identityDigest, WorkloadPassEvidence{}, true, nil
	}
	evidence.Identity.IdentityDigest = identityDigest
	evidence.Identity.WorkloadID = GateID(workloadID)
	parsedGeneration, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || parsedGeneration == 0 {
		return "", WorkloadPassEvidence{}, false, errors.New("stored workload pass evidence generation is invalid")
	}
	evidence.OriginAcceptedGeneration = parsedGeneration
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(currentGeneration, parsedGeneration); err != nil {
		return "", WorkloadPassEvidence{}, false, err
	}
	if err := json.Unmarshal([]byte(executionJSON), &evidence.OriginExecution); err != nil {
		return "", WorkloadPassEvidence{}, false, fmt.Errorf("decode stored workload pass execution: %w", err)
	}
	if !workloadPassIdentityMatches(evidence.Identity, identity) {
		return "", WorkloadPassEvidence{}, false, errors.New("stored workload pass evidence identity does not match lookup request")
	}
	return identityDigest, evidence, false, nil
}

// loadWorkloadPassEvidenceOriginContext 在当前事务内只加载一次来源 run、完整关联投影
// 和 receipt set；provisional run 的 catalog/execution 索引也只建立一次。
func loadWorkloadPassEvidenceOriginContext(
	tx *sql.Tx,
	evidence WorkloadPassEvidence,
	stats *workloadPassEvidenceLookupStats,
) (workloadPassEvidenceOriginContext, error) {
	return loadWorkloadPassEvidenceBaseOriginContext(tx, evidence, stats)
}

// loadWorkloadPassEvidenceBaseOriginContext 加载基础证据的来源 run、receipt 和 provisional 索引。
func loadWorkloadPassEvidenceBaseOriginContext(
	tx *sql.Tx,
	evidence WorkloadPassEvidence,
	stats *workloadPassEvidenceLookupStats,
) (workloadPassEvidenceOriginContext, error) {
	record, err := loadRemoteCIRunRow(tx, evidence.OriginJobID)
	if err != nil {
		return workloadPassEvidenceOriginContext{}, err
	}
	if err := loadRemoteCIRunDetails(tx, evidence.OriginJobID, &record); err != nil {
		return workloadPassEvidenceOriginContext{}, err
	}
	if stats != nil {
		stats.originRunLoads++
	}
	receiptDigest, err := workloadReceiptSetSHA256(tx, record)
	if err != nil {
		return workloadPassEvidenceOriginContext{}, err
	}
	if stats != nil {
		stats.originReceiptSetValidations++
	}
	origin := workloadPassEvidenceOriginContext{record: record, receiptDigest: receiptDigest}
	if !record.Authoritative && remoteCIProvisionalFailureStatus(record.Status) && record.CleanupComplete {
		catalog, err := loadSQLiteWorkloadCatalog(tx, record.CatalogDigest)
		if err != nil {
			return workloadPassEvidenceOriginContext{}, fmt.Errorf("load stored provisional workload catalog: %w", err)
		}
		origin.canonical = indexCanonicalWorkloadPassCatalog(catalog.Catalog)
		origin.executionByID = indexWorkloadExecutions(record.WorkloadExecutions)
	}
	return origin, nil
}

// validateStoredWorkloadPassEvidenceWithOriginContext 逐项验证 identity/evidence，并复用
// 已加载的来源 run、receipt digest 和 provisional 索引。
func validateStoredWorkloadPassEvidenceWithOriginContext(
	origin workloadPassEvidenceOriginContext,
	evidence WorkloadPassEvidence,
) error {
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("stored workload pass evidence: %w", err)
	}
	return validateStoredWorkloadPassEvidenceBase(origin, evidence)
}

// validateStoredWorkloadPassEvidenceBase 验证基础证据的 origin、运行终态、provisional 执行和 receipt。
func validateStoredWorkloadPassEvidenceBase(
	origin workloadPassEvidenceOriginContext,
	evidence WorkloadPassEvidence,
) error {
	if err := validateStoredWorkloadPassEvidenceOrigin(origin.record, evidence); err != nil {
		return err
	}
	if origin.record.Authoritative {
		if origin.record.Status != ResultStatusPassed || !origin.record.CleanupComplete {
			return errors.New("stored workload pass evidence authoritative run is not passed and cleaned")
		}
	} else {
		if !remoteCIProvisionalFailureStatus(origin.record.Status) || !origin.record.CleanupComplete {
			return errors.New("stored workload pass evidence provisional run is not a cleaned failure")
		}
		if err := validateProvisionalWorkloadPassEvidenceWithContext(origin.record, evidence, origin.canonical, origin.executionByID); err != nil {
			return err
		}
	}
	if origin.receiptDigest != evidence.OriginReceiptSetSHA256 {
		return errors.New("stored workload pass evidence receipt set is missing or tampered")
	}
	return nil
}
