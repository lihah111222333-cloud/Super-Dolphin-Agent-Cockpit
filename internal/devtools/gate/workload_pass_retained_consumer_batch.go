package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// retainedConsumerBatchSize keeps every retained-consumer projection query
// below SQLite's bind-variable limit while preserving a fixed query shape.
const retainedConsumerBatchSize = 400

// loadRetainedConsumerRunRows 批量读取 ci_runs 与 agent identity 主投影。
func loadRetainedConsumerRunRows(tx *sql.Tx, jobIDs []string, stats *workloadPassEvidenceLookupStats) (map[string]RemoteCIRunRecord, error) {
	if tx == nil {
		return nil, errors.New("retained consumer batch transaction is nil")
	}
	if len(jobIDs) == 0 {
		return map[string]RemoteCIRunRecord{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT runs.job_id, identities.agent_token_digest,
		runs.force, runs.entrypoint, runs.profile, runs.plan_digest, runs.catalog_digest, runs.accepted_generation, runs.image_cache_snapshot_id,
		runs.source_tree_sha, runs.candidate_gate_source_sha256, runs.candidate_gate_toolchain_sha256,
		runs.runner_image, runs.status, runs.authoritative, runs.started_at_unix_ms, runs.completed_at_unix_ms, runs.cleanup_complete, runs.error_text
		FROM ci_runs AS runs INNER JOIN ci_run_agent_identities AS identities ON identities.job_id = runs.job_id
		WHERE runs.job_id IN (`+placeholders+`) ORDER BY runs.job_id`, stringsToAny(jobIDs)...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("batch load retained consumer runs", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.retainedConsumerBatchQueries++
	}
	records := make(map[string]RemoteCIRunRecord, len(jobIDs))
	for rows.Next() {
		record, err := scanRetainedConsumerRunRow(rows)
		if err != nil {
			return nil, err
		}
		if _, duplicate := records[record.JobID]; duplicate {
			return nil, fmt.Errorf("retained consumer run %q is duplicated", record.JobID)
		}
		records[record.JobID] = record
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate retained consumer runs", err)
	}
	if len(records) != len(jobIDs) {
		return nil, errors.New("retained consumer run projection is incomplete")
	}
	return records, nil
}

// scanRetainedConsumerRunRow 复用主记录严格解码，proof 字段不是 authority。
func scanRetainedConsumerRunRow(rows *sql.Rows) (RemoteCIRunRecord, error) {
	var record RemoteCIRunRecord
	var entrypoint, profile, status, generation string
	var force, authoritative, cleanup int
	var startedMS, completedMS int64
	if err := rows.Scan(&record.JobID, &record.AgentTokenDigest, &force, &entrypoint, &profile, &record.PlanDigest, &record.CatalogDigest, &generation, &record.ImageCacheSnapshotID, &record.SourceTreeSHA, &record.CandidateGateSourceSHA256, &record.CandidateGateToolchainSHA256, &record.RunnerImage, &status, &authoritative, &startedMS, &completedMS, &cleanup, &record.ErrorText); err != nil {
		return RemoteCIRunRecord{}, mapDurationLedgerSQLiteError("scan retained consumer run", err)
	}
	value, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || value == 0 {
		return RemoteCIRunRecord{}, errors.New("stored retained consumer accepted generation is invalid")
	}
	if force != 0 && force != 1 || authoritative != 0 && authoritative != 1 || cleanup != 0 && cleanup != 1 {
		return RemoteCIRunRecord{}, errors.New("stored retained consumer boolean identity is invalid")
	}
	record.Entrypoint, record.Profile, record.Status = CIEntrypointID(entrypoint), Profile(profile), ResultStatus(status)
	record.AcceptedGeneration, record.Force, record.Authoritative, record.CleanupComplete = value, force == 1, authoritative == 1, cleanup == 1
	record.StartedAt, record.CompletedAt = time.UnixMilli(startedMS).UTC(), time.UnixMilli(completedMS).UTC()
	return record, nil
}

// loadRetainedConsumerWorkloadResults 批量读取 consumer 自有结果投影。
func loadRetainedConsumerWorkloadResults(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	if len(jobIDs) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT job_id, workload_id, identity_digest, execution_digest, input_digest, environment_digest, disposition, origin_job_id, origin_accepted_generation, evidence_sha256
		FROM ci_run_workload_results WHERE job_id IN (`+placeholders+") ORDER BY job_id, workload_id", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer workload results", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.retainedConsumerBatchQueries++
	}
	for rows.Next() {
		var jobID, workloadID, generation string
		var result RemoteCIWorkloadResult
		if err := rows.Scan(&jobID, &workloadID, &result.Identity.IdentityDigest, &result.Identity.ExecutionDigest, &result.Identity.InputDigest, &result.Identity.EnvironmentDigest, &result.Disposition, &result.OriginJobID, &generation, &result.EvidenceSHA256); err != nil {
			return mapDurationLedgerSQLiteError("scan retained consumer workload result", err)
		}
		record, ok := records[jobID]
		if !ok {
			return errors.New("retained consumer workload result references an unknown run")
		}
		value, err := strconv.ParseUint(generation, 10, 64)
		if err != nil || value == 0 {
			return errors.New("stored retained consumer workload result generation is invalid")
		}
		result.Identity.WorkloadID, result.OriginAcceptedGeneration = GateID(workloadID), value
		record.WorkloadResults = append(record.WorkloadResults, result)
		records[jobID] = record
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer workload results", err)
	}
	return nil
}

// loadRetainedConsumerExecutionScopes 批量恢复不可变 subset scope 投影。
func loadRetainedConsumerExecutionScopes(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT job_id, accepted_generation, scope_json, scope_digest, scope_count FROM ci_remote_run_execution_scopes WHERE job_id IN (`+placeholders+") ORDER BY job_id", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer execution scopes", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.retainedConsumerBatchQueries++
	}
	for rows.Next() {
		var jobID, generation, encoded, digest string
		var count int
		if err := rows.Scan(&jobID, &generation, &encoded, &digest, &count); err != nil {
			return mapDurationLedgerSQLiteError("scan retained consumer execution scope", err)
		}
		record, ok := records[jobID]
		if !ok || generation != strconv.FormatUint(record.AcceptedGeneration, 10) {
			return errors.New("retained consumer execution scope binding is invalid")
		}
		scope, err := decodeRemoteCIExecutionScope(encoded, digest)
		if err != nil || count != len(scope.selectedGateIDs) {
			return errors.New("retained consumer execution scope is invalid")
		}
		record.Scope = &scope
		records[jobID] = record
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer execution scopes", err)
	}
	return nil
}

// loadRetainedConsumerExecutions 批量恢复 aggregate 或 workload execution 投影。
func loadRetainedConsumerExecutions(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, table string, workload bool, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	shardColumn := "''"
	if workload {
		shardColumn = "shard_identity"
	}
	rows, err := tx.Query(`SELECT job_id, `+shardColumn+`, workload_id, status, exit_code, started_at_unix_ms, completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json FROM `+table+` WHERE job_id IN (`+placeholders+") ORDER BY job_id, workload_id", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer executions", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.retainedConsumerBatchQueries++
	}
	for rows.Next() {
		if err := scanAndAppendRetainedConsumerExecution(rows, records, workload); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer executions", err)
	}
	return nil
}

// scanAndAppendRetainedConsumerExecution 严格解码单条 execution 并写回所属 run。
func scanAndAppendRetainedConsumerExecution(rows *sql.Rows, records map[string]RemoteCIRunRecord, workload bool) error {
	jobID, execution, profile, err := scanRetainedConsumerExecution(rows)
	if err != nil {
		return err
	}
	if err := decodeRetainedConsumerExecutionProfile(&execution, profile, workload); err != nil {
		return err
	}
	record, ok := records[jobID]
	if !ok {
		return errors.New("retained consumer execution references an unknown run")
	}
	if workload {
		record.WorkloadExecutions = append(record.WorkloadExecutions, execution)
	} else {
		record.Executions = append(record.Executions, execution)
	}
	records[jobID] = record
	return nil
}

// scanRetainedConsumerExecution 扫描两类 execution 共用的关系字段。
func scanRetainedConsumerExecution(rows *sql.Rows) (string, PlanGateExecution, string, error) {
	var jobID, workloadID, status, timings, profile string
	var startedMS, completedMS int64
	var execution PlanGateExecution
	if err := rows.Scan(&jobID, &execution.ShardIdentity, &workloadID, &status, &execution.ExitCode, &startedMS, &completedMS, &execution.ArgvDigest, &execution.LogDigest, &timings, &profile); err != nil {
		return "", PlanGateExecution{}, "", mapDurationLedgerSQLiteError("scan retained consumer execution", err)
	}
	testTimings, err := decodeStoredRemoteCIExecutionTestTimings(timings)
	if err != nil {
		return "", PlanGateExecution{}, "", err
	}
	execution.GateID, execution.Status, execution.TestTimings = GateID(workloadID), ResultStatus(status), testTimings
	execution.StartedAt, execution.CompletedAt = time.UnixMilli(startedMS).UTC(), time.UnixMilli(completedMS).UTC()
	return jobID, execution, profile, nil
}

// decodeRetainedConsumerExecutionProfile 按 execution 类型使用对应严格 decoder。
func decodeRetainedConsumerExecutionProfile(execution *PlanGateExecution, encoded string, workload bool) error {
	var err error
	if workload {
		execution.ExecutionProfile, err = decodeStoredRemoteCIExecutionProfile(encoded)
	} else {
		execution.ExecutionProfile, err = decodeStoredRemoteCIAggregateExecutionProfile(encoded)
	}
	return err
}

// loadRetainedConsumerRecords 在 proof 选择前批量组装和验证 consumer run。
func loadRetainedConsumerRecords(tx *sql.Tx, jobIDs []string, stats *workloadPassEvidenceLookupStats) (map[string]RemoteCIRunRecord, error) {
	records := make(map[string]RemoteCIRunRecord, len(jobIDs))
	for start := 0; start < len(jobIDs); start += retainedConsumerBatchSize {
		end := min(start+retainedConsumerBatchSize, len(jobIDs))
		chunk, err := loadRetainedConsumerChunk(tx, jobIDs[start:end], stats)
		if err != nil {
			return nil, err
		}
		catalogs, err := loadRetainedConsumerCatalogs(tx, chunk, stats)
		if err != nil {
			return nil, err
		}
		receipts, err := loadRetainedConsumerCheckReceipts(tx, jobIDs[start:end], stats)
		if err != nil {
			return nil, err
		}
		if err := validateAndCopyRetainedConsumerChunk(records, chunk, catalogs, receipts); err != nil {
			return nil, err
		}
	}
	return records, nil
}

// validateAndCopyRetainedConsumerChunk 对完整投影逐 run 执行 canonical 校验。
func validateAndCopyRetainedConsumerChunk(destination, chunk map[string]RemoteCIRunRecord, catalogs map[string]retainedConsumerCatalog, receipts map[string][]CheckReceiptRecord) error {
	for jobID, record := range chunk {
		if err := validateRemoteCIRunRecord(record); err != nil {
			return fmt.Errorf("validate retained consumer %q: %w", jobID, err)
		}
		if err := validateRetainedConsumerCatalogCoverage(record, catalogs); err != nil {
			return fmt.Errorf("validate retained consumer %q catalog coverage: %w", jobID, err)
		}
		if err := validateWorkloadCatalogPassingCheckReceipts(catalogs[record.CatalogDigest].catalog, receipts[jobID]); err != nil {
			return fmt.Errorf("validate retained consumer %q receipts: %w", jobID, err)
		}
		if err := validateCheckReceiptsAgainstRemoteRun(record, receipts[jobID]); err != nil {
			return fmt.Errorf("validate retained consumer %q receipt binding: %w", jobID, err)
		}
		destination[jobID] = record
	}
	return nil
}

// loadRetainedConsumerChunk 按固定投影顺序读取一个 consumer chunk。
func loadRetainedConsumerChunk(tx *sql.Tx, jobIDs []string, stats *workloadPassEvidenceLookupStats) (map[string]RemoteCIRunRecord, error) {
	chunk, err := loadRetainedConsumerRunRows(tx, jobIDs, stats)
	if err != nil {
		return nil, err
	}
	loaders := []func(*sql.Tx, []string, map[string]RemoteCIRunRecord, *workloadPassEvidenceLookupStats) error{
		loadRetainedConsumerWorkloadResults, loadRetainedConsumerExecutionScopes,
	}
	for _, load := range loaders {
		if err := load(tx, jobIDs, chunk, stats); err != nil {
			return nil, err
		}
	}
	if err := loadRetainedConsumerTimingDetails(tx, jobIDs, chunk, stats); err != nil {
		return nil, err
	}
	if err := loadRetainedConsumerShards(tx, jobIDs, chunk, stats); err != nil {
		return nil, err
	}
	if err := loadRetainedConsumerTerminalEvidence(tx, jobIDs, chunk, stats); err != nil {
		return nil, err
	}
	if err := loadRetainedConsumerExecutions(tx, jobIDs, chunk, "ci_gate_executions", false, stats); err != nil {
		return nil, err
	}
	if err := loadRetainedConsumerExecutions(tx, jobIDs, chunk, "ci_workload_executions", true, stats); err != nil {
		return nil, err
	}
	return chunk, nil
}

// loadRetainedConsumerTimingDetails 批量读取 warning 与两个 timing 投影。
func loadRetainedConsumerTimingDetails(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	loaders := []func(*sql.Tx, []string, map[string]RemoteCIRunRecord, *workloadPassEvidenceLookupStats) error{
		loadRetainedConsumerWarnings, loadRetainedConsumerTimingWarnings,
		loadRetainedConsumerTimingObservations, loadRetainedConsumerCompileTimingObservations,
	}
	for _, load := range loaders {
		if err := load(tx, jobIDs, records, stats); err != nil {
			return err
		}
	}
	return nil
}

// loadRetainedConsumerShards 批量恢复 shard 与 workload 归属投影。
func loadRetainedConsumerShards(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT shards.job_id, shards.shard_identity, shards.container_group_id, shards.container_status, shards.materialization_timing_json, shards.resources_json, workloads.workload_id FROM ci_shards AS shards LEFT JOIN ci_shard_workloads AS workloads ON workloads.job_id = shards.job_id AND workloads.shard_identity = shards.shard_identity WHERE shards.job_id IN (`+placeholders+") ORDER BY shards.job_id, shards.shard_identity, workloads.workload_id", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer shards", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	indexes := make(map[string]map[string]int, len(records))
	for rows.Next() {
		if err := scanAndAppendRetainedConsumerShard(rows, records, indexes); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer shards", err)
	}
	return nil
}

// scanAndAppendRetainedConsumerShard 解码 shard JSON 后与所属 run 合并。
func scanAndAppendRetainedConsumerShard(rows *sql.Rows, records map[string]RemoteCIRunRecord, indexes map[string]map[string]int) error {
	var jobID, timingJSON, resourcesJSON string
	var workloadID sql.NullString
	var shard RemoteCIShardRecord
	if err := rows.Scan(&jobID, &shard.ShardIdentity, &shard.ContainerGroup, &shard.ContainerStatus, &timingJSON, &resourcesJSON, &workloadID); err != nil {
		return mapDurationLedgerSQLiteError("scan retained consumer shard", err)
	}
	record, ok := records[jobID]
	if !ok {
		return errors.New("retained consumer shard references an unknown run")
	}
	if err := decodeStoredRemoteCIShardEvidence(&shard, timingJSON, resourcesJSON, record.Status, record.Authoritative); err != nil {
		return err
	}
	index := retainedConsumerShardIndex(indexes, jobID, shard.ShardIdentity, &record, shard)
	if workloadID.Valid {
		if strings.TrimSpace(workloadID.String) == "" {
			return errors.New("retained consumer shard workload is invalid")
		}
		record.Shards[index].Workloads = append(record.Shards[index].Workloads, GateID(workloadID.String))
	}
	records[jobID] = record
	return nil
}

// retainedConsumerShardIndex 维护 job 内 shard 的单一组装位置。
func retainedConsumerShardIndex(indexes map[string]map[string]int, jobID, shardID string, record *RemoteCIRunRecord, shard RemoteCIShardRecord) int {
	byShard := indexes[jobID]
	if byShard == nil {
		byShard = make(map[string]int)
		indexes[jobID] = byShard
	}
	if index, ok := byShard[shardID]; ok {
		return index
	}
	index := len(record.Shards)
	byShard[shardID] = index
	record.Shards = append(record.Shards, shard)
	return index
}

// loadRetainedConsumerTimingWarnings 批量恢复结构化终态 timing warning 投影。
func loadRetainedConsumerTimingWarnings(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	query := `SELECT job_id, agent_token_digest, accepted_generation, scope, shard_identity, workload_id, evidence_kind, action, evidence_started_at_unix_ms, observed_at_unix_ms, evidence_duration_ms, target_ms, warning_text FROM ` + cicontract.RunTimingWarningsTable + ` WHERE job_id IN (` + placeholders + `) ORDER BY job_id, scope, shard_identity, workload_id, evidence_kind, target_ms`
	rows, err := tx.Query(query, stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer timing warnings", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		if err := scanAndAppendRetainedConsumerTimingWarning(rows, records); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer timing warnings", err)
	}
	return nil
}

// scanAndAppendRetainedConsumerTimingWarning 严格恢复单条结构化 timing warning。
func scanAndAppendRetainedConsumerTimingWarning(rows *sql.Rows, records map[string]RemoteCIRunRecord) error {
	var jobID, generation, scope, workloadID, evidenceKind, action string
	var startedMS, observedMS int64
	var warning RemoteCITimingWarning
	if err := rows.Scan(&jobID, &warning.AgentTokenDigest, &generation, &scope, &warning.ShardIdentity, &workloadID, &evidenceKind, &action, &startedMS, &observedMS, &warning.EvidenceDurationMS, &warning.TargetMS, &warning.WarningText); err != nil {
		return mapDurationLedgerSQLiteError("scan retained consumer timing warning", err)
	}
	accepted, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || accepted == 0 || generation != strconv.FormatUint(accepted, 10) {
		return errors.New("stored retained consumer timing warning generation is invalid")
	}
	warning.JobID, warning.AcceptedGeneration = jobID, accepted
	warning.Scope, warning.WorkloadID = cicontract.TimingScope(scope), GateID(workloadID)
	warning.EvidenceKind, warning.Action = cicontract.TimingWarningEvidenceKind(evidenceKind), cicontract.TimingWarningAction(action)
	warning.EvidenceStartedAt, warning.ObservedAt = time.UnixMilli(startedMS).UTC(), time.UnixMilli(observedMS).UTC()
	if err := warning.Validate(); err != nil {
		return fmt.Errorf("validate retained consumer timing warning: %w", err)
	}
	record, ok := records[jobID]
	if !ok {
		return errors.New("retained consumer timing warning references an unknown run")
	}
	record.TimingWarnings = append(record.TimingWarnings, warning)
	records[jobID] = record
	return nil
}

// loadRetainedConsumerCompileTimingObservations 批量恢复 compile timing 投影。
func loadRetainedConsumerCompileTimingObservations(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT job_id, package_target, semantic_key, platform, runner_identity_digest, toolchain_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, duration_ms, started_at_unix_ms, completed_at_unix_ms, measurement, aggregation FROM ci_compile_timing_observations WHERE job_id IN (`+placeholders+") ORDER BY job_id, package_target, semantic_key, platform, runner_identity_digest, toolchain_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib, started_at_unix_ms, completed_at_unix_ms", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer compile timings", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		if err := scanAndAppendRetainedConsumerCompileTiming(rows, records); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer compile timings", err)
	}
	return nil
}

// scanAndAppendRetainedConsumerCompileTiming 严格恢复单条 compile timing 行。
func scanAndAppendRetainedConsumerCompileTiming(rows *sql.Rows, records map[string]RemoteCIRunRecord) error {
	var jobID, measurement, aggregation string
	var startedMS, completedMS int64
	var observation CompileTimingObservation
	if err := rows.Scan(&jobID, &observation.Identity.PackageTarget, &observation.Identity.SemanticKey, &observation.Identity.Platform, &observation.Identity.RunnerIdentityDigest, &observation.Identity.ToolchainDigest, &observation.Identity.ExecutionMode, &observation.Identity.ResourceClassID, &observation.Identity.ResourceCPU, &observation.Identity.ResourceMemoryGiB, &observation.DurationMS, &startedMS, &completedMS, &measurement, &aggregation); err != nil {
		return mapDurationLedgerSQLiteError("scan retained consumer compile timing", err)
	}
	observation.StartedAt, observation.CompletedAt = time.UnixMilli(startedMS).UTC(), time.UnixMilli(completedMS).UTC()
	observation.Measurement, observation.Aggregation = cicontract.ObservationState(measurement), cicontract.TimingAggregation(aggregation)
	if err := observation.Validate(); err != nil {
		return fmt.Errorf("validate retained consumer compile timing: %w", err)
	}
	record, ok := records[jobID]
	if !ok {
		return errors.New("retained consumer compile timing references an unknown run")
	}
	record.CompileTimingObservations = append(record.CompileTimingObservations, observation)
	records[jobID] = record
	return nil
}

type retainedConsumerTimingScanner struct {
	rows  *sql.Rows
	jobID *string
}

// Scan 为既有 timing row scanner 注入 batch 查询返回的 job_id。
func (scanner retainedConsumerTimingScanner) Scan(destinations ...any) error {
	return scanner.rows.Scan(append([]any{scanner.jobID}, destinations...)...)
}

// timingObservationJobID 在严格 Validate 前返回本批量行的实际 job 绑定。
func (scanner retainedConsumerTimingScanner) timingObservationJobID() string {
	return *scanner.jobID
}

// loadRetainedConsumerTimingObservations 批量恢复完整 timing observation 投影。
func loadRetainedConsumerTimingObservations(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT job_id, scope, shard_identity, workload_id, phase, started_at_unix_ms, completed_at_unix_ms, duration_ms, measurement, reason, aggregation, cache_evidence_json, compile_group_id, compile_artifact_key, compile_package_target, compile_workload_ids_json, compile_artifact_sha256, compile_artifact_size, compile_cache_hits, compile_cache_misses, compile_cache_puts, compile_cache_status, compile_status, compile_exit_code, compile_error_text, compile_command_digest, compile_profile_digest, compile_resource_class_id, compile_resource_cpu, compile_resource_memory_gib, compile_execution_mode FROM ci_timing_observations WHERE job_id IN (`+placeholders+") ORDER BY job_id, scope, shard_identity, workload_id, phase, compile_group_id, compile_artifact_key", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer timing observations", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		var jobID string
		observation, err := scanTimingObservation(retainedConsumerTimingScanner{rows: rows, jobID: &jobID}, "")
		if err != nil {
			return err
		}
		observation.JobID = jobID
		record, ok := records[jobID]
		if !ok {
			return errors.New("retained consumer timing observation references an unknown run")
		}
		record.TimingObservations = append(record.TimingObservations, observation)
		records[jobID] = record
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer timing observations", err)
	}
	return nil
}

// loadRetainedConsumerWarnings 批量恢复普通 warning 文本投影。
func loadRetainedConsumerWarnings(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT job_id, warning_text FROM ci_run_warnings WHERE job_id IN (`+placeholders+") ORDER BY job_id, ordinal", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer warnings", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		var jobID, warning string
		if err := rows.Scan(&jobID, &warning); err != nil {
			return mapDurationLedgerSQLiteError("scan retained consumer warning", err)
		}
		record, ok := records[jobID]
		if !ok {
			return errors.New("retained consumer warning references an unknown run")
		}
		record.Warnings = append(record.Warnings, warning)
		records[jobID] = record
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer warnings", err)
	}
	return nil
}

type retainedConsumerCatalog struct {
	catalog      WorkloadCatalog
	observations map[string]struct{}
}

// loadRetainedConsumerCatalogs 使用固定 IN 查询装配 catalog、workload 与观测投影。
func loadRetainedConsumerCatalogs(tx *sql.Tx, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) (map[string]retainedConsumerCatalog, error) {
	digests := retainedConsumerCatalogDigests(records)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(digests)), ",")
	catalogs, counts, err := queryRetainedConsumerCatalogs(tx, digests, placeholders, stats)
	if err != nil {
		return nil, err
	}
	if err := queryRetainedConsumerCatalogWorkloads(tx, digests, placeholders, catalogs, stats); err != nil {
		return nil, err
	}
	if err := validateRetainedConsumerCatalogs(catalogs, counts); err != nil {
		return nil, err
	}
	if err := queryRetainedConsumerCatalogObservations(tx, digests, placeholders, catalogs, stats); err != nil {
		return nil, err
	}
	return catalogs, nil
}

// retainedConsumerCatalogDigests 去重每个 chunk 实际绑定的 catalog 身份。
func retainedConsumerCatalogDigests(records map[string]RemoteCIRunRecord) []string {
	digests, seen := make([]string, 0, len(records)), make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, ok := seen[record.CatalogDigest]; !ok {
			seen[record.CatalogDigest] = struct{}{}
			digests = append(digests, record.CatalogDigest)
		}
	}
	return digests
}

// queryRetainedConsumerCatalogs 查询 catalog 主投影与预期 workload 数。
func queryRetainedConsumerCatalogs(tx *sql.Tx, digests []string, placeholders string, stats *workloadPassEvidenceLookupStats) (map[string]retainedConsumerCatalog, map[string]int, error) {
	rows, err := tx.Query(`SELECT catalog_digest, catalog_version, authoritative, workload_count FROM ci_workload_catalogs WHERE catalog_digest IN (`+placeholders+") ORDER BY catalog_digest", stringsToAny(digests)...)
	if err != nil {
		return nil, nil, mapDurationLedgerSQLiteError("batch load retained consumer catalogs", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	catalogs, counts := make(map[string]retainedConsumerCatalog, len(digests)), make(map[string]int, len(digests))
	for rows.Next() {
		var digest string
		var version, authoritative, count int
		if err := rows.Scan(&digest, &version, &authoritative, &count); err != nil {
			return nil, nil, mapDurationLedgerSQLiteError("scan retained consumer catalog", err)
		}
		if authoritative != 0 && authoritative != 1 || count < 0 {
			return nil, nil, errors.New("stored retained consumer catalog shape is invalid")
		}
		catalogs[digest] = retainedConsumerCatalog{catalog: WorkloadCatalog{Version: version, Authoritative: authoritative == 1}, observations: make(map[string]struct{})}
		counts[digest] = count
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapDurationLedgerSQLiteError("iterate retained consumer catalogs", err)
	}
	if len(catalogs) != len(digests) {
		return nil, nil, errors.New("retained consumer catalog projection is incomplete")
	}
	return catalogs, counts, nil
}

// queryRetainedConsumerCatalogWorkloads 查询 catalog workload 子投影并按 digest 分组。
func queryRetainedConsumerCatalogWorkloads(tx *sql.Tx, digests []string, placeholders string, catalogs map[string]retainedConsumerCatalog, stats *workloadPassEvidenceLookupStats) error {
	rows, err := tx.Query(`SELECT catalog_digest, workload_id, kind, command_digest, input_digest, bootstrap_estimate_ms, shardable FROM ci_catalog_workloads WHERE catalog_digest IN (`+placeholders+") ORDER BY catalog_digest, ordinal", stringsToAny(digests)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer catalog workloads", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		var digest, kind string
		var workload Workload
		var shardable int
		if err := rows.Scan(&digest, &workload.ID, &kind, &workload.CommandDigest, &workload.InputDigest, &workload.BootstrapEstimateMS, &shardable); err != nil {
			return mapDurationLedgerSQLiteError("scan retained consumer catalog workload", err)
		}
		item, ok := catalogs[digest]
		if !ok || shardable != 0 && shardable != 1 {
			return errors.New("retained consumer catalog workload binding is invalid")
		}
		workload.Kind, workload.Shardable = WorkloadKind(kind), shardable == 1
		item.catalog.Workloads = append(item.catalog.Workloads, workload)
		catalogs[digest] = item
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer catalog workloads", err)
	}
	return nil
}

// validateRetainedConsumerCatalogs 验证 batch 组装后的条目计数、内容和 digest。
func validateRetainedConsumerCatalogs(catalogs map[string]retainedConsumerCatalog, counts map[string]int) error {
	for digest, item := range catalogs {
		if len(item.catalog.Workloads) != counts[digest] {
			return errors.New("retained consumer catalog workload count is inconsistent")
		}
		if err := ValidateWorkloadCatalog(item.catalog); err != nil {
			return fmt.Errorf("validate retained consumer catalog: %w", err)
		}
		actual, err := WorkloadCatalogDigest(item.catalog)
		if err != nil || actual != digest {
			return errors.New("retained consumer catalog digest is invalid")
		}
	}
	return nil
}

// queryRetainedConsumerCatalogObservations 查询 catalog 的不可变观测身份。
func queryRetainedConsumerCatalogObservations(tx *sql.Tx, digests []string, placeholders string, catalogs map[string]retainedConsumerCatalog, stats *workloadPassEvidenceLookupStats) error {
	rows, err := tx.Query(`SELECT catalog_digest, source_tree_sha, entrypoint, profile, accepted_generation FROM ci_catalog_observations WHERE catalog_digest IN (`+placeholders+") ORDER BY catalog_digest", stringsToAny(digests)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer catalog observations", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		var digest, source, entrypoint, profile, generation string
		if err := rows.Scan(&digest, &source, &entrypoint, &profile, &generation); err != nil {
			return mapDurationLedgerSQLiteError("scan retained consumer catalog observation", err)
		}
		item, ok := catalogs[digest]
		if !ok {
			return errors.New("retained consumer catalog observation references unknown catalog")
		}
		item.observations[retainedConsumerCatalogObservationKey(source, entrypoint, profile, generation)] = struct{}{}
		catalogs[digest] = item
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer catalog observations", err)
	}
	return nil
}

// incrementRetainedConsumerBatchQueries 仅在真实 SQLite Query 成功后记录查询次数。
func incrementRetainedConsumerBatchQueries(stats *workloadPassEvidenceLookupStats) {
	if stats != nil {
		stats.retainedConsumerBatchQueries++
	}
}

func retainedConsumerCatalogObservationKey(source, entrypoint, profile, generation string) string {
	return source + "\x00" + entrypoint + "\x00" + profile + "\x00" + generation
}

// validateRetainedConsumerCatalogCoverage 校验 catalog、观测和 run 记录覆盖闭环。
func validateRetainedConsumerCatalogCoverage(record RemoteCIRunRecord, catalogs map[string]retainedConsumerCatalog) error {
	item, ok := catalogs[record.CatalogDigest]
	if !ok || !item.catalog.Authoritative {
		return errors.New("retained consumer requires an authoritative workload catalog")
	}
	key := retainedConsumerCatalogObservationKey(record.SourceTreeSHA, string(record.Entrypoint), string(record.Profile), strconv.FormatUint(record.AcceptedGeneration, 10))
	if _, observed := item.observations[key]; !observed {
		return errors.New("retained consumer requires a matching workload catalog observation")
	}
	index, err := newRemoteCIRunCatalogIndex(item.catalog)
	if err != nil {
		return err
	}
	if err := index.validateRecorded(record, remoteCIRunRecordedWorkloads(record)); err != nil {
		return err
	}
	scope, err := resolveRemoteCIRunExecutionScope(record.Scope, item.catalog)
	if err != nil {
		return err
	}
	if err := validateRemoteCIRunScopeRecords(record, item.catalog, scope); err != nil {
		return err
	}
	return index.validatePassed(record, scope)
}
