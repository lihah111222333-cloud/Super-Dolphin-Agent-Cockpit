package gate

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// errLegacyRemoteCIExecutionProfile marks rows written before the current
// ExecutionProfile semantic surface. PASS lookup treats it as a cache MISS;
// ordinary authoritative reads remain strict and return the error.
var errLegacyRemoteCIExecutionProfile = errors.New("legacy remote CI execution profile")

// loadRemoteCIRunRow 从一致性读快照解码远端 CI run 的主记录。
func loadRemoteCIRunRow(database sqliteRowQueryer, jobID string) (RemoteCIRunRecord, error) {
	var (
		record                                          RemoteCIRunRecord
		entrypoint, profile, status, acceptedGeneration string
		force, authoritative, cleanupComplete           int
		startedAtMS, completedAtMS                      int64
	)
	err := database.QueryRow(`
		SELECT runs.job_id, identities.agent_token_digest,
			runs.force, runs.entrypoint, runs.profile, runs.plan_digest, runs.catalog_digest, runs.accepted_generation, runs.image_cache_snapshot_id,
			runs.source_tree_sha, runs.candidate_gate_source_sha256, runs.candidate_gate_toolchain_sha256,
			runs.runner_image, runs.status, runs.authoritative,
			runs.started_at_unix_ms, runs.completed_at_unix_ms,
			runs.cleanup_complete, runs.error_text
		FROM ci_runs AS runs
		INNER JOIN ci_run_agent_identities AS identities ON identities.job_id = runs.job_id
		WHERE runs.job_id = ?
	`, jobID).Scan(
		&record.JobID,
		&record.AgentTokenDigest,
		&force,
		&entrypoint,
		&profile,
		&record.PlanDigest,
		&record.CatalogDigest,
		&acceptedGeneration,
		&record.ImageCacheSnapshotID,
		&record.SourceTreeSHA,
		&record.CandidateGateSourceSHA256,
		&record.CandidateGateToolchainSHA256,
		&record.RunnerImage,
		&status,
		&authoritative,
		&startedAtMS,
		&completedAtMS,
		&cleanupComplete,
		&record.ErrorText,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteCIRunRecord{}, fmt.Errorf("%w: %s", ErrRemoteCIRunNotFound, jobID)
	}
	if err != nil {
		return RemoteCIRunRecord{}, mapDurationLedgerSQLiteError("load remote CI run", err)
	}
	record.Entrypoint = CIEntrypointID(entrypoint)
	record.Profile = Profile(profile)
	if record.AcceptedGeneration, err = strconv.ParseUint(acceptedGeneration, 10, 64); err != nil || record.AcceptedGeneration == 0 {
		return RemoteCIRunRecord{}, errors.New("stored remote CI accepted generation is invalid")
	}
	if force != 0 && force != 1 {
		return RemoteCIRunRecord{}, errors.New("stored remote CI force identity is invalid")
	}
	record.Status = ResultStatus(status)
	record.Force = force == 1
	if err := cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("stored remote CI agent token digest: %w", err)
	}
	record.Authoritative = authoritative == 1
	record.CleanupComplete = cleanupComplete == 1
	record.StartedAt = time.UnixMilli(startedAtMS).UTC()
	record.CompletedAt = time.UnixMilli(completedAtMS).UTC()
	return record, nil
}

// loadRemoteCIRunDetails 在同一个只读事务中补全 run 的关联投影。
func loadRemoteCIRunDetails(transaction *sql.Tx, jobID string, record *RemoteCIRunRecord) error {
	var err error
	record.Scope, err = loadRemoteCIExecutionScope(transaction, jobID, record.AcceptedGeneration)
	if err != nil {
		return err
	}
	record.Shards, err = loadRemoteCIShardRows(transaction, jobID, record.Status, record.Authoritative)
	if err != nil {
		return err
	}
	record.Executions, err = loadRemoteCIExecutionRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.WorkloadExecutions, err = loadRemoteCIWorkloadExecutionRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.WorkloadResults, err = loadRemoteCIWorkloadResults(transaction, jobID)
	if err != nil {
		return err
	}
	record.Warnings, err = loadRemoteCIRunWarningRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.TimingWarnings, err = loadRemoteCITimingWarnings(transaction, cicontract.RunTimingWarningsTable, jobID)
	if err != nil {
		return err
	}
	record.TimingObservations, err = loadTimingObservations(transaction, jobID)
	if err != nil {
		return err
	}
	record.CompileTimingObservations, err = loadSQLiteCompileTimingObservations(transaction, jobID)
	if err != nil {
		return err
	}
	return nil
}

