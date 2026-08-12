package gate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// insertRetainedWorkloadPassProofWithEvidence 使用事务开头批量读取的 canonical evidence
// 构造 proof，避免写回每个 reused workload 时重复查询同一证据。
func insertRetainedWorkloadPassProofWithEvidence(tx *sql.Tx, consumerJobID string, consumerAcceptedGeneration uint64, result RemoteCIWorkloadResult, evidence WorkloadPassEvidence) error {
	if err := validateReusableWorkloadEvidenceBinding(evidence, result); err != nil {
		return err
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("validate retained workload pass proof: %w", err)
	}
	executionJSON, err := encodeRetainedWorkloadPassOriginJSON(evidence)
	if err != nil {
		return fmt.Errorf("encode retained workload pass proof execution: %w", err)
	}
	expected := retainedWorkloadPassProof{
		ConsumerJobID: consumerJobID, ConsumerAcceptedGeneration: strconv.FormatUint(consumerAcceptedGeneration, 10),
		WorkloadID: string(result.Identity.WorkloadID), IdentityDigest: evidence.Identity.IdentityDigest,
		OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: strconv.FormatUint(evidence.OriginAcceptedGeneration, 10),
		OriginSourceTreeSHA: evidence.OriginSourceTreeSHA, OriginReceiptSetSHA256: evidence.OriginReceiptSetSHA256,
		OriginExecutionJSON: string(executionJSON), EvidenceSHA256: evidence.EvidenceSHA256,
	}
	if err := expected.canonicalizeExecutionJSON(evidence.Identity); err != nil {
		return fmt.Errorf("canonicalize retained workload pass proof execution: %w", err)
	}
	return insertOrCompareRetainedWorkloadPassProof(tx, expected, result)
}

const retainedWorkloadPassOriginSchemaVersion = 1

type retainedWorkloadPassOrigin struct {
	SchemaVersion  int                  `json:"schema_version"`
	SourceIdentity WorkloadPassIdentity `json:"source_identity"`
	Execution      PlanGateExecution    `json:"execution"`
}

// Validate 校验 retained proof JSON 的版本和完整来源身份。
func (origin retainedWorkloadPassOrigin) Validate() error {
	if origin.SchemaVersion != retainedWorkloadPassOriginSchemaVersion {
		return errors.New("retained workload pass origin schema version is invalid")
	}
	return validateWorkloadPassIdentity(origin.SourceIdentity)
}

// encodeRetainedWorkloadPassOriginJSON 把来源身份和执行封装进现有 proof JSON 列，
// 让 source/environment replay 在 direct origin 被压缩后仍可独立验证。
func encodeRetainedWorkloadPassOriginJSON(evidence WorkloadPassEvidence) ([]byte, error) {
	return json.Marshal(retainedWorkloadPassOrigin{SchemaVersion: retainedWorkloadPassOriginSchemaVersion, SourceIdentity: evidence.Identity, Execution: evidence.OriginExecution})
}

// decodeRetainedWorkloadPassOriginJSON 严格解码新 envelope；旧行只允许使用
// consumer identity 还原，因此历史 source replay 会 fail-fast 而不会误授权。
func decodeRetainedWorkloadPassOriginJSON(data string, legacyIdentity WorkloadPassIdentity) (WorkloadPassIdentity, PlanGateExecution, error) {
	var discriminator struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal([]byte(data), &discriminator); err != nil {
		return WorkloadPassIdentity{}, PlanGateExecution{}, err
	}
	if discriminator.SchemaVersion == nil {
		var execution PlanGateExecution
		if err := decodeStoredWorkloadPassExecutionJSON(data, &execution); err != nil {
			return WorkloadPassIdentity{}, PlanGateExecution{}, err
		}
		return legacyIdentity, execution, nil
	}
	var origin retainedWorkloadPassOrigin
	if err := DecodeStrictJSON([]byte(data), &origin); err != nil {
		return WorkloadPassIdentity{}, PlanGateExecution{}, err
	}
	return origin.SourceIdentity, origin.Execution, nil
}

