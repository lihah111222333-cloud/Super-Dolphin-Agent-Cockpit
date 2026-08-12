package gate

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// workloadPassEvidenceLookupBatchSize 限制单次 SQLite 身份查询的绑定变量数量。
const workloadPassEvidenceLookupBatchSize = 400

// workloadPassEvidenceLookupStats 记录规模回归需要的查询、来源加载与已验证选择计数。
// 生产调用传入 nil，不把任何事实缓存到本次 SQLite 事务之外。
type workloadPassEvidenceLookupStats struct {
	authorityTransactions        int
	identityBatchQueries         int
	originRunLoads               int
	originReceiptSetValidations  int
	originExecutionBatchQueries  int
	originResultBatchQueries     int
	retainedProofBatchQueries    int
	retainedConsumerBatchQueries int
	selectedDirectSources        int
	selectedRetainedSources      int
	provisionalProjectionReloads int
	loadedProjectionDigests      int
}

// workloadPassEvidenceOriginContext 保存一个来源 run 在当前只读事务内的一次加载结果。
type workloadPassEvidenceOriginContext struct {
	record            RemoteCIRunRecord
	receiptDigest     string
	canonical         map[GateID]Workload
	executionByID     map[GateID]PlanGateExecution
	currentGeneration uint64
}

// loadWorkloadPassReadProofBatches separates the direct origin path from the
// retained v16 consumer path before validation.  The source-replay reader uses
// the single-item entry point because it is not an identity batch lookup.
func loadWorkloadPassReadProofBatches(
	tx *sql.Tx,
	found map[string]WorkloadPassEvidence,
	currentGeneration uint64,
	stats *workloadPassEvidenceLookupStats,
) (map[string]workloadPassEvidenceOriginContext, map[string]retainedWorkloadPassProofRow, error) {
	values := make([]WorkloadPassEvidence, 0, len(found))
	for _, evidence := range found {
		values = append(values, evidence)
	}
	return loadWorkloadPassReadProofContexts(tx, values, currentGeneration, stats)
}

// loadWorkloadPassReadProofContexts 为普通 lookup 与 source replay 共用固定批次
// origin/consumer 校验，禁止调用方退回逐 evidence SQLite readback。
func loadWorkloadPassReadProofContexts(
	tx *sql.Tx,
	values []WorkloadPassEvidence,
	currentGeneration uint64,
	stats *workloadPassEvidenceLookupStats,
) (map[string]workloadPassEvidenceOriginContext, map[string]retainedWorkloadPassProofRow, error) {
	origins := make(map[string]workloadPassEvidenceOriginContext, len(values))
	retained := make(map[string]retainedWorkloadPassProofRow)
	if err := loadDirectWorkloadPassOriginBatches(tx, values, currentGeneration, origins, stats); err != nil {
		return nil, nil, err
	}
	retainedCandidates := workloadPassEvidenceWithoutDirectOrigins(values, origins)
	if err := loadRetainedWorkloadPassProofBatches(tx, retainedCandidates, currentGeneration, retained, stats); err != nil {
		return nil, nil, err
	}
	return origins, retained, nil
}

// workloadPassEvidenceWithoutDirectOrigins 只让已不存在直接 origin 的证据进入
// v16 consumer-proof 路径；provisional consumer 不能遮蔽仍存活的直接来源。
func workloadPassEvidenceWithoutDirectOrigins(evidence []WorkloadPassEvidence, origins map[string]workloadPassEvidenceOriginContext) []WorkloadPassEvidence {
	result := make([]WorkloadPassEvidence, 0, len(evidence))
	for _, item := range evidence {
		if _, direct := origins[item.OriginJobID]; !direct {
			result = append(result, item)
		}
	}
	return result
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
	found, err := loadWorkloadPassEvidenceBatches(tx, identities, retainedGenerations, stats)
	if err != nil {
		return nil, err
	}
	return validateAndOrderWorkloadPassEvidence(tx, identities, found, currentGeneration, stats)
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
	stats *workloadPassEvidenceLookupStats,
) (map[string]WorkloadPassEvidence, error) {
	found := make(map[string]WorkloadPassEvidence, len(identities))
	for start := 0; start < len(identities); start += workloadPassEvidenceLookupBatchSize {
		end := min(start+workloadPassEvidenceLookupBatchSize, len(identities))
		batch, err := loadWorkloadPassEvidenceBatch(tx, identities[start:end], retainedGenerations, stats)
		if err != nil {
			return nil, err
		}
		maps.Copy(found, batch)
	}
	return found, nil
}