// loadTimingObservations 从同一 SQLite 快照还原真实阶段观测，保留结构化时长和缓存证据而不从日志推断。
func loadTimingObservations(database sqliteRowQueryer, jobID string) ([]TimingObservation, error) {
	rows, err := database.Query(`SELECT scope, shard_identity, workload_id, phase, started_at_unix_ms, completed_at_unix_ms, duration_ms, measurement, reason, aggregation, cache_evidence_json,
		compile_group_id, compile_artifact_key, compile_package_target, compile_workload_ids_json, compile_artifact_sha256, compile_artifact_size,
		compile_cache_hits, compile_cache_misses, compile_cache_puts, compile_cache_status, compile_status, compile_exit_code, compile_error_text,
		compile_command_digest, compile_profile_digest, compile_resource_class_id, compile_resource_cpu, compile_resource_memory_gib, compile_execution_mode
		FROM ci_timing_observations WHERE job_id = ? ORDER BY scope, shard_identity, workload_id, phase, compile_group_id, compile_artifact_key`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query timing observations", err)
	}
	defer rows.Close()
	var observations []TimingObservation
	for rows.Next() {
		observation, err := scanTimingObservation(rows, jobID)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate timing observations", err)
	}
	return observations, nil
}

type timingObservationRowScanner interface {
	Scan(...any) error
}

// timingObservationJobIDProvider 允许批量 reader 在 Scan 后、严格验证前注入行所属 job。
type timingObservationJobIDProvider interface {
	timingObservationJobID() string
}

// scanTimingObservation 从 SQLite 行严格恢复阶段、缓存和编译计数，并执行完整观测校验。
func scanTimingObservation(scanner timingObservationRowScanner, jobID string) (TimingObservation, error) {
	var scope, workloadID, phase, measurement, aggregation string
	var startedMS, completedMS int64
	observation := TimingObservation{JobID: jobID}
	var cacheEvidenceJSON, compileWorkloadIDsJSON string
	var compileCacheHits, compileCacheMisses, compileCachePuts int64
	if err := scanner.Scan(&scope, &observation.ShardIdentity, &workloadID, &phase, &startedMS, &completedMS, &observation.DurationMS, &measurement, &observation.Reason, &aggregation, &cacheEvidenceJSON,
		&observation.CompileGroupID, &observation.CompileArtifactKey, &observation.CompilePackageTarget, &compileWorkloadIDsJSON, &observation.CompileArtifactSHA256, &observation.CompileArtifactSize,
		&compileCacheHits, &compileCacheMisses, &compileCachePuts, &observation.CompileCacheStatus, &observation.CompileStatus, &observation.CompileExitCode, &observation.CompileErrorText,
		&observation.CompileCommandDigest, &observation.CompileProfileDigest, &observation.CompileResourceClassID, &observation.CompileResourceCPU, &observation.CompileResourceMemoryGiB, &observation.CompileExecutionMode); err != nil {
		return TimingObservation{}, mapDurationLedgerSQLiteError("scan timing observation", err)
	}
	if provider, ok := scanner.(timingObservationJobIDProvider); ok {
		observation.JobID = provider.timingObservationJobID()
	}
	if compileCacheHits < 0 || compileCacheMisses < 0 || compileCachePuts < 0 {
		return TimingObservation{}, errors.New("stored compile group cache counter is negative")
	}
	observation.CompileCacheHits, observation.CompileCacheMisses, observation.CompileCachePuts = uint64(compileCacheHits), uint64(compileCacheMisses), uint64(compileCachePuts)
	observation.Scope, observation.WorkloadID, observation.Phase = cicontract.TimingScope(scope), GateID(workloadID), cicontract.TimingPhase(phase)
	if observation.Scope == cicontract.TimingScopeCompileGroup {
		if err := decodeCompileGroupWorkloadIDs([]byte(compileWorkloadIDsJSON), &observation.CompileWorkloadIDs); err != nil {
			return TimingObservation{}, fmt.Errorf("decode stored compile group workload IDs: %w", err)
		}
	}
	observation.Measurement, observation.Aggregation = cicontract.ObservationState(measurement), cicontract.TimingAggregation(aggregation)
	if err := DecodeStrictJSON([]byte(cacheEvidenceJSON), &observation.CacheEvidence); err != nil {
		return TimingObservation{}, fmt.Errorf("decode stored timing cache evidence: %w", err)
	}
	observation.StartedAt, observation.CompletedAt = timingObservationTimes(startedMS, completedMS)
	if err := observation.Validate(); err != nil {
		return TimingObservation{}, errors.New("stored timing observation is invalid")
	}
	return observation, nil
}