// retainedWorkloadPassProof is the complete comparison row. Tags are the sole
// INSERT projection source; consumer generation is read from ci_runs.
type retainedWorkloadPassProof struct {
	ConsumerJobID              string `proof:"consumer_job_id"`
	ConsumerAcceptedGeneration string `proof:"-"`
	WorkloadID                 string `proof:"workload_id"`
	IdentityDigest             string `proof:"identity_digest"`
	OriginJobID                string `proof:"origin_job_id"`
	OriginAcceptedGeneration   string `proof:"origin_accepted_generation"`
	OriginSourceTreeSHA        string `proof:"origin_source_tree_sha"`
	OriginReceiptSetSHA256     string `proof:"origin_receipt_set_sha256"`
	OriginExecutionJSON        string `proof:"origin_execution_json"`
	EvidenceSHA256             string `proof:"evidence_sha256"`
}

type reusableWorkloadEvidenceDirectKey struct {
	identityDigest string
	generation     uint64
	originJobID    string
}

type reusableWorkloadEvidenceSourceKey struct {
	generation  uint64
	originJobID string
	workloadID  GateID
}

type reusableWorkloadEvidenceBatch struct {
	direct  map[reusableWorkloadEvidenceDirectKey]WorkloadPassEvidence
	sources map[reusableWorkloadEvidenceSourceKey][]WorkloadPassEvidence
}

// loadSQLiteReusableWorkloadEvidenceBatch 按 distinct origin run 分块预载 evidence，
// 让 provisional 写回事务的查询数不随 reused workload 数量增长。
func loadSQLiteReusableWorkloadEvidenceBatch(tx *sql.Tx, results []RemoteCIWorkloadResult) (reusableWorkloadEvidenceBatch, error) {
	loaded := reusableWorkloadEvidenceBatch{
		direct:  make(map[reusableWorkloadEvidenceDirectKey]WorkloadPassEvidence),
		sources: make(map[reusableWorkloadEvidenceSourceKey][]WorkloadPassEvidence),
	}
	originIDs := reusableWorkloadEvidenceOriginIDs(results)
	for start := 0; start < len(originIDs); start += workloadPassEvidenceLookupBatchSize {
		end := min(start+workloadPassEvidenceLookupBatchSize, len(originIDs))
		if err := loaded.loadChunk(tx, originIDs[start:end]); err != nil {
			return reusableWorkloadEvidenceBatch{}, err
		}
	}
	return loaded, nil
}

// reusableWorkloadEvidenceOriginIDs 保持 result 顺序并去重来源 run。
func reusableWorkloadEvidenceOriginIDs(results []RemoteCIWorkloadResult) []string {
	seen := make(map[string]struct{})
	originIDs := make([]string, 0)
	for _, result := range results {
		if result.Disposition != WorkloadDispositionReused {
			continue
		}
		if _, ok := seen[result.OriginJobID]; ok {
			continue
		}
		seen[result.OriginJobID] = struct{}{}
		originIDs = append(originIDs, result.OriginJobID)
	}
	return originIDs
}

// loadChunk 用一条 IN 查询加载一组来源 run 的全部 promoted evidence。
func (loaded reusableWorkloadEvidenceBatch) loadChunk(tx *sql.Tx, originIDs []string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(originIDs)), ",")
	rows, err := tx.Query(`SELECT identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256
		FROM ci_workload_pass_evidence WHERE origin_job_id IN (`+placeholders+`)
		ORDER BY origin_job_id, accepted_generation, workload_id, identity_digest`, stringsToAny(originIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("query reused workload evidence batch", err)
	}
	defer rows.Close()
	for rows.Next() {
		evidence, err := scanWorkloadPassSourceReplayCandidate(rows)
		if err != nil {
			return err
		}
		loaded.add(evidence)
	}
	return mapDurationLedgerSQLiteError("iterate reused workload evidence batch", rows.Err())
}

// add 同时建立 direct identity 与 source replay 两种严格解析索引。
func (loaded reusableWorkloadEvidenceBatch) add(evidence WorkloadPassEvidence) {
	direct := reusableWorkloadEvidenceDirectKey{identityDigest: evidence.Identity.IdentityDigest, generation: evidence.OriginAcceptedGeneration, originJobID: evidence.OriginJobID}
	source := reusableWorkloadEvidenceSourceKey{generation: evidence.OriginAcceptedGeneration, originJobID: evidence.OriginJobID, workloadID: evidence.Identity.WorkloadID}
	loaded.direct[direct] = evidence
	loaded.sources[source] = append(loaded.sources[source], evidence)
}

