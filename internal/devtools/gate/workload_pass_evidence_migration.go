package gate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// workloadPassEvidenceMigrationBatchSize 限制每次索引历史探测的规模；大目录拆分为只读批次。
const workloadPassEvidenceMigrationBatchSize = 1024

const workloadPassEvidenceAliasSourceSelect = `SELECT source_identity_digest, source_accepted_generation
	FROM ci_workload_pass_evidence_aliases
	WHERE alias_identity_digest = ? AND alias_accepted_generation = ?`

// WorkloadPassEvidenceMigration 将来源行绑定到严格重算的当前身份别名；来源仍是 origin 校验权威。
type WorkloadPassEvidenceMigration struct {
	Source    WorkloadPassEvidence
	Projected WorkloadPassEvidence
}

// RecordMigratedWorkloadPassEvidence 在一个 SQLite 事务内持久化已验证别名，并原样保留所有来源证据行。
func (store *DurationLedgerStore) RecordMigratedWorkloadPassEvidence(migrations []WorkloadPassEvidenceMigration) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if len(migrations) == 0 {
		return nil
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "migrated workload pass evidence", func(tx *sql.Tx) error {
		currentGeneration, err := currentAcceptedBaselineGeneration(tx)
		if err != nil {
			return err
		}
		if err := requireHistoricallyAcceptedGeneration(tx, currentGeneration); err != nil {
			return err
		}
		seen := make(map[string]struct{}, len(migrations))
		for index, migration := range migrations {
			key := migration.Projected.Identity.IdentityDigest + "\x00" + strconv.FormatUint(migration.Projected.OriginAcceptedGeneration, 10)
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate migrated workload pass evidence %q", migration.Projected.Identity.WorkloadID)
			}
			seen[key] = struct{}{}
			if err := recordMigratedWorkloadPassEvidence(tx, currentGeneration, migration); err != nil {
				return fmt.Errorf("persist migrated workload pass evidence %d: %w", index, err)
			}
		}
		return compactDurationLedgerAuthority(tx)
	})
}