func timingObservationTimes(startedMS, completedMS int64) (time.Time, time.Time) {
	var startedAt, completedAt time.Time
	if startedMS != 0 {
		startedAt = time.UnixMilli(startedMS).UTC()
	}
	if completedMS != 0 {
		completedAt = time.UnixMilli(completedMS).UTC()
	}
	return startedAt, completedAt
}

// decodeCompileGroupWorkloadIDs 严格解码编译组 workload 列表并拒绝空值或重复身份。
func decodeCompileGroupWorkloadIDs(data []byte, target *[]GateID) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if len(*target) == 0 {
		return errors.New("compile group workload IDs are empty")
	}
	seen := make(map[GateID]struct{}, len(*target))
	for _, workloadID := range *target {
		if workloadID == "" {
			return errors.New("compile group workload ID is empty")
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("compile group workload ID %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
	}
	return nil
}

// loadRemoteCIWorkloadExecutionRows 读取每个计划 workload 的实际执行结果，供回执验证其未被历史 PASS 跳过。
func loadRemoteCIWorkloadExecutionRows(database sqliteRowQueryer, jobID string) ([]PlanGateExecution, error) {
	rows, err := database.Query(`
		SELECT shard_identity, workload_id, status, exit_code, started_at_unix_ms, completed_at_unix_ms,
			argv_digest, log_digest, test_timings_json, execution_profile_json
		FROM ci_workload_executions
		WHERE job_id = ?
		ORDER BY workload_id
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI workload executions", err)
	}
	defer rows.Close()
	var executions []PlanGateExecution
	for rows.Next() {
		var execution PlanGateExecution
		var startedMS, completedMS int64
		var profileJSON, testTimingsJSON string
		if err := rows.Scan(&execution.ShardIdentity, &execution.GateID, &execution.Status, &execution.ExitCode, &startedMS, &completedMS, &execution.ArgvDigest, &execution.LogDigest, &testTimingsJSON, &profileJSON); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI workload execution", err)
		}
		execution.StartedAt = time.UnixMilli(startedMS).UTC()
		execution.CompletedAt = time.UnixMilli(completedMS).UTC()
		if execution.TestTimings, err = decodeStoredRemoteCIExecutionTestTimings(testTimingsJSON); err != nil {
			return nil, err
		}
		execution.ExecutionProfile, err = decodeStoredRemoteCIExecutionProfile(profileJSON)
		if err != nil {
			return nil, err
		}
		if err := execution.ExecutionProfile.Validate(); err != nil {
			return nil, errors.New("stored remote CI workload execution profile is invalid")
		}
		expectedFlags, err := WorkloadExecutionGoFlags(string(execution.GateID))
		if err != nil {
			return nil, fmt.Errorf("stored remote CI workload %q expected GoFlags: %w", execution.GateID, err)
		}
		if execution.ExecutionProfile.GoFlags != expectedFlags {
			return nil, fmt.Errorf("stored remote CI workload %q profile GoFlags %q does not match expected %q", execution.GateID, execution.ExecutionProfile.GoFlags, expectedFlags)
		}
		executions = append(executions, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI workload executions", err)
	}
	return executions, nil
}

// loadRemoteCIShardRows 从读取快照恢复 run 的 shard 投影。
func loadRemoteCIShardRows(
	database sqliteRowQueryer,
	jobID string,
	status ResultStatus,
	authoritative bool,
) ([]RemoteCIShardRecord, error) {
	rows, err := database.Query(`
		SELECT shards.shard_identity, shards.container_group_id, shards.container_status, shards.materialization_timing_json, shards.resources_json,
			workloads.workload_id
		FROM ci_shards AS shards
		LEFT JOIN ci_shard_workloads AS workloads
			ON workloads.job_id = shards.job_id
			AND workloads.shard_identity = shards.shard_identity
		WHERE shards.job_id = ?
		ORDER BY shards.shard_identity, workloads.workload_id
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI shards", err)
	}
	defer rows.Close()
	var (
		shards     []RemoteCIShardRecord
		shardIndex = make(map[string]int)
	)
	for rows.Next() {
		var (
			shard         RemoteCIShardRecord
			workloadID    sql.NullString
			timingJSON    string
			resourcesJSON string
		)
		if err := rows.Scan(
			&shard.ShardIdentity,
			&shard.ContainerGroup,
			&shard.ContainerStatus,
			&timingJSON,
			&resourcesJSON,
			&workloadID,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI shard", err)
		}
		if err := decodeStoredRemoteCIShardEvidence(&shard, timingJSON, resourcesJSON, status, authoritative); err != nil {
			return nil, err
		}
		index, exists := shardIndex[shard.ShardIdentity]
		if !exists {
			index = len(shards)
			shardIndex[shard.ShardIdentity] = index
			shards = append(shards, shard)
		}
		if workloadID.Valid {
			if strings.TrimSpace(workloadID.String) == "" {
				return nil, errors.New("stored remote CI shard workload ID is invalid")
			}
			shards[index].Workloads = append(shards[index].Workloads, GateID(workloadID.String))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI shards", err)
	}
	if err := rows.Close(); err != nil {
		return nil, mapDurationLedgerSQLiteError("close remote CI shards", err)
	}
	return loadRemoteCIShardTerminalEvidenceIntoShards(database, jobID, shards, shardIndex)
}

// loadRemoteCIShardTerminalEvidenceIntoShards 严格加载并绑定分片终态证据，拒绝未知分片身份。
func loadRemoteCIShardTerminalEvidenceIntoShards(
	database sqliteRowQueryer,
	jobID string,
	shards []RemoteCIShardRecord,
	shardIndex map[string]int,
) ([]RemoteCIShardRecord, error) {
	terminalEvidence, err := loadRemoteCIShardTerminalEvidence(database, jobID)
	if err != nil {
		return nil, err
	}
	for shardIdentity, evidence := range terminalEvidence {
		index, exists := shardIndex[shardIdentity]
		if !exists {
			return nil, fmt.Errorf("stored remote CI terminal evidence references unknown shard %q", shardIdentity)
		}
		shards[index].TerminalEvidence = evidence
	}
	return shards, nil
}

// decodeStoredRemoteCIShardEvidence 严格解码并校验 SQLite 分片的资源和物化时序证据。
// 未创建分片没有 ECI 资源可记录；其空 resources_json 只有在完整的
// Unknown/not_measured placeholder 形状下才可回读，不能被解释为零资源。
func decodeStoredRemoteCIShardEvidence(shard *RemoteCIShardRecord, timingJSON, resourcesJSON string, status ResultStatus, authoritative bool) error {
	if shard == nil {
		return errors.New("stored remote CI shard is required")
	}
	if resourcesJSON != "" {
		if err := DecodeStrictJSON([]byte(resourcesJSON), &shard.Resources); err != nil {
			return fmt.Errorf("decode stored remote CI shard resources: %w", err)
		}
		if err := shard.Resources.Validate(); err != nil {
			return fmt.Errorf("validate stored remote CI shard resources: %w", err)
		}
	}
	if timingJSON == "" {
		return errors.New("stored remote CI shard materialization timing is required")
	}
	if err := DecodeStrictJSON([]byte(timingJSON), &shard.MaterializationTiming); err != nil {
		return fmt.Errorf("decode stored remote CI shard materialization timing: %w", err)
	}
	if err := validateRemoteCIShardMaterializationTiming(status, authoritative, *shard); err != nil {
		return fmt.Errorf("validate stored remote CI shard materialization timing: %w", err)
	}
	if resourcesJSON == "" && !remoteCIShardResourcesMissingAllowed(status, authoritative, *shard) {
		return errors.New("stored remote CI shard resources are required")
	}
	return nil
}

// loadRemoteCIExecutionRows 从读取快照恢复 gate 终态投影。
func loadRemoteCIExecutionRows(
	database sqliteRowQueryer,
	jobID string,
) ([]PlanGateExecution, error) {
	rows, err := database.Query(`
		SELECT workload_id, status, exit_code, started_at_unix_ms,
			completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json
		FROM ci_gate_executions
		WHERE job_id = ?
		ORDER BY workload_id
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI gate executions", err)
	}
	defer rows.Close()
	var executions []PlanGateExecution
	for rows.Next() {
		var (
			execution                    PlanGateExecution
			workloadID, status           string
			startedAtMS, completedAtMS   int64
			testTimingsJSON, profileJSON string
		)
		if err := rows.Scan(
			&workloadID,
			&status,
			&execution.ExitCode,
			&startedAtMS,
			&completedAtMS,
			&execution.ArgvDigest,
			&execution.LogDigest,
			&testTimingsJSON,
			&profileJSON,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI gate execution", err)
		}
		execution.GateID = GateID(workloadID)
		execution.Status = ResultStatus(status)
		execution.StartedAt = time.UnixMilli(startedAtMS).UTC()
		execution.CompletedAt = time.UnixMilli(completedAtMS).UTC()
		if execution.TestTimings, err = decodeStoredRemoteCIExecutionTestTimings(testTimingsJSON); err != nil {
			return nil, err
		}
		execution.ExecutionProfile, err = decodeStoredRemoteCIAggregateExecutionProfile(profileJSON)
		if err != nil {
			return nil, err
		}
		if err := validateRemoteCIAggregateExecution(execution); err != nil {
			return nil, err
		}
		executions = append(executions, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI gate executions", err)
	}
	return executions, nil
}

func decodeStoredRemoteCIExecutionTestTimings(encoded string) ([]GoTestTiming, error) {
	var timings []GoTestTiming
	if err := decodeStrictJSON(bytes.NewReader([]byte(encoded)), &timings); err != nil ||
		!validPlanGateTestTimings(timings, ExecutorPlanReportSchemaVersion) {
		return nil, errors.New("stored remote CI execution test timings are invalid")
	}
	return timings, nil
}

// decodeStoredRemoteCIExecutionProfile 只接受非空且无未知字段的当前结构化执行画像。
func decodeStoredRemoteCIExecutionProfile(encoded string) (ExecutionProfile, error) {
	if strings.TrimSpace(encoded) == "" {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &fields); err != nil {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is invalid")
	}
	if fields == nil {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is invalid")
	}
	if err := validateStoredRemoteCIGoFlags(fields); err != nil {
		return ExecutionProfile{}, err
	}
	var profile ExecutionProfile
	if err := DecodeStrictJSON([]byte(encoded), &profile); err != nil {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is invalid")
	}
	return profile, nil
}

// storedRemoteCIAggregateExecutionProfile 让 strict decoder 使用 parent gate 的区间并集语义。
type storedRemoteCIAggregateExecutionProfile ExecutionProfile

// Validate 拒绝旧结构和无效关键路径，同时允许 startup/test-body 区间重叠。
func (profile storedRemoteCIAggregateExecutionProfile) Validate() error {
	return ExecutionProfile(profile).ValidateAggregate()
}

// decodeStoredRemoteCIAggregateExecutionProfile 严格读取协调器生成的 parent gate 聚合画像。
func decodeStoredRemoteCIAggregateExecutionProfile(encoded string) (ExecutionProfile, error) {
	if strings.TrimSpace(encoded) == "" {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &fields); err != nil {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is invalid")
	}
	if fields == nil {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is invalid")
	}
	if err := validateStoredRemoteCIGoFlags(fields); err != nil {
		return ExecutionProfile{}, err
	}
	var profile storedRemoteCIAggregateExecutionProfile
	if err := DecodeStrictJSON([]byte(encoded), &profile); err != nil {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is invalid")
	}
	return ExecutionProfile(profile), nil
}

// validateStoredRemoteCIGoFlags requires a concrete JSON string. Presence
// alone is insufficient because encoding/json silently maps null to "".
func validateStoredRemoteCIGoFlags(fields map[string]json.RawMessage) error {
	raw, present := fields["go_flags"]
	if !present {
		return fmt.Errorf("%w: stored remote CI execution profile is invalid: go_flags field is missing", errLegacyRemoteCIExecutionProfile)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("stored remote CI execution profile is invalid")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("stored remote CI execution profile is invalid")
	}
	return nil
}

// validateRemoteCIAggregateExecution 校验 parent gate 的区间并集/关键路径 profile 未与执行时间戳漂移。
func validateRemoteCIAggregateExecution(execution PlanGateExecution) error {
	if err := execution.ExecutionProfile.ValidateAggregate(); err != nil {
		return errors.New("stored remote CI aggregate execution profile is invalid")
	}
	if execution.StartedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) ||
		execution.ExecutionProfile.TotalMS != execution.CompletedAt.Sub(execution.StartedAt).Milliseconds() {
		return errors.New("stored remote CI aggregate execution interval is invalid")
	}
	return nil
}

func replaceSQLiteRemoteRunWarnings(
	transaction *sql.Tx,
	jobID string,
	warnings []string,
) error {
	if _, err := transaction.Exec(`DELETE FROM ci_run_warnings WHERE job_id = ?`, jobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI run warnings", err)
	}
	for ordinal, warning := range warnings {
		if _, err := transaction.Exec(`
			INSERT INTO ci_run_warnings (job_id, ordinal, warning_text)
			VALUES (?, ?, ?)
		`, jobID, ordinal, warning); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI run warning", err)
		}
	}
	return nil
}

// loadRemoteCIRunWorkloadRows 分别恢复复用与未命中 workload。
func loadRemoteCIRunWarningRows(database sqliteRowQueryer, jobID string) ([]string, error) {
	rows, err := database.Query(`
		SELECT warning_text
		FROM ci_run_warnings
		WHERE job_id = ?
		ORDER BY ordinal
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI run warnings", err)
	}
	defer rows.Close()
	var warnings []string
	for rows.Next() {
		var warning string
		if err := rows.Scan(&warning); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI run warning", err)
		}
		warnings = append(warnings, warning)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI run warnings", err)
	}
	return warnings, nil
}

// validateSQLiteRemoteCIRunCatalogCoverage 确认 run 的 workload 与 gate 记录都属于绑定 catalog。
func validateSQLiteRemoteCIRunCatalogCoverage(transaction *sql.Tx, record RemoteCIRunRecord) error {
	recordedWorkloads := remoteCIRunRecordedWorkloads(record)
	if remoteCIRunHasNoCatalogRecords(record, recordedWorkloads) {
		return nil
	}
	catalog, err := loadSQLiteWorkloadCatalog(transaction, record.CatalogDigest)
	if err != nil {
		return fmt.Errorf("load remote CI workload catalog: %w", err)
	}
	if err := validateSQLiteAuthoritativeRemoteCIRunCatalog(transaction, record, catalog.Catalog); err != nil {
		return err
	}
	index, err := newRemoteCIRunCatalogIndex(catalog.Catalog)
	if err != nil {
		return err
	}
	if err := validateRemoteCIRunScopeRecordCoverage(record, catalog.Catalog, index); err != nil {
		return err
	}
	if err := index.validateRecorded(record, recordedWorkloads); err != nil {
		return err
	}
	return nil
}

// validateSQLiteAuthoritativeRemoteCIRunCatalog 校验权威成功 run 的目录及观测权威性。
func validateSQLiteAuthoritativeRemoteCIRunCatalog(transaction *sql.Tx, record RemoteCIRunRecord, catalog WorkloadCatalog) error {
	if record.Status != ResultStatusPassed || !record.Authoritative {
		return nil
	}
	if !catalog.Authoritative {
		return errors.New("passed remote CI run requires an authoritative workload catalog")
	}
	return validateSQLiteRemoteCIRunCatalogObservation(transaction, record)
}

// validateSQLiteRemoteCIRunCatalogObservation 确认权威 run 的目录观测与 run identity 完全一致。
func validateSQLiteRemoteCIRunCatalogObservation(transaction *sql.Tx, record RemoteCIRunRecord) error {
	var authoritative int
	err := transaction.QueryRow(`
		SELECT catalogs.authoritative
		FROM ci_catalog_observations AS observations
		INNER JOIN ci_workload_catalogs AS catalogs ON catalogs.catalog_digest = observations.catalog_digest
		WHERE observations.catalog_digest = ?
			AND observations.source_tree_sha = ?
			AND observations.entrypoint = ?
			AND observations.profile = ?
			AND observations.accepted_generation = ?
	`, record.CatalogDigest, record.SourceTreeSHA, string(record.Entrypoint), string(record.Profile), strconv.FormatUint(record.AcceptedGeneration, 10)).Scan(&authoritative)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("authoritative remote CI run requires a matching workload catalog observation")
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load remote CI workload observation", err)
	}
	if authoritative != 1 {
		return errors.New("authoritative remote CI run requires an authoritative workload catalog observation")
	}
	return nil
}