// resolve 保留单项 reader 的 direct-first 与 source 唯一性语义。
func (loaded reusableWorkloadEvidenceBatch) resolve(result RemoteCIWorkloadResult) (WorkloadPassEvidence, error) {
	direct := reusableWorkloadEvidenceDirectKey{identityDigest: result.Identity.IdentityDigest, generation: result.OriginAcceptedGeneration, originJobID: result.OriginJobID}
	if evidence, ok := loaded.direct[direct]; ok {
		return evidence, nil
	}
	source := reusableWorkloadEvidenceSourceKey{generation: result.OriginAcceptedGeneration, originJobID: result.OriginJobID, workloadID: result.Identity.WorkloadID}
	candidates := loaded.sources[source]
	if len(candidates) == 0 {
		return WorkloadPassEvidence{}, fmt.Errorf("reused workload result %q has no promoted evidence", result.Identity.WorkloadID)
	}
	if len(candidates) != 1 {
		return WorkloadPassEvidence{}, fmt.Errorf("reused workload result %q has ambiguous promoted source evidence", result.Identity.WorkloadID)
	}
	return candidates[0], nil
}

// insertOrCompareRetainedWorkloadPassProof 只允许完全相同的冲突行幂等通过，
// 任一字段漂移都使整个写事务失败。
func insertOrCompareRetainedWorkloadPassProof(tx *sql.Tx, expected retainedWorkloadPassProof, result RemoteCIWorkloadResult) error {
	columns, err := retainedWorkloadPassProofColumns()
	if err != nil {
		return err
	}
	arguments, err := expected.insertArguments()
	if err != nil {
		return err
	}
	query := fmt.Sprintf("INSERT INTO ci_retained_workload_pass_proofs (%s) VALUES (%s)", strings.Join(columns, ", "), strings.TrimRight(strings.Repeat("?, ", len(columns)), ", "))
	_, insertErr := tx.Exec(query, arguments...)
	if insertErr == nil {
		return nil
	}
	if !isSQLiteConstraintError(insertErr) {
		return mapDurationLedgerSQLiteError("insert retained workload pass proof", insertErr)
	}
	actual, err := loadRetainedWorkloadPassProofForCollision(tx, expected.ConsumerJobID, GateID(expected.WorkloadID))
	if errors.Is(err, sql.ErrNoRows) {
		return mapDurationLedgerSQLiteError("insert retained workload pass proof", insertErr)
	}
	if err != nil {
		return fmt.Errorf("reload conflicting retained workload pass proof: %w", err)
	}
	if err := actual.validate(result); err != nil {
		return fmt.Errorf("validate conflicting retained workload pass proof: %w", err)
	}
	if !actual.matches(expected) {
		return errors.New("conflicting retained workload pass proof for consumer and workload")
	}
	return nil
}

func (proof retainedWorkloadPassProof) matches(expected retainedWorkloadPassProof) bool {
	return proof == expected
}

// retainedWorkloadPassProofColumns 从唯一结构标签生成写入列并拒绝缺失或重复标签。
func retainedWorkloadPassProofColumns() ([]string, error) {
	typeOfProof := reflect.TypeFor[retainedWorkloadPassProof]()
	columns := make([]string, 0)
	seenColumns := make(map[string]struct{})
	for field := range typeOfProof.Fields() {
		column, ok := field.Tag.Lookup("proof")
		if !ok || column == "" {
			return nil, fmt.Errorf("retained workload pass proof field %q has no projection tag", field.Name)
		}
		if column == "-" {
			continue
		}
		if _, duplicate := seenColumns[column]; duplicate {
			return nil, fmt.Errorf("retained workload pass proof field %q reuses projection tag %q", field.Name, column)
		}
		seenColumns[column] = struct{}{}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, errors.New("retained workload pass proof insert projection is empty")
	}
	return columns, nil
}

