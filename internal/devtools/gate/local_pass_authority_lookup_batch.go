package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// localWorkloadPassLookupStats records the SQLite lookup shape for scale tests.
type localWorkloadPassLookupStats struct{ authorityTransactions, identityBatchQueries, originBatchQueries, originLoads int }

func (stats *localWorkloadPassLookupStats) recordAuthorityTransaction() {
	if stats != nil {
		stats.authorityTransactions++
	}
}
func (stats *localWorkloadPassLookupStats) recordIdentityBatchQuery() {
	if stats != nil {
		stats.identityBatchQueries++
	}
}
func (stats *localWorkloadPassLookupStats) recordOriginLoad() {
	if stats != nil {
		stats.originLoads++
	}
}
func (stats *localWorkloadPassLookupStats) recordOriginBatchQuery() {
	if stats != nil {
		stats.originBatchQueries++
	}
}

func loadLocalWorkloadPassOriginsBatch(tx *sql.Tx, originIDs []string, stats *localWorkloadPassLookupStats) (map[string]localWorkloadPassOriginCache, error) {
	loaded := make(map[string]localWorkloadPassOriginCache, len(originIDs))
	for start := 0; start < len(originIDs); start += workloadPassEvidenceLookupBatchSize {
		end := min(start+workloadPassEvidenceLookupBatchSize, len(originIDs))
		if err := loadLocalWorkloadPassOriginsBatchChunk(tx, originIDs[start:end], loaded, stats); err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

func loadLocalWorkloadPassOriginsBatchChunk(tx *sql.Tx, originIDs []string, loaded map[string]localWorkloadPassOriginCache, stats *localWorkloadPassLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(originIDs)), ",")
	stats.recordOriginBatchQuery()
	rows, err := tx.Query(`SELECT run_id, local_generation, source_tree_sha, catalog_digest, host_context_digest, toolchain_closure_digest, runner_semantic_policy, runner_semantic_digest, cpu_window_start_unix_ms, cpu_window_end_unix_ms, cpu_sample_count, cpu_busy_average_percent, available_cpu, available_memory_gib, status, cleanup_complete, started_at_unix_ms, completed_at_unix_ms, projection_digest FROM ci_local_workload_origins WHERE authority_kind = 'local-canonical' AND run_id IN (`+placeholders+`)`, stringsToAny(originIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load local workload PASS origins", err)
	}
	defer rows.Close()
	for rows.Next() {
		origin, err := scanLocalWorkloadPassOrigin(rows)
		if err != nil {
			return err
		}
		loaded[origin.RunID] = localWorkloadPassOriginCache{origin: origin}
		stats.recordOriginLoad()
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate batch local workload PASS origins", err)
	}
	stats.recordOriginBatchQuery()
	executions, err := tx.Query(`SELECT execution.run_id, execution.workload_id, execution.identity_digest, execution.execution_digest, execution.input_digest, execution.environment_digest, execution.status, execution.exit_code, execution.started_at_unix_ms, execution.completed_at_unix_ms, execution.environment_json, execution.execution_json FROM ci_local_workload_executions AS execution JOIN ci_local_workload_origins AS origin ON origin.run_id = execution.run_id AND origin.local_generation = execution.local_generation WHERE origin.authority_kind = 'local-canonical' AND execution.run_id IN (`+placeholders+") ORDER BY execution.run_id, execution.workload_id", stringsToAny(originIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load local workload PASS executions", err)
	}
	defer executions.Close()
	for executions.Next() {
		var runID string
		entry, err := scanLocalWorkloadPassExecutionWithRunID(executions, &runID)
		if err != nil {
			return err
		}
		cached, exists := loaded[runID]
		if !exists {
			return errors.New("batch local workload PASS execution references unknown origin")
		}
		cached.entries = append(cached.entries, entry)
		loaded[runID] = cached
	}
	if err := executions.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate batch local workload PASS executions", err)
	}
	return validateLocalWorkloadPassOriginBatch(loaded)
}

func validateLocalWorkloadPassOriginBatch(loaded map[string]localWorkloadPassOriginCache) error {
	for runID, cached := range loaded {
		for _, entry := range cached.entries {
			if err := validateLocalWorkloadPassEntry(cached.origin, entry); err != nil {
				return err
			}
		}
		loaded[runID] = cached
	}
	return nil
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

func scanLocalWorkloadPassOrigin(rows interface{ Scan(...any) error }) (LocalWorkloadPassOrigin, error) {
	var origin LocalWorkloadPassOrigin
	var generation string
	var cleanup int
	var started, completed, cpuStart, cpuEnd, samples int64
	if err := rows.Scan(&origin.RunID, &generation, &origin.SourceTreeSHA, &origin.CatalogDigest, &origin.HostContextDigest, &origin.ToolchainClosureDigest, &origin.RunnerSemanticPolicy, &origin.RunnerSemanticDigest, &cpuStart, &cpuEnd, &samples, &origin.CPUBusyAveragePercent, &origin.AvailableCPU, &origin.AvailableMemoryGiB, &origin.Status, &cleanup, &started, &completed, &origin.ProjectionDigest); err != nil {
		return LocalWorkloadPassOrigin{}, mapDurationLedgerSQLiteError("scan local workload PASS origin", err)
	}
	var err error
	origin.LocalGeneration, err = parseLocalAcceptedGeneration(generation)
	if err != nil {
		return LocalWorkloadPassOrigin{}, err
	}
	origin.CleanupComplete = cleanup == 1
	origin.CPUWindowStart = time.UnixMilli(cpuStart).UTC()
	origin.CPUWindowEnd = time.UnixMilli(cpuEnd).UTC()
	origin.CPUSampleCount = int(samples)
	origin.StartedAt = time.UnixMilli(started).UTC()
	origin.CompletedAt = time.UnixMilli(completed).UTC()
	if err := validateLocalWorkloadPassOrigin(origin); err != nil {
		return LocalWorkloadPassOrigin{}, err
	}
	return origin, nil
}

func scanLocalWorkloadPassExecutionWithRunID(rows interface{ Scan(...any) error }, runID *string) (LocalWorkloadPassEntry, error) {
	var entry LocalWorkloadPassEntry
	var workloadID, encoded, environmentJSON, status string
	var exitCode, started, completed int64
	args := []any{&workloadID, &entry.Identity.IdentityDigest, &entry.Identity.ExecutionDigest, &entry.Identity.InputDigest, &entry.Identity.EnvironmentDigest, &status, &exitCode, &started, &completed, &environmentJSON, &encoded}
	if runID != nil {
		args = append([]any{runID}, args...)
	}
	if err := rows.Scan(args...); err != nil {
		return LocalWorkloadPassEntry{}, mapDurationLedgerSQLiteError("scan local workload PASS execution", err)
	}
	entry.Identity.WorkloadID = GateID(workloadID)
	if err := decodeLocalWorkloadPassEnvironmentJSON(environmentJSON, &entry.Environment); err != nil {
		return LocalWorkloadPassEntry{}, fmt.Errorf("decode local workload PASS environment: %w", err)
	}
	if err := decodeStoredWorkloadPassExecutionJSON(encoded, &entry.Execution); err != nil {
		return LocalWorkloadPassEntry{}, fmt.Errorf("decode local workload PASS origin execution: %w", err)
	}
	if err := validateLocalExecutionColumns(entry.Execution, status, exitCode, started, completed); err != nil {
		return LocalWorkloadPassEntry{}, err
	}
	return entry, nil
}