type remoteCIRunCatalogIndex struct {
	catalog    WorkloadCatalog
	workloads  map[GateID]struct{}
	shardable  map[GateID]struct{}
	executions map[GateID]struct{}
}

// remoteCIRunRecordedWorkloads 汇总 run 的直接和 shard 记录 workload。
func remoteCIRunRecordedWorkloads(record RemoteCIRunRecord) []GateID {
	var workloads []GateID
	for _, shard := range record.Shards {
		workloads = append(workloads, shard.Workloads...)
	}
	return workloads
}

// remoteCIRunHasNoCatalogRecords 识别非 PASS 且没有任何 catalog 投影的空 run。
func remoteCIRunHasNoCatalogRecords(record RemoteCIRunRecord, workloads []GateID) bool {
	return record.Status != ResultStatusPassed && len(workloads) == 0 && len(record.Executions) == 0
}

// newRemoteCIRunCatalogIndex 建立 workload 与其父 gate 的可验证索引。
func newRemoteCIRunCatalogIndex(catalog WorkloadCatalog) (remoteCIRunCatalogIndex, error) {
	index := remoteCIRunCatalogIndex{catalog: catalog, workloads: make(map[GateID]struct{}), shardable: make(map[GateID]struct{}), executions: make(map[GateID]struct{})}
	for _, workload := range catalog.Workloads {
		id := GateID(workload.ID)
		index.workloads[id] = struct{}{}
		if !workload.Shardable {
			if id != GateIDReleaseLayeredCheck {
				return remoteCIRunCatalogIndex{}, fmt.Errorf("remote CI non-shardable catalog workload %q is not the release owner", id)
			}
			// Owner-only entry 生成直接的 GateExecution，而不是 shard coverage。
			index.executions[id] = struct{}{}
			continue
		}
		parent, err := WorkloadParentGateID(workload.ID)
		if err != nil {
			return remoteCIRunCatalogIndex{}, fmt.Errorf("resolve remote CI catalog workload parent: %w", err)
		}
		index.shardable[id], index.executions[parent] = struct{}{}, struct{}{}
	}
	return index, nil
}

