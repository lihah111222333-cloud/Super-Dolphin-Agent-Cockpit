package gate

import (
	"database/sql"
	"errors"
	"fmt"
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
	var schemaVersion uint32
	var storedGeneration, stateJSON, stateSHA256 string
	err := transaction.QueryRow(`SELECT schema_version, generation, state_json, state_sha256 FROM ci_remote_baseline_state WHERE singleton = 1`).Scan(&schemaVersion, &storedGeneration, &stateJSON, &stateSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRemoteBaselineStateNotFound
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load accepted baseline generation authority", err)
	}
	current, parseErr := strconv.ParseUint(storedGeneration, 10, 64)
	if parseErr != nil || current == 0 || storedGeneration != strconv.FormatUint(current, 10) || schemaVersion != 3 || strings.TrimSpace(stateJSON) == "" || strings.TrimSpace(stateSHA256) == "" {
		return errors.New("accepted baseline generation authority is invalid")
	}
	if generation > current {
		return fmt.Errorf("accepted baseline generation %d was never accepted; current generation is %d", generation, current)
	}
	return nil
}

// compactDurationLedgerAuthority 是所有历史写事务的最后一个数据库操作。
// 它只按 accepted baseline generation 淘汰；保留代内部没有行数上限。
func compactDurationLedgerAuthority(transaction *sql.Tx) error {
	bindings := cicontract.RetentionRootBindings()
	var union strings.Builder
	for index, binding := range bindings {
		if index != 0 {
			union.WriteString(" UNION ALL ")
		}
		fmt.Fprintf(&union, "SELECT %s FROM %s", binding.GenerationColumn, binding.Table)
	}
	retainedGenerations := `WITH retained_generations AS (
		SELECT accepted_generation FROM (SELECT DISTINCT accepted_generation FROM (` + union.String() + `)
		ORDER BY length(accepted_generation) DESC, accepted_generation DESC LIMIT ?)
	)`
	for _, binding := range bindings {
		if _, err := transaction.Exec(retainedGenerations+fmt.Sprintf(` DELETE FROM %s
			WHERE %s NOT IN (SELECT accepted_generation FROM retained_generations)`, binding.Table, binding.GenerationColumn), cicontract.RetentionGenerations); err != nil {
			return mapDurationLedgerSQLiteError("compact accepted baseline generations in "+binding.Table, err)
		}
	}
	// Catalogs are content addressed and may be shared by any number of retained
	// observations/runs. Delete only after all generation-bound owners are pruned.
	if _, err := transaction.Exec(`DELETE FROM ` + cicontract.WorkloadCatalogsTable + ` WHERE catalog_digest NOT IN (
		SELECT catalog_digest FROM ` + cicontract.RemoteRunsTable + `
		UNION SELECT catalog_digest FROM ` + cicontract.CatalogObservationsTable + `
	)`); err != nil {
		return mapDurationLedgerSQLiteError("prune unreferenced workload catalogs", err)
	}
	return nil
}