func (proof retainedWorkloadPassProof) insertArguments() ([]any, error) {
	value := reflect.ValueOf(proof)
	arguments := make([]any, 0, value.NumField()-1)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.Tag.Get("proof") == "-" {
			continue
		}
		if field.Tag.Get("proof") == "" || field.Type.Kind() != reflect.String {
			return nil, fmt.Errorf("retained workload pass proof field %q is not a tagged string projection", field.Name)
		}
		arguments = append(arguments, value.Field(index).String())
	}
	return arguments, nil
}

func loadRetainedWorkloadPassProofForCollision(tx *sql.Tx, consumerJobID string, workloadID GateID) (retainedWorkloadPassProof, error) {
	columns, err := retainedWorkloadPassProofColumns()
	if err != nil {
		return retainedWorkloadPassProof{}, err
	}
	projection := make([]string, 0, len(columns)+1)
	projection = append(projection, "consumer.accepted_generation")
	for _, column := range columns {
		projection = append(projection, "proof."+column)
	}
	var proof retainedWorkloadPassProof
	destinations, err := proof.scanDestinations()
	if err != nil {
		return retainedWorkloadPassProof{}, err
	}
	row := tx.QueryRow(fmt.Sprintf("SELECT %s FROM ci_retained_workload_pass_proofs AS proof JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id WHERE proof.consumer_job_id = ? AND proof.workload_id = ?", strings.Join(projection, ", ")), consumerJobID, string(workloadID))
	if err := row.Scan(append([]any{&proof.ConsumerAcceptedGeneration}, destinations...)...); err != nil {
		return retainedWorkloadPassProof{}, err
	}
	return proof, nil
}

func (proof *retainedWorkloadPassProof) scanDestinations() ([]any, error) {
	value := reflect.ValueOf(proof).Elem()
	destinations := make([]any, 0, value.NumField()-1)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.Tag.Get("proof") == "-" {
			continue
		}
		if field.Tag.Get("proof") == "" || field.Type.Kind() != reflect.String {
			return nil, fmt.Errorf("retained workload pass proof field %q is not a scannable tagged string projection", field.Name)
		}
		destinations = append(destinations, value.Field(index).Addr().Interface())
	}
	return destinations, nil
}

func (proof *retainedWorkloadPassProof) canonicalizeExecutionJSON(legacyIdentity WorkloadPassIdentity) error {
	identity, execution, err := decodeRetainedWorkloadPassOriginJSON(proof.OriginExecutionJSON, legacyIdentity)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(retainedWorkloadPassOrigin{SchemaVersion: retainedWorkloadPassOriginSchemaVersion, SourceIdentity: identity, Execution: execution})
	if err != nil {
		return err
	}
	proof.OriginExecutionJSON = string(encoded)
	return nil
}

// validate 严格验证持久化 proof 的来源身份，并绑定当前 consumer replay 结果。
func (proof retainedWorkloadPassProof) validate(result RemoteCIWorkloadResult) error {
	if err := proof.canonicalizeExecutionJSON(result.Identity); err != nil {
		return err
	}
	generation, err := strconv.ParseUint(proof.OriginAcceptedGeneration, 10, 64)
	if err != nil || generation == 0 {
		return errors.New("retained workload pass proof origin generation is invalid")
	}
	identity, execution, err := decodeRetainedWorkloadPassOriginJSON(proof.OriginExecutionJSON, result.Identity)
	if err != nil {
		return err
	}
	if proof.IdentityDigest != identity.IdentityDigest {
		return errors.New("retained workload pass proof identity does not match origin payload")
	}
	evidence := WorkloadPassEvidence{Identity: identity, OriginJobID: proof.OriginJobID, OriginAcceptedGeneration: generation, OriginSourceTreeSHA: proof.OriginSourceTreeSHA, OriginReceiptSetSHA256: proof.OriginReceiptSetSHA256, OriginExecution: execution, EvidenceSHA256: proof.EvidenceSHA256}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return err
	}
	if evidence.OriginJobID != result.OriginJobID || evidence.OriginAcceptedGeneration != result.OriginAcceptedGeneration {
		return errors.New("retained workload pass proof does not match reused result origin")
	}
	return validateReusableWorkloadEvidenceBinding(evidence, result)
}