// validateRecorded 确认 run 的 workload 与执行 gate 都存在于 catalog。
func (index remoteCIRunCatalogIndex) validateRecorded(record RemoteCIRunRecord, workloads []GateID) error {
	for _, workloadID := range workloads {
		if _, exists := index.workloads[workloadID]; !exists {
			return fmt.Errorf("%s remote CI recorded workload %q is absent from its catalog", record.Status, workloadID)
		}
	}
	for _, execution := range record.Executions {
		if _, exists := index.executions[execution.GateID]; !exists {
			return fmt.Errorf("%s remote CI recorded gate execution %q is absent from its catalog", record.Status, execution.GateID)
		}
	}
	return nil
}

// validatePassed 要求结果覆盖 catalog；fresh 分片和执行仅记录 executed workload。
func (index remoteCIRunCatalogIndex) validatePassed(record RemoteCIRunRecord, scope RemoteCIExecutionScope) error {
	expected, err := expectedRemoteCIShardableWorkloads(index.shardable, scope)
	if err != nil {
		return err
	}
	results, executedWorkloads, err := passedRemoteCIWorkloadResults(record.WorkloadResults, expected)
	if err != nil {
		return err
	}
	shardWorkloads, shardByWorkload, err := passedFreshShardWorkloads(record.Shards, results)
	if err != nil {
		return err
	}
	if err := validateRemoteCIRunFreshWorkloadSet("shard", shardWorkloads, executedWorkloads); err != nil {
		return err
	}
	executionWorkloads, err := passedFreshExecutionWorkloads(record.WorkloadExecutions, results, shardByWorkload)
	if err != nil {
		return err
	}
	return validateRemoteCIRunFreshWorkloadSet("execution", executionWorkloads, executedWorkloads)
}

