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

// retentionGenerationQuery 构造唯一一次跨五个历史根的 generation 枚举。
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

// loadRetentionGenerations 只从五个历史根读取一次并按 uint64 代数排序。
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

// deleteStaleRetentionGenerations 对五个历史根执行正向 IN 删除。
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
	bindings := cicontract.RetentionRootBindings()
	generations, err := loadRetentionGenerations(transaction, bindings)
	if err != nil {
		return err
	}
	if err := materializeRetentionStaleGenerations(transaction, generations); err != nil {
		return err
	}
	defer func() {
		if dropErr := dropRetentionStaleGenerations(transaction); dropErr != nil && err == nil {
			err = dropErr
		}
	}()
	if err := deleteStaleRetentionGenerations(transaction, bindings); err != nil {
		return err
	}
	if err := compactLiveRemoteCITimingWarnings(transaction); err != nil {
		return err
	}
	return pruneUnreferencedWorkloadCatalogs(transaction)
}
