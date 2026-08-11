package gate

import (
	"bytes"
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
	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
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
	Domain            string `json:"domain"`
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
	payload, err := json.Marshal(workloadPassIdentityPayload{
		Domain:            cicontract.WorkloadPassIdentityDomain,
		WorkloadID:        identity.WorkloadID,
		ExecutionDigest:   identity.ExecutionDigest,
		InputDigest:       identity.InputDigest,
		EnvironmentDigest: identity.EnvironmentDigest,
	})
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
		legacyDigest, err := legacyWorkloadPassIdentitySHA256(identity)
		if err != nil {
			return err
		}
		if identity.IdentityDigest == legacyDigest {
			return fmt.Errorf("%w: workload pass identity uses the retired no-domain digest", errLegacyWorkloadPassIdentityDomain)
		}
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
func promoteSQLiteRemoteCIWorkloadPassEvidence(tx *sql.Tx, jobID string, verifiedIdentities map[GateID]WorkloadPassIdentity) error {
	record, err := loadPromotableRemoteCIRun(tx, jobID)
	if err != nil {
		return err
	}
	receiptDigest, err := workloadReceiptSetSHA256(tx, record)
	if err != nil {
		return err
	}
	executions := indexWorkloadExecutions(record.WorkloadExecutions)
	if err := promoteExecutedWorkloadPassEvidence(tx, record, receiptDigest, executions, verifiedIdentities); err != nil {
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
	if err := validateSQLiteRemoteCIRunCatalogCoverage(tx, record); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("validate promotable remote CI catalog coverage: %w", err)
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
	verifiedIdentities map[GateID]WorkloadPassIdentity,
) error {
	for _, result := range record.WorkloadResults {
		if result.Disposition != WorkloadDispositionExecuted {
			continue
		}
		execution, ok := executions[result.Identity.WorkloadID]
		if !ok {
			return fmt.Errorf("executed workload result %q lacks execution", result.Identity.WorkloadID)
		}
		identity, ok := verifiedIdentities[result.Identity.WorkloadID]
		if !ok || identity != result.Identity {
			return fmt.Errorf("executed workload result %q is not in the verified authority identity set", result.Identity.WorkloadID)
		}
		if err := insertWorkloadPassEvidence(tx, record, receiptDigest, identity, execution); err != nil {
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
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("validate workload pass evidence before SQLite insert: %w", err)
	}
	encoded, err := json.Marshal(execution)
	if err != nil {
		return fmt.Errorf("encode origin workload execution: %w", err)
	}
	generation := strconv.FormatUint(evidence.OriginAcceptedGeneration, 10)
	return insertWorkloadPassEvidenceRow(tx, evidence, generation, string(encoded))
}

// insertWorkloadPassEvidenceRow 执行 plain INSERT；唯一键冲突只在完整 proof
// 逐列相等时幂等成功，否则 fail-fast，绝不静默 upsert。
func insertWorkloadPassEvidenceRow(tx *sql.Tx, evidence WorkloadPassEvidence, generation, executionJSON string) error {
	_, err := tx.Exec(`INSERT INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, evidence.Identity.IdentityDigest, generation, string(evidence.Identity.WorkloadID), evidence.Identity.ExecutionDigest, evidence.Identity.InputDigest, evidence.Identity.EnvironmentDigest, evidence.OriginJobID, evidence.OriginSourceTreeSHA, evidence.OriginReceiptSetSHA256, executionJSON, evidence.EvidenceSHA256)
	if err == nil {
		return nil
	}
	if !isSQLiteConstraintError(err) {
		return mapDurationLedgerSQLiteError("promote workload pass evidence", err)
	}
	stored, scanErr := loadWorkloadPassEvidenceProof(tx, evidence.Identity.IdentityDigest, generation)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return mapDurationLedgerSQLiteError("promote workload pass evidence", err)
	}
	if scanErr != nil {
		return mapDurationLedgerSQLiteError("reload conflicting workload pass evidence", scanErr)
	}
	expected := workloadPassEvidenceProof{identityDigest: evidence.Identity.IdentityDigest, acceptedGeneration: generation, workloadID: string(evidence.Identity.WorkloadID), executionDigest: evidence.Identity.ExecutionDigest, inputDigest: evidence.Identity.InputDigest, environmentDigest: evidence.Identity.EnvironmentDigest, originJobID: evidence.OriginJobID, originSourceTreeSHA: evidence.OriginSourceTreeSHA, originReceiptSetSHA256: evidence.OriginReceiptSetSHA256, originExecutionJSON: executionJSON, evidenceSHA256: evidence.EvidenceSHA256}
	if !stored.matches(expected) {
		return errors.New("conflicting workload pass evidence proof for identity and accepted generation")
	}
	return nil
}

// isSQLiteConstraintError 只识别 SQLite constraint 主码，避免把 busy/io 错误当作幂等碰撞。
func isSQLiteConstraintError(err error) bool {
	sqliteErr, ok := errors.AsType[*sqlitedriver.Error](err)
	return ok && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
}

type workloadPassEvidenceProof struct {
	identityDigest, acceptedGeneration, workloadID, executionDigest, inputDigest, environmentDigest string
	originJobID, originSourceTreeSHA, originReceiptSetSHA256, originExecutionJSON, evidenceSHA256   string
}

// loadWorkloadPassEvidenceProof 读取 identity/generation 冲突行的全部规范列。
func loadWorkloadPassEvidenceProof(tx *sql.Tx, identityDigest, generation string) (workloadPassEvidenceProof, error) {
	var proof workloadPassEvidenceProof
	err := tx.QueryRow(`SELECT identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256 FROM ci_workload_pass_evidence WHERE identity_digest = ? AND accepted_generation = ?`, identityDigest, generation).Scan(
		&proof.identityDigest, &proof.acceptedGeneration, &proof.workloadID, &proof.executionDigest, &proof.inputDigest, &proof.environmentDigest,
		&proof.originJobID, &proof.originSourceTreeSHA, &proof.originReceiptSetSHA256, &proof.originExecutionJSON, &proof.evidenceSHA256,
	)
	return proof, err
}

// matches 逐列比较 canonical proof，确保幂等仅接受完整字节相等。
func (proof workloadPassEvidenceProof) matches(expected workloadPassEvidenceProof) bool {
	actualFields := []string{proof.identityDigest, proof.acceptedGeneration, proof.workloadID, proof.executionDigest, proof.inputDigest, proof.environmentDigest, proof.originJobID, proof.originSourceTreeSHA, proof.originReceiptSetSHA256, proof.originExecutionJSON, proof.evidenceSHA256}
	expectedFields := []string{expected.identityDigest, expected.acceptedGeneration, expected.workloadID, expected.executionDigest, expected.inputDigest, expected.environmentDigest, expected.originJobID, expected.originSourceTreeSHA, expected.originReceiptSetSHA256, expected.originExecutionJSON, expected.evidenceSHA256}
	for index := range actualFields {
		if actualFields[index] != expectedFields[index] {
			return false
		}
	}
	return true
}

// LookupWorkloadPassEvidence 仅返回当前 accepted 代及前两代中的最新保留证据。
func (store *DurationLedgerStore) LookupWorkloadPassEvidence(identities []WorkloadPassIdentity) ([]WorkloadPassEvidence, error) {
	return store.lookupWorkloadPassEvidenceWithStats(identities, nil, nil)
}

// lookupWorkloadPassEvidenceWithStats 共享精确 evidence authority 路径；expectedGeneration
// 非空时在同一只读快照中拒绝 accepted generation 漂移。
func (store *DurationLedgerStore) lookupWorkloadPassEvidenceWithStats(
	identities []WorkloadPassIdentity,
	expectedGeneration *uint64,
	stats *workloadPassEvidenceLookupStats,
) ([]WorkloadPassEvidence, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if err := validateWorkloadPassIdentities(identities); err != nil {
		return nil, err
	}
	// 首次读取缺失 authority 时按契约原子初始化当前 schema/index；accepted baseline
	// 仍由下方读取严格验证，空库不会生成默认代或放宽 PASS。
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin workload pass evidence lookup", err)
	}
	defer tx.Rollback()
	if stats != nil {
		stats.authorityTransactions++
	}
	return lookupWorkloadPassEvidenceTransaction(tx, identities, expectedGeneration, stats)
}

// lookupWorkloadPassEvidenceTransaction 在同一 accepted-generation 快照中读取、验证并提交精确证据。
func lookupWorkloadPassEvidenceTransaction(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	expectedGeneration *uint64,
	stats *workloadPassEvidenceLookupStats,
) ([]WorkloadPassEvidence, error) {
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return nil, fmt.Errorf("load workload evidence accepted baseline generation: %w", err)
	}
	if expectedGeneration != nil && currentGeneration != *expectedGeneration {
		return nil, errors.New("workload PASS evidence expected generation is no longer current")
	}
	result, err := loadWorkloadPassEvidenceForIdentitiesWithStats(tx, identities, currentGeneration, stats)
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

// retainedWorkloadPassGenerations 返回当前 accepted 代及最多两个连续前代的规范存储值。
func retainedWorkloadPassGenerations(current uint64) [3]string {
	return [3]string{
		strconv.FormatUint(current, 10),
		strconv.FormatUint(current-min(current-1, 1), 10),
		strconv.FormatUint(current-min(current-1, 2), 10),
	}
}

// workloadReceiptSetSHA256 对完整 current check receipt 集合或失败 provisional
// 的完整 SQLite workload projection 重算规范摘要。
func workloadReceiptSetSHA256(tx *sql.Tx, record RemoteCIRunRecord) (string, error) {
	return workloadReceiptSetSHA256WithStats(tx, record, nil)
}

// workloadReceiptSetSHA256WithStats 为真实 SQLite 性能回归记录 provisional
// projection 是否发生二次回读；nil 不改变生产行为。
func workloadReceiptSetSHA256WithStats(tx *sql.Tx, record RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) (string, error) {
	if !record.Authoritative && remoteCIProvisionalFailureStatus(record.Status) && record.CleanupComplete {
		if stats != nil {
			stats.loadedProjectionDigests++
		}
		return digestProvisionalWorkloadProjection(record)
	}
	receipts, err := loadCheckReceiptsForEvidence(tx, record.JobID)
	if err != nil {
		return "", err
	}
	if err := validateStoredWorkloadReceiptCollection(receipts); err != nil {
		return "", err
	}
	if err := validateWorkloadReceiptSetBinding(tx, record, receipts); err != nil {
		return "", err
	}
	return digestWorkloadReceiptSet(receipts)
}

// validateStoredWorkloadReceiptCollection 逐条校验 SQLite 回执，再固定同一集合的共享身份。
func validateStoredWorkloadReceiptCollection(receipts []CheckReceiptRecord) error {
	if len(receipts) == 0 {
		return errors.New("stored workload evidence check receipts are empty")
	}
	for index, receipt := range receipts {
		if err := validateCheckReceiptRecord(receipt); err != nil {
			return fmt.Errorf("stored workload evidence check receipt[%d]: %w", index, err)
		}
	}
	if _, err := validatePassingCheckReceiptCollection(receipts); err != nil {
		return fmt.Errorf("stored workload evidence check receipt collection: %w", err)
	}
	return nil
}

// validateWorkloadReceiptSetBinding 从持久化完整目录投影本次 full/subset 范围，
// 再校验 promotion 回执集合与不可变运行身份绑定。
func validateWorkloadReceiptSetBinding(tx *sql.Tx, record RemoteCIRunRecord, receipts []CheckReceiptRecord) error {
	executionCatalog, err := storedWorkloadReceiptExecutionCatalog(tx, record)
	if err != nil {
		return err
	}
	if err := validateWorkloadCatalogPassingCheckReceipts(executionCatalog, receipts); err != nil {
		return fmt.Errorf("stored complete check receipts: %w", err)
	}
	return validateCheckReceiptsAgainstRemoteRun(record, receipts)
}

// validateCheckReceiptsAgainstRemoteRun 严格绑定全部回执与所属 run，RunID 必须等于记录 JobID；调用方负责各自的 catalog scope coverage。
func validateCheckReceiptsAgainstRemoteRun(record RemoteCIRunRecord, receipts []CheckReceiptRecord) error {
	if len(receipts) == 0 {
		return errors.New("check receipt set is empty")
	}
	for _, receipt := range receipts {
		if receipt.RunID != record.JobID || receipt.JobID != record.JobID || receipt.AgentTokenDigest != record.AgentTokenDigest || receipt.Force != record.Force || receipt.CandidateTreeSHA != record.SourceTreeSHA || receipt.AcceptedGeneration != record.AcceptedGeneration || receipt.AcceptedSnapshotID != record.ImageCacheSnapshotID {
			return errors.New("check receipt set does not bind promotion run")
		}
	}
	return nil
}

// storedWorkloadReceiptExecutionCatalog loads the sole persisted catalog then
// applies the run's typed full/subset scope for receipt validation.
func storedWorkloadReceiptExecutionCatalog(tx *sql.Tx, record RemoteCIRunRecord) (WorkloadCatalog, error) {
	catalog, err := loadSQLiteWorkloadCatalog(tx, record.CatalogDigest)
	if err != nil {
		return WorkloadCatalog{}, fmt.Errorf("load stored workload catalog for receipt binding: %w", err)
	}
	executionCatalog, err := ProjectRemoteCIExecutionCatalog(catalog.Catalog, record.Scope)
	if err != nil {
		return WorkloadCatalog{}, fmt.Errorf("project stored workload catalog for receipt binding: %w", err)
	}
	return executionCatalog, nil
}

// digestWorkloadReceiptSet 对 canonical receipt 摘要排序后计算规范集合 digest。
func digestWorkloadReceiptSet(receipts []CheckReceiptRecord) (string, error) {
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
	rows, err := tx.Query(`SELECT run_id, job_id, candidate_tree_sha, agent_token_digest, accepted_generation, accepted_snapshot_id, required_check, executed, reused, reuse_proof_sha256, passed, force, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256 FROM ci_check_receipts WHERE job_id = ? ORDER BY required_check`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query workload evidence check receipts", err)
	}
	defer rows.Close()
	var receipts []CheckReceiptRecord
	for rows.Next() {
		receipt, err := scanWorkloadEvidenceReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate workload evidence check receipts", err)
	}
	if err := validateStoredWorkloadReceiptCollection(receipts); err != nil {
		return nil, err
	}
	return receipts, nil
}

// scanWorkloadEvidenceReceipt 解码单条回执，先拒绝 SQLite 布尔和 generation 编码漂移。
func scanWorkloadEvidenceReceipt(rows *sql.Rows) (CheckReceiptRecord, error) {
	var receipt CheckReceiptRecord
	var generation, check string
	var executed, reused, passed, force int
	var started, completed, duration int64
	if err := rows.Scan(&receipt.RunID, &receipt.JobID, &receipt.CandidateTreeSHA, &receipt.AgentTokenDigest, &generation, &receipt.AcceptedSnapshotID, &check, &executed, &reused, &receipt.ReuseProofSHA256, &passed, &force, &started, &completed, &duration, &receipt.ReceiptSHA256); err != nil {
		return CheckReceiptRecord{}, mapDurationLedgerSQLiteError("scan workload evidence check receipt", err)
	}
	if err := validateStoredWorkloadReceiptBooleans(executed, reused, passed); err != nil {
		return CheckReceiptRecord{}, err
	}
	parsedGeneration, err := strconv.ParseUint(generation, 10, 64)
	if err != nil {
		return CheckReceiptRecord{}, errors.New("stored workload evidence receipt generation is invalid")
	}
	receipt.AcceptedGeneration = parsedGeneration
	receipt.RequiredCheck = cicontract.RequiredCheck(check)
	receipt.Executed, receipt.Reused, receipt.Passed = executed == 1, reused == 1, passed == 1
	if force != 0 && force != 1 {
		return CheckReceiptRecord{}, errors.New("stored workload evidence receipt force identity is invalid")
	}
	receipt.Force = force == 1
	receipt.StartedAt = unixMilliUTC(started)
	receipt.CompletedAt = unixMilliUTC(completed)
	receipt.Duration = time.Duration(duration) * time.Millisecond
	return receipt, nil
}

// validateStoredWorkloadReceiptBooleans 禁止 SQLite 整数布尔列被映射为静默零值。
func validateStoredWorkloadReceiptBooleans(executed, reused, passed int) error {
	if executed != 0 && executed != 1 {
		return errors.New("stored workload evidence receipt executed encoding is invalid")
	}
	if reused != 0 && reused != 1 {
		return errors.New("stored workload evidence receipt reused encoding is invalid")
	}
	if passed != 0 && passed != 1 {
		return errors.New("stored workload evidence receipt passed encoding is invalid")
	}
	return nil
}

// loadSQLiteReusableWorkloadEvidence 读取并解码 reused 结果引用的完整提升证据。
func loadSQLiteReusableWorkloadEvidence(tx *sql.Tx, result RemoteCIWorkloadResult) (WorkloadPassEvidence, error) {
	var evidence WorkloadPassEvidence
	var generation, executionJSON string
	err := tx.QueryRow(`SELECT identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256 FROM ci_workload_pass_evidence WHERE identity_digest = ? AND accepted_generation = ? AND origin_job_id = ?`, result.Identity.IdentityDigest, strconv.FormatUint(result.OriginAcceptedGeneration, 10), result.OriginJobID).Scan(
		&evidence.Identity.IdentityDigest, &generation, &evidence.Identity.WorkloadID, &evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest,
		&evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return loadSQLiteWorkloadPassReplaySource(tx, result)
	}
	if err != nil {
		return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("load reused workload evidence", err)
	}
	evidence.OriginAcceptedGeneration, err = strconv.ParseUint(generation, 10, 64)
	if err != nil || evidence.OriginAcceptedGeneration == 0 {
		return WorkloadPassEvidence{}, errors.New("stored reused workload evidence generation is invalid")
	}
	if err := decodeStoredWorkloadPassExecutionJSON(executionJSON, &evidence.OriginExecution); err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("decode reused workload origin execution: %w", err)
	}
	return evidence, nil
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

// validateStoredWorkloadPassEvidenceOrigin 校验 evidence 与来源 run 的 generation/tree 绑定。
func validateStoredWorkloadPassEvidenceOrigin(record RemoteCIRunRecord, evidence WorkloadPassEvidence) error {
	if evidence.OriginAcceptedGeneration != record.AcceptedGeneration {
		return errors.New("stored workload pass evidence generation does not match origin run")
	}
	if evidence.OriginSourceTreeSHA != record.SourceTreeSHA {
		return errors.New("stored workload pass evidence tree does not match origin run")
	}
	if record.ImageCacheSnapshotID == "" {
		return errors.New("stored workload pass evidence origin snapshot is missing")
	}
	return nil
}

// validateProvisionalWorkloadPassEvidenceWithContext 验证单项 provisional
// evidence，复用已加载的 catalog 与 workload execution 索引。
func validateProvisionalWorkloadPassEvidenceWithContext(
	record RemoteCIRunRecord,
	evidence WorkloadPassEvidence,
	canonical map[GateID]Workload,
	executions map[GateID]PlanGateExecution,
) error {
	if err := validateCanonicalWorkloadPassIdentity(evidence.Identity, canonical); err != nil {
		return fmt.Errorf("stored provisional workload identity: %w", err)
	}
	execution, ok := executions[evidence.Identity.WorkloadID]
	if !ok {
		return fmt.Errorf("stored provisional workload %q execution is missing", evidence.Identity.WorkloadID)
	}
	if err := validateProvisionalWorkloadPassCandidate(record, RemoteCIWorkloadResult{Identity: evidence.Identity, Disposition: WorkloadDispositionExecuted, OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: evidence.OriginAcceptedGeneration}, execution); err != nil {
		return err
	}
	encodedStored, err := json.Marshal(execution)
	if err != nil {
		return fmt.Errorf("encode stored provisional workload execution: %w", err)
	}
	encodedEvidence, err := json.Marshal(evidence.OriginExecution)
	if err != nil {
		return fmt.Errorf("encode provisional workload evidence execution: %w", err)
	}
	if !bytes.Equal(encodedStored, encodedEvidence) {
		return errors.New("stored provisional workload evidence execution was tampered")
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
	query := fmt.Sprintf(`INSERT INTO %s (run_id, job_id, candidate_tree_sha, agent_token_digest, force, accepted_generation, accepted_snapshot_id, required_check, executed, reused, reuse_proof_sha256, passed, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`, cicontract.CheckReceiptsTable)
	for _, receipt := range receipts {
		if _, err := tx.Exec(query, receipt.RunID, receipt.JobID, receipt.CandidateTreeSHA, receipt.AgentTokenDigest, boolToSQLite(receipt.Force), strconv.FormatUint(receipt.AcceptedGeneration, 10), receipt.AcceptedSnapshotID, string(receipt.RequiredCheck), boolToSQLite(receipt.Executed), boolToSQLite(receipt.Reused), receipt.ReuseProofSHA256, receipt.StartedAt.UTC().UnixMilli(), receipt.CompletedAt.UTC().UnixMilli(), receipt.Duration.Milliseconds(), receipt.ReceiptSHA256); err != nil {
			return mapDurationLedgerSQLiteError("append check receipt", err)
		}
	}
	return nil
}

type receiptReloadIdentity struct {
	force  bool
	digest string
}

// verifySQLiteCheckReceiptReload 逐项核对本次事务回读的回执摘要，拒绝缺失、额外或漂移记录。
func verifySQLiteCheckReceiptReload(tx *sql.Tx, jobID string, want []CheckReceiptRecord) error {
	rows, err := tx.Query(fmt.Sprintf(`SELECT required_check, force, receipt_sha256 FROM %s WHERE job_id = ?`, cicontract.CheckReceiptsTable), jobID)
	if err != nil {
		return mapDurationLedgerSQLiteError("reload check receipts", err)
	}
	defer rows.Close()
	wantByCheck := make(map[cicontract.RequiredCheck]receiptReloadIdentity, len(want))
	for _, receipt := range want {
		wantByCheck[receipt.RequiredCheck] = receiptReloadIdentity{force: receipt.Force, digest: receipt.ReceiptSHA256}
	}
	if err := scanSQLiteCheckReceiptReload(rows, wantByCheck); err != nil {
		return err
	}
	if len(wantByCheck) != 0 {
		return errors.New("reloaded check receipt collection is incomplete")
	}
	return nil
}

// scanSQLiteCheckReceiptReload 读取并核对事务内回执的 force/digest 身份集合。
func scanSQLiteCheckReceiptReload(rows *sql.Rows, wantByCheck map[cicontract.RequiredCheck]receiptReloadIdentity) error {
	for rows.Next() {
		var check cicontract.RequiredCheck
		var force int
		var digest string
		if err := rows.Scan(&check, &force, &digest); err != nil {
			return mapDurationLedgerSQLiteError("scan reloaded check receipt", err)
		}
		if force != 0 && force != 1 {
			return errors.New("reloaded check receipt force identity is invalid")
		}
		if expected, ok := wantByCheck[check]; !ok || expected.force != (force == 1) || expected.digest != digest {
			return errors.New("reloaded check receipt does not exactly match this invocation")
		}
		delete(wantByCheck, check)
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate reloaded check receipts", err)
	}
	return nil
}