// passedFreshShardWorkloads 仅接受标记 executed 的 workload，并保留其 fresh shard 归属。
func passedFreshShardWorkloads(shards []RemoteCIShardRecord, results map[GateID]string) (map[GateID]struct{}, map[GateID]string, error) {
	workloads := make(map[GateID]struct{})
	shardsByWorkload := make(map[GateID]string)
	for _, shard := range shards {
		for _, workloadID := range shard.Workloads {
			if results[workloadID] != WorkloadDispositionExecuted {
				return nil, nil, fmt.Errorf("passed remote CI fresh shard workload %q is not executed", workloadID)
			}
			workloads[workloadID] = struct{}{}
			shardsByWorkload[workloadID] = shard.ShardIdentity
		}
	}
	return workloads, shardsByWorkload, nil
}

func passedFreshExecutionWorkloads(executions []PlanGateExecution, results map[GateID]string, shardsByWorkload map[GateID]string) (map[GateID]struct{}, error) {
	workloads := make(map[GateID]struct{}, len(executions))
	for _, execution := range executions {
		if results[execution.GateID] != WorkloadDispositionExecuted {
			return nil, fmt.Errorf("passed remote CI fresh execution workload %q is not executed", execution.GateID)
		}
		if execution.ShardIdentity != shardsByWorkload[execution.GateID] {
			return nil, fmt.Errorf("passed remote CI fresh execution workload %q does not match its shard", execution.GateID)
		}
		workloads[execution.GateID] = struct{}{}
	}
	return workloads, nil
}