// replaceSQLiteRemoteCIWorkloadResults 原子替换整次 run 的 workload 投影；
// evidence 缺失、歧义或 retained-proof 冲突必须回滚整个批次。
func replaceSQLiteRemoteCIWorkloadResults(tx *sql.Tx, record RemoteCIRunRecord) error {
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return fmt.Errorf("load reused workload evidence accepted generation: %w", err)
	}
	evidence, err := loadSQLiteReusableWorkloadEvidenceBatch(tx, record.WorkloadResults)
	if err != nil {
		return err
	}
	origins := make(map[string]workloadPassEvidenceOriginContext)
	if _, err := tx.Exec(`DELETE FROM ci_run_workload_results WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI workload results", err)
	}
	for _, result := range record.WorkloadResults {
		if err := storeSQLiteRemoteCIWorkloadResult(tx, record, result, currentGeneration, origins, evidence); err != nil {
			return err
		}
	}
	return nil
}

// storeSQLiteRemoteCIWorkloadResult 校验证据来源后写入单项结果和 retained proof。
func storeSQLiteRemoteCIWorkloadResult(tx *sql.Tx, record RemoteCIRunRecord, result RemoteCIWorkloadResult, currentGeneration uint64, origins map[string]workloadPassEvidenceOriginContext, evidence reusableWorkloadEvidenceBatch) error {
	if result.Disposition == WorkloadDispositionExecuted && (result.OriginJobID != record.JobID || result.OriginAcceptedGeneration != record.AcceptedGeneration) {
		return fmt.Errorf("executed workload result %q must originate from this run", result.Identity.WorkloadID)
	}
	if result.Disposition == WorkloadDispositionReused {
		loaded, err := evidence.resolve(result)
		if err != nil {
			return err
		}
		if err := verifySQLiteReusableWorkloadEvidenceWithOriginCache(tx, result, loaded, currentGeneration, origins); err != nil {
			return err
		}
		if err := insertRetainedWorkloadPassProofWithEvidence(tx, record.JobID, record.AcceptedGeneration, result, loaded); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO ci_run_workload_results (job_id, workload_id, identity_digest, execution_digest, input_digest, environment_digest, disposition, origin_job_id, origin_accepted_generation, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, record.JobID, string(result.Identity.WorkloadID), result.Identity.IdentityDigest, result.Identity.ExecutionDigest, result.Identity.InputDigest, result.Identity.EnvironmentDigest, result.Disposition, result.OriginJobID, strconv.FormatUint(result.OriginAcceptedGeneration, 10), result.EvidenceSHA256); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI workload result", err)
	}
	return nil
}

func verifySQLiteReusableWorkloadEvidence(tx *sql.Tx, result RemoteCIWorkloadResult) error {
	evidence, err := loadSQLiteReusableWorkloadEvidence(tx, result)
	if err != nil {
		return err
	}
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return fmt.Errorf("load reused workload evidence accepted generation: %w", err)
	}
	return verifySQLiteReusableWorkloadEvidenceWithOriginCache(tx, result, evidence, currentGeneration, make(map[string]workloadPassEvidenceOriginContext))
}

// verifySQLiteReusableWorkloadEvidenceWithOriginCache 让同一来源 run 的完整投影
// 和 receipt set 在写事务内只回读一次，逐 workload 仍严格校验自己的 evidence。
func verifySQLiteReusableWorkloadEvidenceWithOriginCache(tx *sql.Tx, result RemoteCIWorkloadResult, evidence WorkloadPassEvidence, currentGeneration uint64, origins map[string]workloadPassEvidenceOriginContext) error {
	if err := validateReusableWorkloadEvidenceBinding(evidence, result); err != nil {
		return err
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return fmt.Errorf("reused workload result %q origin %q evidence proof: %w", result.Identity.WorkloadID, evidence.OriginJobID, err)
	}
	origin, ok := origins[evidence.OriginJobID]
	if !ok {
		loaded, err := loadWorkloadPassEvidenceBaseOriginContext(tx, evidence, currentGeneration, nil)
		if err != nil {
			return fmt.Errorf("load reused workload evidence origin %q: %w", result.OriginJobID, err)
		}
		origin = loaded
		origins[evidence.OriginJobID] = origin
	}
	if err := validateStoredWorkloadPassEvidenceBase(tx, origin, evidence); err != nil {
		return fmt.Errorf("reused workload result %q origin proof: %w", result.Identity.WorkloadID, err)
	}
	return nil
}
