package gate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// requireHistoricallyAcceptedGeneration 在历史根写事务内证明 generation
// 不晚于同一 SQLite authority 当前已接受代；refresh 只允许逐代晋级，因此该
// 上界既允许正在收尾的旧代运行，也拒绝伪造的未来代污染三代窗口。
func requireHistoricallyAcceptedGeneration(transaction *sql.Tx, generation uint64) error {
	if generation == 0 {
		return errors.New("accepted baseline generation is required")
	}
	current, err := currentAcceptedBaselineGeneration(transaction)
	if err != nil {
		return err
	}
	if generation > current {
		return fmt.Errorf("accepted baseline generation %d was never accepted; current generation is %d", generation, current)
	}
	return nil
}

// currentAcceptedBaselineGeneration 从内容摘要已复核的基线状态读取当前代。
func currentAcceptedBaselineGeneration(transaction *sql.Tx) (uint64, error) {
	var schemaVersion uint32
	var storedGeneration, stateJSON, stateSHA256 string
	err := transaction.QueryRow(`SELECT schema_version, generation, state_json, state_sha256 FROM ci_remote_baseline_state WHERE singleton = 1`).Scan(&schemaVersion, &storedGeneration, &stateJSON, &stateSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrRemoteBaselineStateNotFound
	}
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("load accepted baseline generation authority", err)
	}
	current, parseErr := strconv.ParseUint(storedGeneration, 10, 64)
	if parseErr != nil || current == 0 || storedGeneration != strconv.FormatUint(current, 10) || schemaVersion != 3 {
		return 0, errors.New("accepted baseline generation authority is invalid")
	}
	if _, err := validateAcceptedBaselineStateProjection(stateJSON, stateSHA256, current); err != nil {
		return 0, err
	}
	return current, nil
}