// validateAndOrderWorkloadPassEvidence 按请求顺序验证证据，并按来源 run 复用上下文。
func validateAndOrderWorkloadPassEvidence(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	found map[string]WorkloadPassEvidence,
	currentGeneration uint64,
	stats *workloadPassEvidenceLookupStats,
) ([]WorkloadPassEvidence, error) {
	// The lookup path must not turn a fixed identity query into one SQLite
	// origin readback per hit.  Source-replay keeps the single-item validator;
	// this multi-item path preloads direct origins and v16 consumer proofs.
	origins, retained, err := loadWorkloadPassReadProofBatches(tx, found, currentGeneration, stats)
	if err != nil {
		return nil, err
	}
	result := make([]WorkloadPassEvidence, 0, len(found))
	for _, identity := range identities {
		evidence, ok := found[identity.IdentityDigest]
		if !ok {
			continue
		}
		if origin, ok := origins[evidence.OriginJobID]; ok {
			if err := validateStoredWorkloadPassEvidenceWithOriginContext(tx, origin, evidence); err != nil {
				return nil, err
			}
			if stats != nil {
				stats.selectedDirectSources++
			}
		} else if proof, ok := retained[evidence.Identity.IdentityDigest]; ok {
			if err := proof.validate(evidence, currentGeneration); err != nil {
				return nil, err
			}
			if stats != nil {
				stats.selectedRetainedSources++
			}
		} else {
			return nil, errors.New("workload pass evidence has neither direct origin nor v16 retained proof")
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
	return scanWorkloadPassEvidenceBatchRows(rows, requested)
}

// workloadPassEvidenceBatchQuery 构造使用 identity/generation 复合索引的分块 SQL。
func workloadPassEvidenceBatchQuery(identities []WorkloadPassIdentity, retainedGenerations [3]string) (string, []any) {
	rows := make([]string, 0, len(identities))
	args := make([]any, 0, len(identities)+len(retainedGenerations)*2)
	for _, identity := range identities {
		rows = append(rows, "(?)")
		args = append(args, identity.IdentityDigest)
	}
	query := `WITH requested(identity_digest) AS (VALUES ` + strings.Join(rows, ", ") + `)
	SELECT evidence.identity_digest, evidence.accepted_generation, evidence.workload_id, evidence.execution_digest, evidence.input_digest, evidence.environment_digest, evidence.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, evidence.evidence_sha256, evidence.accepted_generation, 'direct'
	FROM requested JOIN ci_workload_pass_evidence AS evidence ON evidence.identity_digest = requested.identity_digest
	LEFT JOIN ci_run_workload_results AS direct ON direct.job_id = evidence.origin_job_id AND direct.workload_id = evidence.workload_id AND direct.identity_digest = evidence.identity_digest
	LEFT JOIN ci_runs AS origin ON origin.job_id = evidence.origin_job_id
	WHERE evidence.accepted_generation IN (?, ?, ?)
	UNION
	SELECT proof.identity_digest, proof.origin_accepted_generation, proof.workload_id, '', '', '', proof.origin_job_id, proof.origin_source_tree_sha, proof.origin_receipt_set_sha256,
		CASE WHEN json_type(proof.origin_execution_json, '$.schema_version') IS NOT NULL THEN json_extract(proof.origin_execution_json, '$.execution') ELSE proof.origin_execution_json END,
		proof.evidence_sha256, COALESCE(consumer.accepted_generation, ''), 'retained'
	FROM requested CROSS JOIN ci_retained_workload_pass_proofs AS proof INDEXED BY idx_ci_retained_workload_pass_proofs_lookup ON proof.identity_digest = requested.identity_digest
	LEFT JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id
	WHERE (consumer.accepted_generation IN (?, ?, ?) OR consumer.job_id IS NULL)
		AND (consumer.authoritative = 1 OR EXISTS (SELECT 1 FROM ci_check_receipts AS receipt WHERE receipt.job_id = proof.consumer_job_id))
	ORDER BY 1, 12 DESC, 13`
	for _, generation := range retainedGenerations {
		args = append(args, generation)
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
) (map[string]WorkloadPassEvidence, error) {
	found := make(map[string]WorkloadPassEvidence, len(requested))
	for rows.Next() {
		identityDigest, evidence, skip, err := decodeWorkloadPassEvidenceBatchRow(rows, requested, found)
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
) (string, WorkloadPassEvidence, bool, error) {
	var (
		identityDigest, generation, workloadID, executionJSON, consumerGeneration, source string
		evidence                                                                          WorkloadPassEvidence
	)
	if err := rows.Scan(&identityDigest, &generation, &workloadID, &evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest, &evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256, &consumerGeneration, &source); err != nil {
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
	if err := bindBatchWorkloadPassEvidenceIdentity(&evidence, identity, identityDigest, workloadID, consumerGeneration, source); err != nil {
		return "", WorkloadPassEvidence{}, false, err
	}
	parsedGeneration, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || parsedGeneration == 0 {
		return "", WorkloadPassEvidence{}, false, errors.New("stored workload pass evidence generation is invalid")
	}
	evidence.OriginAcceptedGeneration = parsedGeneration
	if err := decodeStoredWorkloadPassExecutionJSON(executionJSON, &evidence.OriginExecution); err != nil {
		return "", WorkloadPassEvidence{}, false, fmt.Errorf("decode stored workload pass execution: %w", err)
	}
	if !workloadPassIdentityMatches(evidence.Identity, identity) {
		return "", WorkloadPassEvidence{}, false, errors.New("stored workload pass evidence identity does not match lookup request")
	}
	return identityDigest, evidence, false, nil
}

// bindBatchWorkloadPassEvidenceIdentity keeps retained proof discovery separate
// from its later strict consumer/result validation.
func bindBatchWorkloadPassEvidenceIdentity(evidence *WorkloadPassEvidence, requested WorkloadPassIdentity, identityDigest, workloadID, consumerGeneration, source string) error {
	if source == "retained" {
		if consumerGeneration == "" {
			return errors.New("retained workload pass proof consumer is missing")
		}
		evidence.Identity = requested
		if workloadID != string(requested.WorkloadID) {
			return errors.New("retained workload pass proof workload binding drifted")
		}
		return nil
	}
	if source == "direct" {
		evidence.Identity.IdentityDigest = identityDigest
		evidence.Identity.WorkloadID = GateID(workloadID)
		return nil
	}
	return errors.New("batch workload pass evidence source is invalid")
}

// decodeStoredWorkloadPassExecutionJSON 严格解码持久化 execution，拒绝未知字段和尾随值。
func decodeStoredWorkloadPassExecutionJSON(encoded string, target *PlanGateExecution) error {
	if target == nil {
		return errors.New("stored workload pass execution target is nil")
	}
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

// loadWorkloadPassEvidenceBaseOriginContext 加载基础证据的来源 run、receipt 和 provisional 索引。
func loadWorkloadPassEvidenceBaseOriginContext(
	tx *sql.Tx,
	evidence WorkloadPassEvidence,
	currentGeneration uint64,
	stats *workloadPassEvidenceLookupStats,
) (workloadPassEvidenceOriginContext, error) {
	record, err := loadRemoteCIRunRow(tx, evidence.OriginJobID)
	if err != nil {
		return workloadPassEvidenceOriginContext{}, err
	}
	if err := loadWorkloadPassEvidenceOriginDetails(tx, &record); err != nil {
		return workloadPassEvidenceOriginContext{}, err
	}
	if stats != nil {
		stats.originRunLoads++
	}
	receiptDigest, err := workloadReceiptSetSHA256WithStats(tx, record, stats)
	if err != nil {
		return workloadPassEvidenceOriginContext{}, err
	}
	if stats != nil {
		stats.originReceiptSetValidations++
	}
	origin := workloadPassEvidenceOriginContext{record: record, receiptDigest: receiptDigest, currentGeneration: currentGeneration}
	if err := populateProvisionalWorkloadPassEvidenceOrigin(tx, &origin); err != nil {
		return workloadPassEvidenceOriginContext{}, err
	}
	return origin, nil
}

// loadWorkloadPassEvidenceOriginDetails 加载 PASS proof 所需的 typed scope 和
// workload 投影。权威 run 的完整 check receipt 与 cleanup 终态已在同一事务
// 重验，无需为每个历史 origin 重解码 shard、timing 和 warning。
func loadWorkloadPassEvidenceOriginDetails(tx *sql.Tx, record *RemoteCIRunRecord) error {
	if record == nil {
		return errors.New("workload pass evidence origin record is nil")
	}
	var err error
	record.Scope, err = loadRemoteCIExecutionScope(tx, record.JobID, record.AcceptedGeneration)
	if err != nil {
		return err
	}
	if !record.Authoritative && remoteCIProvisionalFailureStatus(record.Status) && record.CleanupComplete {
		return loadRemoteCIRunDetails(tx, record.JobID, record)
	}
	record.WorkloadExecutions, err = loadRemoteCIWorkloadExecutionRows(tx, record.JobID)
	if err != nil {
		return err
	}
	record.WorkloadResults, err = loadRemoteCIWorkloadResults(tx, record.JobID)
	return err
}

// populateProvisionalWorkloadPassEvidenceOrigin 只为清理完成的失败 run 加载
// catalog/execution 索引；权威 run 不引入兼容性 fallback。
func populateProvisionalWorkloadPassEvidenceOrigin(tx *sql.Tx, origin *workloadPassEvidenceOriginContext) error {
	if origin.record.Authoritative {
		return nil
	}
	if !remoteCIProvisionalFailureStatus(origin.record.Status) {
		return nil
	}
	if !origin.record.CleanupComplete {
		return nil
	}
	catalog, err := loadSQLiteWorkloadCatalog(tx, origin.record.CatalogDigest)
	if err != nil {
		return fmt.Errorf("load stored provisional workload catalog: %w", err)
	}
	origin.canonical = indexCanonicalWorkloadPassCatalog(catalog.Catalog)
	origin.executionByID = indexWorkloadExecutions(origin.record.WorkloadExecutions)
	return nil
}

// validateStoredWorkloadPassEvidenceWithOriginContext 逐项验证 identity/evidence，并复用
// 已加载的来源 run、receipt digest 和 provisional 索引。
func validateStoredWorkloadPassEvidenceWithOriginContext(
	tx *sql.Tx,
	origin workloadPassEvidenceOriginContext,
	evidence WorkloadPassEvidence,
) error {
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("stored workload pass evidence: %w", err)
	}
	return validateStoredWorkloadPassEvidenceBase(tx, origin, evidence)
}

// validateStoredWorkloadPassEvidenceBase 验证基础证据的 origin、运行终态、provisional 执行和 receipt。
func validateStoredWorkloadPassEvidenceBase(
	tx *sql.Tx,
	origin workloadPassEvidenceOriginContext,
	evidence WorkloadPassEvidence,
) error {
	if tx == nil {
		return errors.New("stored workload pass evidence validation transaction is nil")
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(origin.currentGeneration, evidence.OriginAcceptedGeneration); err != nil {
		return fmt.Errorf("stored workload pass evidence generation is outside retained authority window: %w", err)
	}
	if err := validateStoredWorkloadPassEvidenceOrigin(origin.record, evidence); err != nil {
		return err
	}
	if err := validateStoredWorkloadPassEvidenceRunState(origin, evidence); err != nil {
		return err
	}
	if err := validateStoredWorkloadPassEvidenceExecution(origin.record, evidence); err != nil {
		return err
	}
	// A promoted evidence row must terminate at the origin run's direct
	// executed projection.  Without this check a reused row could form a
	// reuse chain while retaining an otherwise valid execution JSON proof.
	if err := validateDirectExecutedWorkloadResult(origin.record, evidence); err != nil {
		return fmt.Errorf("stored workload pass evidence origin proof: %w", err)
	}
	if origin.receiptDigest != evidence.OriginReceiptSetSHA256 {
		return errors.New("stored workload pass evidence receipt set is missing or tampered")
	}
	return nil
}

// validateDirectExecutedWorkloadResult 在已回读的 origin projection 中确认 workload 是直接 executed 行，避免大批量查询逐条访问 SQLite。
func validateDirectExecutedWorkloadResult(record RemoteCIRunRecord, evidence WorkloadPassEvidence) error {
	var matched *RemoteCIWorkloadResult
	for index := range record.WorkloadResults {
		candidate := &record.WorkloadResults[index]
		if candidate.Identity.WorkloadID != evidence.Identity.WorkloadID {
			continue
		}
		if matched != nil {
			return fmt.Errorf("reused workload result %q origin is duplicated", evidence.Identity.WorkloadID)
		}
		matched = candidate
	}
	if matched == nil {
		return fmt.Errorf("reused workload result %q origin executed result is missing", evidence.Identity.WorkloadID)
	}
	if matched.Disposition != WorkloadDispositionExecuted {
		return fmt.Errorf("reused workload result %q origin is not executed", evidence.Identity.WorkloadID)
	}
	if matched.OriginJobID != evidence.OriginJobID || matched.OriginAcceptedGeneration != evidence.OriginAcceptedGeneration {
		return fmt.Errorf("reused workload result %q origin identity is not direct", evidence.Identity.WorkloadID)
	}
	if matched.EvidenceSHA256 != "" {
		return fmt.Errorf("reused workload result %q origin executed row carries evidence", evidence.Identity.WorkloadID)
	}
	if matched.Identity != evidence.Identity {
		return fmt.Errorf("reused workload result %q origin identity digest drifted", evidence.Identity.WorkloadID)
	}
	return nil
}

// validateStoredWorkloadPassEvidenceRunState 严格确认来源 run 的权威终态或清理失败终态。
func validateStoredWorkloadPassEvidenceRunState(origin workloadPassEvidenceOriginContext, evidence WorkloadPassEvidence) error {
	if origin.record.Authoritative {
		if origin.record.Status != ResultStatusPassed || !origin.record.CleanupComplete {
			return errors.New("stored workload pass evidence authoritative run is not passed and cleaned")
		}
		return nil
	}
	if !remoteCIProvisionalFailureStatus(origin.record.Status) || !origin.record.CleanupComplete {
		return errors.New("stored workload pass evidence provisional run is not a cleaned failure")
	}
	return validateProvisionalWorkloadPassEvidenceWithContext(origin.record, evidence, origin.canonical, origin.executionByID)
}

// validateStoredWorkloadPassEvidenceExecution 逐 workload 比对来源 execution 的规范 JSON。
func validateStoredWorkloadPassEvidenceExecution(record RemoteCIRunRecord, evidence WorkloadPassEvidence) error {
	var originExecution PlanGateExecution
	matchCount := 0
	for _, candidate := range record.WorkloadExecutions {
		if candidate.GateID != evidence.Identity.WorkloadID {
			continue
		}
		matchCount++
		originExecution = candidate
	}
	if matchCount == 0 {
		return fmt.Errorf("stored workload pass evidence origin execution %q is missing", evidence.Identity.WorkloadID)
	}
	if matchCount > 1 {
		return fmt.Errorf("stored workload pass evidence origin execution %q is duplicated", evidence.Identity.WorkloadID)
	}
	storedExecutionJSON, err := json.Marshal(originExecution)
	if err != nil {
		return fmt.Errorf("encode stored workload pass origin execution: %w", err)
	}
	evidenceExecutionJSON, err := json.Marshal(evidence.OriginExecution)
	if err != nil {
		return fmt.Errorf("encode stored workload pass evidence execution: %w", err)
	}
	if !bytes.Equal(storedExecutionJSON, evidenceExecutionJSON) {
		return errors.New("stored workload pass evidence origin execution JSON does not match origin run")
	}
	return nil
}