func validateRemoteCIRunFreshWorkloadSet(kind string, recorded, executed map[GateID]struct{}) error {
	for workloadID := range executed {
		if _, exists := recorded[workloadID]; !exists {
			return fmt.Errorf("passed remote CI executed workload %q is missing from fresh %s records", workloadID, kind)
		}
	}
	for workloadID := range recorded {
		if _, exists := executed[workloadID]; !exists {
			return fmt.Errorf("passed remote CI fresh %s workload %q is not executed", kind, workloadID)
		}
	}
	return nil
}

// advanceCIQueryRevision 推进 SQLite 查询投影的单调版本。
func advanceCIQueryRevision(transaction *sql.Tx, now time.Time) error {
	var revisionText string
	err := transaction.QueryRow(`
		SELECT revision FROM ci_query_meta WHERE singleton = 1
	`).Scan(&revisionText)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = transaction.Exec(`
			INSERT INTO ci_query_meta (singleton, revision, updated_at_unix_ms)
			VALUES (1, '1', ?)
		`, now.UnixMilli())
		return mapDurationLedgerSQLiteError("initialize CI query revision", err)
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load CI query revision", err)
	}
	revision, err := strconv.ParseUint(revisionText, 10, 64)
	if err != nil || revision == ^uint64(0) {
		return errors.New("CI query revision is invalid")
	}
	result, err := transaction.Exec(`
		UPDATE ci_query_meta
		SET revision = ?, updated_at_unix_ms = ?
		WHERE singleton = 1 AND revision = ?
	`, strconv.FormatUint(revision+1, 10), now.UnixMilli(), revisionText)
	if err != nil {
		return mapDurationLedgerSQLiteError("advance CI query revision", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read CI query revision update count: %w", err)
	}
	if affected != 1 {
		return errors.New("CI query revision changed concurrently")
	}
	return nil
}

func boolToSQLite(value bool) int {
	if value {
		return 1
	}
	return 0
}