// validateAcceptedBaselineStateProjection 在查找逻辑信任代际或 ECI 快照前，
// 以存储的内容摘要复核持久化基线 JSON。完整 BaselineState 解码仍位于 remoteci 写入边界；
// 此读取侧投影刻意不接受更弱的身份校验。
func validateAcceptedBaselineStateProjection(stateJSON, stateSHA256 string, generation uint64) (string, error) {
	if strings.TrimSpace(stateJSON) == "" || strings.TrimSpace(stateSHA256) == "" {
		return "", errors.New("accepted baseline generation authority is invalid")
	}
	digest := sha256.Sum256([]byte(stateJSON))
	if stateSHA256 != fmt.Sprintf("sha256:%x", digest) {
		return "", errors.New("accepted baseline state JSON SHA-256 does not match authority")
	}
	var projection struct {
		SchemaVersion        uint32 `json:"schema_version"`
		Generation           uint64 `json:"generation"`
		ExecutionProvider    string `json:"execution_provider"`
		RegionID             string `json:"region_id"`
		ImageCacheSnapshotID string `json:"image_cache_snapshot_id"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &projection); err != nil {
		return "", fmt.Errorf("decode accepted baseline state projection: %w", err)
	}
	if err := cicontract.ValidateAcceptedBaselineProjection(projection.SchemaVersion, projection.ExecutionProvider, projection.RegionID); err != nil {
		return "", fmt.Errorf("accepted baseline state projection: %w", err)
	}
	if projection.Generation != generation || strings.TrimSpace(projection.ImageCacheSnapshotID) == "" {
		return "", errors.New("accepted baseline state projection does not bind generation and image cache snapshot")
	}
	return projection.ImageCacheSnapshotID, nil
}

const retentionStaleGenerationsTable = "retention_stale_generations"

type retentionGeneration struct {
	value uint64
	text  string
}

// validateRetentionRootBindings 约束七个历史根同属一个代际压缩事务，
// 并拒绝把仅供消费方使用的 v16 proof 辅助投影替代 ci_runs 根。
func validateRetentionRootBindings(bindings []cicontract.RetentionRootBinding) error {
	expected := map[string]struct{}{
		cicontract.DurationSamplesTable:              {},
		cicontract.DurationShardOverheadsTable:       {},
		cicontract.DurationShardOverheadSamplesTable: {},
		cicontract.CatalogObservationsTable:          {},
		cicontract.RemoteRunsTable:                   {},
		cicontract.WorkloadPassEvidenceTable:         {},
		cicontract.CalibrationCheckpointsTable:       {},
	}
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.GenerationColumn != cicontract.AcceptedGenerationColumn {
			return fmt.Errorf("retention root %q generation column = %q, want %q", binding.Table, binding.GenerationColumn, cicontract.AcceptedGenerationColumn)
		}
		switch binding.Table {
		case cicontract.RemoteRunsTable:
			// Remote runs own the consumer rows whose v16 proof is auxiliary only.
		case cicontract.DurationSamplesTable,
			cicontract.DurationShardOverheadsTable,
			cicontract.DurationShardOverheadSamplesTable,
			cicontract.CatalogObservationsTable,
			cicontract.WorkloadPassEvidenceTable,
			cicontract.CalibrationCheckpointsTable:
		default:
			return fmt.Errorf("unsupported retention root %q", binding.Table)
		}
		if _, duplicate := seen[binding.Table]; duplicate {
			return fmt.Errorf("retention root %q is duplicated", binding.Table)
		}
		seen[binding.Table] = struct{}{}
	}
	for table := range expected {
		if _, present := seen[table]; !present {
			return fmt.Errorf("required retention root %q is missing", table)
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("retention root count = %d, want %d", len(seen), len(expected))
	}
	return nil
}

// retentionGenerationQuery 构造唯一一次跨七个历史根的 generation 枚举。
func retentionGenerationQuery(bindings []cicontract.RetentionRootBinding) string {
	var union strings.Builder
	for index, binding := range bindings {
		if index != 0 {
			union.WriteString(" UNION ALL ")
		}
		fmt.Fprintf(&union, "SELECT %s FROM %s", binding.GenerationColumn, binding.Table)
	}
	return `SELECT DISTINCT accepted_generation FROM (` + union.String() + `)`
}

// retentionDeleteQuery 使用已物化的 stale 集合做正向 membership 删除。
func retentionDeleteQuery(binding cicontract.RetentionRootBinding) string {
	return fmt.Sprintf(
		`DELETE FROM %s WHERE %s IN (SELECT accepted_generation FROM temp.%s)`,
		binding.Table,
		binding.GenerationColumn,
		retentionStaleGenerationsTable,
	)
}

// loadRetentionGenerations 只从七个历史根读取一次并按 uint64 代数排序。
func loadRetentionGenerations(transaction *sql.Tx, bindings []cicontract.RetentionRootBinding) ([]retentionGeneration, error) {
	rows, err := transaction.Query(retentionGenerationQuery(bindings))
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("enumerate accepted baseline generations", err)
	}
	defer rows.Close()

	generations := make([]retentionGeneration, 0)
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan accepted baseline generation", err)
		}
		value, err := strconv.ParseUint(text, 10, 64)
		if err != nil || value == 0 || text != strconv.FormatUint(value, 10) {
			return nil, fmt.Errorf("accepted baseline generation %q is invalid", text)
		}
		generations = append(generations, retentionGeneration{value: value, text: text})
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate accepted baseline generations", err)
	}
	sort.Slice(generations, func(left, right int) bool {
		return generations[left].value > generations[right].value
	})
	return generations, nil
}

// materializeRetentionStaleGenerations 将过期代物化到当前事务的临时集合。
func materializeRetentionStaleGenerations(transaction *sql.Tx, generations []retentionGeneration) error {
	if _, err := transaction.Exec(`CREATE TEMP TABLE ` + retentionStaleGenerationsTable + ` (accepted_generation TEXT PRIMARY KEY)`); err != nil {
		return mapDurationLedgerSQLiteError("materialize stale accepted baseline generations", err)
	}
	if len(generations) <= cicontract.RetentionGenerations {
		return nil
	}
	for _, generation := range generations[cicontract.RetentionGenerations:] {
		if _, err := transaction.Exec(
			`INSERT INTO temp.`+retentionStaleGenerationsTable+` (accepted_generation) VALUES (?)`,
			generation.text,
		); err != nil {
			return mapDurationLedgerSQLiteError("materialize stale accepted baseline generation", err)
		}
	}
	return nil
}

// dropRetentionStaleGenerations 清理当前连接上的临时 stale 集合。
func dropRetentionStaleGenerations(transaction *sql.Tx) error {
	if _, err := transaction.Exec(`DROP TABLE temp.` + retentionStaleGenerationsTable); err != nil {
		return mapDurationLedgerSQLiteError("drop stale accepted baseline generations", err)
	}
	return nil
}

type retentionOriginCache struct {
	key    string
	origin workloadPassEvidenceOriginContext
	err    error
	stats  *workloadPassEvidenceLookupStats
}

// load 按已排序 origin 复用一次完整 run/receipt 解码，并保留退休 profile 判定。
func (cache *retentionOriginCache) load(transaction *sql.Tx, evidence WorkloadPassEvidence) (workloadPassEvidenceOriginContext, error) {
	key := evidence.OriginJobID + "\x00" + strconv.FormatUint(evidence.OriginAcceptedGeneration, 10)
	if cache.key != key {
		cache.key = key
		cache.origin, cache.err = loadWorkloadPassEvidenceBaseOriginContext(transaction, evidence, evidence.OriginAcceptedGeneration, cache.stats)
	}
	return cache.origin, cache.err
}

// validateRetentionReusedProof 复用 origin 上下文验证完整 proof，并拒绝 reuse 链或循环。
func validateRetentionReusedProof(transaction *sql.Tx, result RemoteCIWorkloadResult, origins *retentionOriginCache) error {
	_, err := validateRetentionReusedProofEvidence(transaction, result, origins)
	return err
}

// validateRetentionReusedProofEvidence 返回 retention 必须保留的直接来源 evidence。
func validateRetentionReusedProofEvidence(transaction *sql.Tx, result RemoteCIWorkloadResult, origins *retentionOriginCache) (WorkloadPassEvidence, error) {
	evidence, err := loadSQLiteReusableWorkloadEvidence(transaction, result)
	if err != nil {
		return WorkloadPassEvidence{}, err
	}
	if err := validateReusableWorkloadEvidenceBinding(evidence, result); err != nil {
		return WorkloadPassEvidence{}, err
	}
	if err := validateWorkloadPassEvidence(evidence); err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("reused workload result %q evidence proof: %w", result.Identity.WorkloadID, err)
	}
	origin, err := origins.load(transaction, evidence)
	if err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("load reused workload origin %q: %w", result.OriginJobID, err)
	}
	if err := validateStoredWorkloadPassEvidenceBase(transaction, origin, evidence); err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("reused workload result %q origin proof: %w", result.Identity.WorkloadID, err)
	}
	return evidence, nil
}

// deleteStaleRetentionGenerations 对七个历史根执行同一 stale set 的严格删除。
func deleteStaleRetentionGenerations(transaction *sql.Tx, bindings []cicontract.RetentionRootBinding) error {
	for _, binding := range bindings {
		if _, err := transaction.Exec(retentionDeleteQuery(binding)); err != nil {
			return mapDurationLedgerSQLiteError("compact accepted baseline generations in "+binding.Table, err)
		}
	}
	return nil
}

// pruneUnreferencedWorkloadCatalogs 在历史根删除后清理无引用的内容寻址 catalog。
func pruneUnreferencedWorkloadCatalogs(transaction *sql.Tx) error {
	if _, err := transaction.Exec(`DELETE FROM ` + cicontract.WorkloadCatalogsTable + ` AS catalogs
		WHERE NOT EXISTS (
			SELECT 1 FROM ` + cicontract.RemoteRunsTable + ` AS runs
			WHERE runs.catalog_digest = catalogs.catalog_digest
		)
		AND NOT EXISTS (
			SELECT 1 FROM ` + cicontract.CatalogObservationsTable + ` AS observations
			WHERE observations.catalog_digest = catalogs.catalog_digest
		)`); err != nil {
		return mapDurationLedgerSQLiteError("prune unreferenced workload catalogs", err)
	}
	return nil
}

// compactDurationLedgerAuthority 是所有历史写事务的最后一个数据库操作。
// 它只按 accepted baseline generation 淘汰；保留代内部没有行数上限。
func compactDurationLedgerAuthority(transaction *sql.Tx) (err error) {
	currentGeneration, err := currentAcceptedBaselineGeneration(transaction)
	if err != nil {
		return fmt.Errorf("load accepted baseline generation before retention: %w", err)
	}
	return compactDurationLedgerAuthorityAtAcceptedGeneration(transaction, currentGeneration)
}

// compactDurationLedgerAuthorityAtAcceptedGeneration 使用已验证的权威代执行唯一七根历史根压缩事务。
func compactDurationLedgerAuthorityAtAcceptedGeneration(transaction *sql.Tx, currentGeneration uint64) (err error) {
	bindings := cicontract.RetentionRootBindings()
	if err := validateRetentionRootBindings(bindings); err != nil {
		return err
	}
	generations, err := loadRetentionGenerations(transaction, bindings)
	if err != nil {
		return err
	}
	if err := validateRetentionGenerationsAccepted(generations, currentGeneration); err != nil {
		return err
	}
	if err := materializeRetentionStaleGenerations(transaction, generations); err != nil {
		return err
	}
	defer func() {
		cleanupErr := cleanupRetentionTempTables(transaction)
		if cleanupErr != nil && err == nil {
			err = cleanupErr
		}
	}()
	if err := validateRetainedWorkloadPassProofsBeforeCompaction(transaction, currentGeneration); err != nil {
		return err
	}
	if err := deleteStaleRetentionGenerations(transaction, bindings); err != nil {
		return err
	}
	if err := compactLiveRemoteCITimingWarnings(transaction); err != nil {
		return err
	}
	return pruneUnreferencedWorkloadCatalogs(transaction)
}

// validateRetainedWorkloadPassProofsBeforeCompaction 要求每个 live reused consumer
// 在删除旧 direct origin 前都有 consumer-owned v16 proof。
func validateRetainedWorkloadPassProofsBeforeCompaction(transaction *sql.Tx, currentGeneration uint64) error {
	retained := retainedWorkloadPassGenerations(currentGeneration)
	rows, err := transaction.Query(`SELECT result.workload_id, result.identity_digest, result.execution_digest, result.input_digest, result.environment_digest
		FROM ci_run_workload_results AS result
		JOIN ci_runs AS consumer ON consumer.job_id = result.job_id
		LEFT JOIN ci_retained_workload_pass_proofs AS proof
			ON proof.consumer_job_id = result.job_id AND proof.workload_id = result.workload_id
		LEFT JOIN ci_workload_pass_evidence AS evidence
			ON evidence.identity_digest = proof.identity_digest
			AND evidence.accepted_generation = proof.origin_accepted_generation
			AND evidence.origin_job_id = proof.origin_job_id
			AND evidence.evidence_sha256 = proof.evidence_sha256
		WHERE result.disposition = 'reused' AND consumer.accepted_generation IN (?, ?, ?)
			AND (proof.consumer_job_id IS NULL
				OR proof.origin_job_id != result.origin_job_id
				OR proof.origin_accepted_generation != result.origin_accepted_generation
				OR (proof.origin_accepted_generation IN (?, ?, ?) AND (
					evidence.identity_digest IS NULL
					OR proof.origin_source_tree_sha != evidence.origin_source_tree_sha
					OR proof.origin_receipt_set_sha256 != evidence.origin_receipt_set_sha256
					OR CASE WHEN json_type(proof.origin_execution_json, '$.schema_version') IS NOT NULL
						THEN json_extract(proof.origin_execution_json, '$.execution')
						ELSE proof.origin_execution_json END != evidence.origin_execution_json
					OR proof.evidence_sha256 != evidence.evidence_sha256)))
		ORDER BY result.job_id, result.workload_id`, retained[0], retained[1], retained[2], retained[0], retained[1], retained[2])
	if err != nil {
		return mapDurationLedgerSQLiteError("query missing retained workload pass proofs before compaction", err)
	}
	defer rows.Close()
	missing, err := countCurrentDomainMissingRetainedProofs(rows)
	if err != nil {
		return err
	}
	if missing != 0 {
		return fmt.Errorf("retained workload pass proof has no promoted evidence for %d live reused consumer results", missing)
	}
	return nil
}

// countCurrentDomainMissingRetainedProofs 将旧无域 identity 严格降为 MISS，
// 但当前域缺 proof 与未知摘要仍阻断 compaction。
func countCurrentDomainMissingRetainedProofs(rows *sql.Rows) (int, error) {
	missing := 0
	for rows.Next() {
		var identity WorkloadPassIdentity
		if err := rows.Scan(&identity.WorkloadID, &identity.IdentityDigest, &identity.ExecutionDigest, &identity.InputDigest, &identity.EnvironmentDigest); err != nil {
			return 0, mapDurationLedgerSQLiteError("scan missing retained workload pass proof", err)
		}
		err := validateWorkloadPassIdentity(identity)
		if errors.Is(err, errLegacyWorkloadPassIdentityDomain) {
			continue
		}
		if err != nil {
			return 0, err
		}
		missing++
	}
	if err := rows.Err(); err != nil {
		return 0, mapDurationLedgerSQLiteError("iterate missing retained workload pass proofs", err)
	}
	return missing, nil
}

// validateRetentionGenerationsAccepted 拒绝尚未进入 baseline authority 的未来根。
func validateRetentionGenerationsAccepted(generations []retentionGeneration, current uint64) error {
	for _, generation := range generations {
		if generation.value > current {
			return fmt.Errorf("retention generation %d was never accepted; current generation is %d", generation.value, current)
		}
	}
	return nil
}

// cleanupRetentionTempTables 在事务退出时清理连接级 retention 临时集合。
func cleanupRetentionTempTables(transaction *sql.Tx) error {
	return dropRetentionStaleGenerations(transaction)
}