// recordMigratedWorkloadPassEvidence 在已锁定事务内验证 source 并写入 projected evidence 与 alias relation。
func recordMigratedWorkloadPassEvidence(tx *sql.Tx, currentGeneration uint64, migration WorkloadPassEvidenceMigration) error {
	source, projected := migration.Source, migration.Projected
	if err := validateMigratedWorkloadPassEvidencePair(source, projected); err != nil {
		return err
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(currentGeneration, source.OriginAcceptedGeneration); err != nil {
		return fmt.Errorf("source evidence generation: %w", err)
	}
	storedSource, err := loadWorkloadPassEvidenceMigrationRow(tx, source.Identity.IdentityDigest, source.OriginAcceptedGeneration)
	if err != nil {
		return fmt.Errorf("load source evidence: %w", err)
	}
	if !reflect.DeepEqual(storedSource, source) {
		return errors.New("source evidence does not match persisted row")
	}
	origin, err := loadWorkloadPassEvidenceOriginContext(tx, source, nil)
	if err != nil {
		return fmt.Errorf("load source evidence origin: %w", err)
	}
	if err := validateStoredWorkloadPassEvidenceWithOriginContext(origin, source); err != nil {
		return fmt.Errorf("validate source evidence origin: %w", err)
	}
	return persistMigratedWorkloadPassEvidenceProjection(tx, source, projected)
}

// persistMigratedWorkloadPassEvidenceProjection 幂等写入 projected 行和 source 关系，并读回严格比对。
func persistMigratedWorkloadPassEvidenceProjection(tx *sql.Tx, source, projected WorkloadPassEvidence) error {
	encoded, err := json.Marshal(projected.OriginExecution)
	if err != nil {
		return fmt.Errorf("encode migrated origin execution: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, projected.Identity.IdentityDigest, strconv.FormatUint(projected.OriginAcceptedGeneration, 10), string(projected.Identity.WorkloadID), projected.Identity.ExecutionDigest, projected.Identity.InputDigest, projected.Identity.EnvironmentDigest, projected.OriginJobID, projected.OriginSourceTreeSHA, projected.OriginReceiptSetSHA256, string(encoded), projected.EvidenceSHA256); err != nil {
		return mapDurationLedgerSQLiteError("insert migrated workload pass evidence", err)
	}
	storedProjected, err := loadWorkloadPassEvidenceMigrationRow(tx, projected.Identity.IdentityDigest, projected.OriginAcceptedGeneration)
	if err != nil {
		return fmt.Errorf("reload migrated evidence: %w", err)
	}
	if !reflect.DeepEqual(storedProjected, projected) {
		return errors.New("migrated evidence alias conflicts with persisted row")
	}
	return persistMigratedWorkloadPassEvidenceAlias(tx, source, projected)
}

// persistMigratedWorkloadPassEvidenceAlias 幂等写入 alias relation，并确认 source identity/gen 未漂移。
func persistMigratedWorkloadPassEvidenceAlias(tx *sql.Tx, source, projected WorkloadPassEvidence) error {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO ci_workload_pass_evidence_aliases (alias_identity_digest, alias_accepted_generation, source_identity_digest, source_accepted_generation) VALUES (?, ?, ?, ?)`, projected.Identity.IdentityDigest, strconv.FormatUint(projected.OriginAcceptedGeneration, 10), source.Identity.IdentityDigest, strconv.FormatUint(source.OriginAcceptedGeneration, 10)); err != nil {
		return mapDurationLedgerSQLiteError("insert migrated workload pass evidence alias", err)
	}
	storedSourceIdentity, storedSourceGeneration, err := loadWorkloadPassEvidenceAliasRelation(tx, projected.Identity.IdentityDigest, projected.OriginAcceptedGeneration)
	if err != nil {
		return mapDurationLedgerSQLiteError("reload migrated workload pass evidence alias", err)
	}
	if storedSourceIdentity != source.Identity.IdentityDigest || storedSourceGeneration != strconv.FormatUint(source.OriginAcceptedGeneration, 10) {
		return errors.New("migrated evidence alias conflicts with persisted relation")
	}
	return nil
}

// loadWorkloadPassEvidenceAliasRelation 是 alias source SELECT 的唯一 SQL owner；调用方
// 保留各自的 ErrNoRows/错误上下文语义。
func loadWorkloadPassEvidenceAliasRelation(tx *sql.Tx, aliasIdentityDigest string, aliasGeneration uint64) (string, string, error) {
	var sourceIdentityDigest, sourceGeneration string
	err := tx.QueryRow(
		workloadPassEvidenceAliasSourceSelect,
		aliasIdentityDigest,
		strconv.FormatUint(aliasGeneration, 10),
	).Scan(&sourceIdentityDigest, &sourceGeneration)
	return sourceIdentityDigest, sourceGeneration, err
}

// validateMigratedWorkloadPassEvidencePair 证明投影仅改变重算的 identity/input 与 evidence 摘要；执行、环境和 origin 绑定逐字节保持一致。
func validateMigratedWorkloadPassEvidencePair(source, projected WorkloadPassEvidence) error {
	if err := source.Validate(); err != nil {
		return fmt.Errorf("source evidence is invalid: %w", err)
	}
	if err := projected.Validate(); err != nil {
		return fmt.Errorf("projected evidence is invalid: %w", err)
	}
	if err := validateMigratedWorkloadPassIdentityPair(source.Identity, projected.Identity); err != nil {
		return err
	}
	return validateMigratedWorkloadPassOriginPair(source, projected)
}

// validateMigratedWorkloadPassIdentityPair 限定 workload、执行和环境不变，并拒绝 self alias。
func validateMigratedWorkloadPassIdentityPair(source, projected WorkloadPassIdentity) error {
	if source.WorkloadID != projected.WorkloadID || source.ExecutionDigest != projected.ExecutionDigest || source.EnvironmentDigest != projected.EnvironmentDigest {
		return errors.New("migrated evidence changed workload execution or environment identity")
	}
	if source.IdentityDigest == projected.IdentityDigest {
		return errors.New("migrated evidence projection must change identity digest")
	}
	return nil
}

// validateMigratedWorkloadPassOriginPair 限定 origin 绑定和原始 execution 全部保持一致。
func validateMigratedWorkloadPassOriginPair(source, projected WorkloadPassEvidence) error {
	if source.OriginJobID != projected.OriginJobID || source.OriginAcceptedGeneration != projected.OriginAcceptedGeneration || source.OriginSourceTreeSHA != projected.OriginSourceTreeSHA || source.OriginReceiptSetSHA256 != projected.OriginReceiptSetSHA256 || !reflect.DeepEqual(source.OriginExecution, projected.OriginExecution) {
		return errors.New("migrated evidence changed origin binding")
	}
	return nil
}

// loadWorkloadPassEvidenceMigrationRow 从 evidence 主表按 identity/gen 精确读取并严格解码来源行。
func loadWorkloadPassEvidenceMigrationRow(tx *sql.Tx, identityDigest string, generation uint64) (WorkloadPassEvidence, error) {
	var (
		evidence                                    WorkloadPassEvidence
		storedGeneration, workloadID, executionJSON string
	)
	err := tx.QueryRow(`SELECT identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256 FROM ci_workload_pass_evidence WHERE identity_digest = ? AND accepted_generation = ?`, identityDigest, strconv.FormatUint(generation, 10)).Scan(&evidence.Identity.IdentityDigest, &storedGeneration, &workloadID, &evidence.Identity.ExecutionDigest, &evidence.Identity.InputDigest, &evidence.Identity.EnvironmentDigest, &evidence.OriginJobID, &evidence.OriginSourceTreeSHA, &evidence.OriginReceiptSetSHA256, &executionJSON, &evidence.EvidenceSHA256)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkloadPassEvidence{}, errors.New("workload pass evidence row is missing")
		}
		return WorkloadPassEvidence{}, mapDurationLedgerSQLiteError("load workload pass evidence migration row", err)
	}
	parsedGeneration, err := strconv.ParseUint(storedGeneration, 10, 64)
	if err != nil || parsedGeneration != generation {
		return WorkloadPassEvidence{}, errors.New("stored workload pass evidence generation is invalid")
	}
	evidence.OriginAcceptedGeneration = parsedGeneration
	evidence.Identity.WorkloadID = GateID(workloadID)
	if err := decodeStrictJSON(strings.NewReader(executionJSON), &evidence.OriginExecution); err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("decode workload pass evidence origin execution: %w", err)
	}
	return evidence, nil
}

// LookupWorkloadPassEvidenceMigrationCandidates 按 workload/execution/environment
// 返回三代保留窗口内完整、可验证的历史 PASS；输入摘要由调用方 exact-tree 重算。
func (store *DurationLedgerStore) LookupWorkloadPassEvidenceMigrationCandidates(
	identities []WorkloadPassIdentity,
) ([]WorkloadPassEvidence, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	requested, err := validateWorkloadPassEvidenceMigrationRequest(identities)
	if err != nil {
		return nil, err
	}
	if requested == nil {
		return []WorkloadPassEvidence{}, nil
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin workload pass evidence migration lookup", err)
	}
	defer tx.Rollback()
	candidates, err := loadWorkloadPassEvidenceMigrationCandidates(tx, identities, requested)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit workload pass evidence migration lookup", err)
	}
	return candidates, nil
}

// validateWorkloadPassEvidenceMigrationRequest 校验输入身份并建立不依赖输入摘要
// 的匹配键；空请求返回 nil，重复键直接阻断迁移。
func validateWorkloadPassEvidenceMigrationRequest(identities []WorkloadPassIdentity) (map[string]WorkloadPassIdentity, error) {
	if len(identities) == 0 {
		return nil, nil
	}
	if err := validateWorkloadPassIdentities(identities); err != nil {
		return nil, err
	}
	requested := make(map[string]WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		key := workloadPassEvidenceMigrationIdentityKey(identity)
		if _, duplicate := requested[key]; duplicate {
			return nil, fmt.Errorf("duplicate workload pass evidence migration identity for workload %q", identity.WorkloadID)
		}
		requested[key] = identity
	}
	return requested, nil
}

// loadWorkloadPassEvidenceMigrationCandidates 将大请求切成有界 workload 批次，
// 每批独立走 retained-generation/workload tuple 索引，再稳定合并去重结果。
func loadWorkloadPassEvidenceMigrationCandidates(
	tx *sql.Tx,
	identities []WorkloadPassIdentity,
	requested map[string]WorkloadPassIdentity,
) ([]WorkloadPassEvidence, error) {
	currentGeneration, err := currentAcceptedBaselineGeneration(tx)
	if err != nil {
		return nil, err
	}
	retained := retainedWorkloadPassGenerations(currentGeneration)
	candidates := make([]WorkloadPassEvidence, 0, len(requested))
	for start := 0; start < len(identities); start += workloadPassEvidenceMigrationBatchSize {
		end := min(start+workloadPassEvidenceMigrationBatchSize, len(identities))
		batchRequested := make(map[string]WorkloadPassIdentity, end-start)
		for _, identity := range identities[start:end] {
			batchRequested[workloadPassEvidenceMigrationIdentityKey(identity)] = requested[workloadPassEvidenceMigrationIdentityKey(identity)]
		}
		batchCandidates, err := loadWorkloadPassEvidenceMigrationBatch(tx, retained, currentGeneration, batchRequested)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, batchCandidates...)
	}
	return mergeWorkloadPassEvidenceMigrationCandidates(candidates), nil
}

// loadWorkloadPassEvidenceMigrationBatch 在当前事务内按请求 tuple 查询所有 retained
// generation 候选；查询由 workload-leading 复合索引界定，不选择“每代最新 run”。
func loadWorkloadPassEvidenceMigrationBatch(
	tx *sql.Tx,
	retained [3]string,
	currentGeneration uint64,
	requested map[string]WorkloadPassIdentity,
) ([]WorkloadPassEvidence, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	query, args := workloadPassEvidenceMigrationLookupQuery(requested, retained)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query workload pass evidence migration candidates", err)
	}
	defer rows.Close()
	return scanWorkloadPassEvidenceMigrationRows(rows, tx, currentGeneration, requested)
}

// workloadPassEvidenceMigrationLookupQuery 构造有界 VALUES 查询；每个 requested
// workload/execution/environment tuple 都被三代窗口约束并走复合索引，不扫描 evidence 表。
func workloadPassEvidenceMigrationLookupQuery(
	requested map[string]WorkloadPassIdentity,
	retained [3]string,
) (string, []any) {
	identities := make([]WorkloadPassIdentity, 0, len(requested))
	for _, identity := range requested {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(left, right int) bool {
		leftKey := workloadPassEvidenceMigrationIdentityKey(identities[left])
		rightKey := workloadPassEvidenceMigrationIdentityKey(identities[right])
		return leftKey < rightKey
	})
	values := make([]string, len(identities))
	args := make([]any, 0, len(identities)*3+len(retained))
	for index, identity := range identities {
		values[index] = "(?, ?, ?)"
		args = append(args, string(identity.WorkloadID), identity.ExecutionDigest, identity.EnvironmentDigest)
	}
	query := `WITH requested(workload_id, execution_digest, environment_digest) AS (VALUES ` + strings.Join(values, ", ") + `)
		SELECT evidence.identity_digest, evidence.accepted_generation, evidence.workload_id, evidence.execution_digest, evidence.input_digest, evidence.environment_digest, evidence.origin_job_id, evidence.origin_source_tree_sha, evidence.origin_receipt_set_sha256, evidence.origin_execution_json, evidence.evidence_sha256
		FROM requested
		INNER JOIN ci_workload_pass_evidence AS evidence
			ON evidence.workload_id = requested.workload_id
			AND evidence.execution_digest = requested.execution_digest
			AND evidence.environment_digest = requested.environment_digest
		WHERE evidence.accepted_generation IN (?, ?, ?)
		`
	for _, generation := range retained {
		args = append(args, generation)
	}
	return query, args
}

// scanWorkloadPassEvidenceMigrationRows 逐行验证索引命中的候选；坏行或坏 origin
// 只形成该候选的 migration miss，不遮蔽同批其他可验证证据。
func scanWorkloadPassEvidenceMigrationRows(
	rows *sql.Rows,
	tx *sql.Tx,
	currentGeneration uint64,
	requested map[string]WorkloadPassIdentity,
) ([]WorkloadPassEvidence, error) {
	origins := make(map[string]workloadPassEvidenceOriginContext)
	invalidOrigins := make(map[string]struct{})
	candidates := make([]WorkloadPassEvidence, 0)
	for rows.Next() {
		candidate, ok, err := decodeWorkloadPassEvidenceMigrationRow(rows, "", currentGeneration, requested, workloadPassEvidenceOriginContext{})
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		originKey, err := workloadPassEvidenceOriginCacheKey(tx, candidate)
		if err != nil {
			continue
		}
		origin, exists := origins[originKey]
		if !exists {
			if _, invalid := invalidOrigins[originKey]; invalid {
				continue
			}
			origin, err = loadWorkloadPassEvidenceOriginContext(tx, candidate, nil)
			if err != nil {
				invalidOrigins[originKey] = struct{}{}
				continue
			}
			origins[originKey] = origin
		}
		if err := validateStoredWorkloadPassEvidenceWithOriginContext(origin, candidate); err != nil {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate workload pass evidence migration candidates", err)
	}
	return candidates, nil
}

// mergeWorkloadPassEvidenceMigrationCandidates 以稳定 workload/generation 顺序
// 去掉同一持久化证据的重复投影，但保留跨代候选供调用方判定歧义。
func mergeWorkloadPassEvidenceMigrationCandidates(candidates []WorkloadPassEvidence) []WorkloadPassEvidence {
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].Identity.WorkloadID != candidates[right].Identity.WorkloadID {
			return candidates[left].Identity.WorkloadID < candidates[right].Identity.WorkloadID
		}
		if candidates[left].OriginAcceptedGeneration != candidates[right].OriginAcceptedGeneration {
			return candidates[left].OriginAcceptedGeneration > candidates[right].OriginAcceptedGeneration
		}
		if candidates[left].OriginJobID != candidates[right].OriginJobID {
			return candidates[left].OriginJobID < candidates[right].OriginJobID
		}
		return candidates[left].Identity.IdentityDigest < candidates[right].Identity.IdentityDigest
	})
	seen := make(map[string]struct{}, len(candidates))
	merged := candidates[:0]
	for _, candidate := range candidates {
		key := candidate.Identity.IdentityDigest + "\x00" + strconv.FormatUint(candidate.OriginAcceptedGeneration, 10) + "\x00" + candidate.OriginJobID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, candidate)
	}
	return merged
}

func workloadPassEvidenceMigrationIdentityKey(identity WorkloadPassIdentity) string {
	return string(identity.WorkloadID) + "\x00" + identity.ExecutionDigest + "\x00" + identity.EnvironmentDigest
}

// decodeWorkloadPassEvidenceMigrationRow 严格解码一条候选并复用 origin context 验证。
func decodeWorkloadPassEvidenceMigrationRow(
	rows *sql.Rows,
	jobID string,
	currentGeneration uint64,
	requested map[string]WorkloadPassIdentity,
	origin workloadPassEvidenceOriginContext,
) (WorkloadPassEvidence, bool, error) {
	var (
		evidence                              WorkloadPassEvidence
		generation, workloadID, executionJSON string
	)
	if err := rows.Scan(
		&evidence.Identity.IdentityDigest,
		&generation,
		&workloadID,
		&evidence.Identity.ExecutionDigest,
		&evidence.Identity.InputDigest,
		&evidence.Identity.EnvironmentDigest,
		&evidence.OriginJobID,
		&evidence.OriginSourceTreeSHA,
		&evidence.OriginReceiptSetSHA256,
		&executionJSON,
		&evidence.EvidenceSHA256,
	); err != nil {
		return WorkloadPassEvidence{}, false, mapDurationLedgerSQLiteError("scan workload pass evidence migration candidate", err)
	}
	evidence.Identity.WorkloadID = GateID(workloadID)
	parsedGeneration, valid := parseWorkloadPassEvidenceMigrationGeneration(generation, currentGeneration)
	if !valid {
		return WorkloadPassEvidence{}, false, nil
	}
	evidence.OriginAcceptedGeneration = parsedGeneration
	if !workloadPassEvidenceMigrationRowMatches(evidence, parsedGeneration, jobID, origin) {
		return WorkloadPassEvidence{}, false, nil
	}
	if err := decodeStrictJSON(strings.NewReader(executionJSON), &evidence.OriginExecution); err != nil {
		return WorkloadPassEvidence{}, false, nil
	}
	if _, ok := requested[workloadPassEvidenceMigrationIdentityKey(evidence.Identity)]; !ok {
		return WorkloadPassEvidence{}, false, nil
	}
	if !validateMigrationEvidenceOriginIfPresent(origin, evidence) {
		return WorkloadPassEvidence{}, false, nil
	}
	return evidence, true, nil
}

func parseWorkloadPassEvidenceMigrationGeneration(raw string, current uint64) (uint64, bool) {
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed == 0 {
		return 0, false
	}
	if err := cicontract.ValidateWorkloadPassEvidenceGeneration(current, parsed); err != nil {
		return 0, false
	}
	return parsed, true
}

func workloadPassEvidenceMigrationRowMatches(evidence WorkloadPassEvidence, generation uint64, jobID string, origin workloadPassEvidenceOriginContext) bool {
	if jobID != "" && evidence.OriginJobID != jobID {
		return false
	}
	if origin.record.JobID != "" && generation != origin.record.AcceptedGeneration {
		return false
	}
	return true
}

func validateMigrationEvidenceOriginIfPresent(origin workloadPassEvidenceOriginContext, evidence WorkloadPassEvidence) bool {
	if origin.record.JobID == "" {
		return true
	}
	return validateStoredWorkloadPassEvidenceWithOriginContext(origin, evidence) == nil
}
