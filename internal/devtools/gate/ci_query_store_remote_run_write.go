package gate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// storeSQLiteRemoteCIRunProjection 按固定顺序写入全部查询投影。
func storeSQLiteRemoteCIRunProjection(
	transaction *sql.Tx,
	record RemoteCIRunRecord,
	now func() time.Time,
) error {
	if err := writeSQLiteRemoteCIRunCoreProjection(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteCIRunChildProjections(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteRunWarnings(transaction, record.JobID, record.Warnings); err != nil {
		return err
	}
	if err := finalizeSQLiteRemoteCITimingWarnings(transaction, record); err != nil {
		return err
	}
	if err := advanceCIQueryRevision(transaction, now().UTC()); err != nil {
		return err
	}
	return nil
}

// replaceSQLiteRemoteCIRunChildProjections 按固定顺序替换 run 的全部关联投影。
func replaceSQLiteRemoteCIRunChildProjections(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if err := replaceSQLiteRemoteCIRunShards(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteCIRunExecutions(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteCIWorkloadExecutions(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteCIWorkloadResults(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteTimingObservations(transaction, record.JobID, record.TimingObservations); err != nil {
		return err
	}
	if err := replaceSQLiteCompileTimingObservations(transaction, record); err != nil {
		return err
	}
	return nil
}

// writeSQLiteRemoteCIRunCoreProjection 先写入运行记录和 agent 身份，再在同一事务内回读校验。
func writeSQLiteRemoteCIRunCoreProjection(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if err := validateSQLiteRemoteCIRunCatalogCoverage(transaction, record); err != nil {
		return err
	}
	if err := verifySQLiteRemoteCIRunIdentity(transaction, record); err != nil {
		return err
	}
	if err := upsertSQLiteRemoteCIRun(transaction, record); err != nil {
		return err
	}
	if err := insertSQLiteRemoteCIRunAgentIdentity(transaction, record); err != nil {
		return err
	}
	if err := insertSQLiteRemoteCIExecutionScope(transaction, record); err != nil {
		return err
	}
	if err := verifySQLiteRemoteCIRunIdentity(transaction, record); err != nil {
		return fmt.Errorf("read back remote CI agent identity: %w", err)
	}
	return nil
}

func upsertSQLiteRemoteCIRun(transaction *sql.Tx, record RemoteCIRunRecord) error {
	cleanupComplete := boolToSQLite(record.CleanupComplete)
	if _, err := transaction.Exec(`
		INSERT INTO ci_runs (
			job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha,
			candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status,
			authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			status = excluded.status,
			completed_at_unix_ms = excluded.completed_at_unix_ms,
			cleanup_complete = excluded.cleanup_complete,
			error_text = excluded.error_text
	`, record.JobID, boolToSQLite(record.Force), string(record.Entrypoint), string(record.Profile), record.PlanDigest,
		record.CatalogDigest, strconv.FormatUint(record.AcceptedGeneration, 10), record.ImageCacheSnapshotID, record.SourceTreeSHA, record.CandidateGateSourceSHA256, record.CandidateGateToolchainSHA256,
		record.RunnerImage, string(record.Status),
		0, record.StartedAt.UTC().UnixMilli(), record.CompletedAt.UTC().UnixMilli(),
		cleanupComplete, record.ErrorText,
	); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI run", err)
	}
	return nil
}

func insertSQLiteRemoteCIRunAgentIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := transaction.Exec(`
		INSERT INTO ci_run_agent_identities (
			job_id, agent_token_digest, started_at_unix_ms
		) VALUES (?, ?, ?)
		ON CONFLICT(job_id) DO NOTHING
	`, record.JobID, record.AgentTokenDigest, record.StartedAt.UTC().UnixMilli()); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI agent identity", err)
	}
	return nil
}

func replaceSQLiteRemoteCIRunShards(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := transaction.Exec(`DELETE FROM ci_shards WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI shards", err)
	}
	for _, shard := range record.Shards {
		if err := insertSQLiteRemoteCIRunShard(transaction, record, shard); err != nil {
			return err
		}
	}
	return nil
}

// insertSQLiteRemoteCIRunShard 写入分片及其工作负载关联。
func insertSQLiteRemoteCIRunShard(transaction *sql.Tx, record RemoteCIRunRecord, shard RemoteCIShardRecord) error {
	if err := validateRemoteCIShardForWrite(record, shard); err != nil {
		return err
	}
	timing, err := json.Marshal(shard.MaterializationTiming)
	if err != nil {
		return fmt.Errorf("encode remote CI shard materialization timing: %w", err)
	}
	timingJSON := string(timing)
	resourcesJSON := ""
	if shard.Resources.CPU != 0 || shard.Resources.MemoryGiB != 0 || shard.Resources.ClassID != "" {
		resources, err := json.Marshal(shard.Resources)
		if err != nil {
			return fmt.Errorf("encode remote CI shard resources: %w", err)
		}
		resourcesJSON = string(resources)
	}
	if _, err := transaction.Exec(`
		INSERT INTO ci_shards (
			job_id, shard_identity, container_group_id, container_status, materialization_timing_json, resources_json
		) VALUES (?, ?, ?, ?, ?, ?)
	`, record.JobID, shard.ShardIdentity, shard.ContainerGroup, shard.ContainerStatus, timingJSON, resourcesJSON); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI shard", err)
	}
	if err := insertSQLiteRemoteCIShardTerminalEvidence(transaction, record, shard); err != nil {
		return err
	}
	if err := insertSQLiteRemoteCIShardWorkloads(transaction, record.JobID, shard); err != nil {
		return err
	}
	return nil
}

// insertSQLiteRemoteCIShardWorkloads 写入分片的工作负载关联并保留外键校验。
func insertSQLiteRemoteCIShardWorkloads(transaction *sql.Tx, jobID string, shard RemoteCIShardRecord) error {
	for _, workloadID := range shard.Workloads {
		if _, err := transaction.Exec(`
			INSERT INTO ci_shard_workloads (
				job_id, shard_identity, workload_id
			) VALUES (?, ?, ?)
		`, jobID, shard.ShardIdentity, string(workloadID)); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI shard workload", err)
		}
	}
	return nil
}

// validateRemoteCIShardForWrite 统一写入前的物化和资源证据校验，保持读写规则一致。
func validateRemoteCIShardForWrite(record RemoteCIRunRecord, shard RemoteCIShardRecord) error {
	if err := validateRemoteCIShardMaterializationTiming(record.Status, record.Authoritative, shard); err != nil {
		return fmt.Errorf("validate remote CI shard materialization timing: %w", err)
	}
	if remoteCIShardWasNotCreated(shard) {
		return nil
	}
	return validateRemoteCIShardResources(record.Status, record.Authoritative, shard)
}

// replaceSQLiteRemoteCIRunExecutions 原子替换该任务的 gate 执行记录。
func replaceSQLiteRemoteCIRunExecutions(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := transaction.Exec(`DELETE FROM ci_gate_executions WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI gate executions", err)
	}
	for _, execution := range record.Executions {
		profile, err := json.Marshal(execution.ExecutionProfile)
		if err != nil {
			return fmt.Errorf("encode remote CI execution profile: %w", err)
		}
		testTimings, err := json.Marshal(execution.TestTimings)
		if err != nil {
			return fmt.Errorf("encode remote CI execution test timings: %w", err)
		}
		if _, err := transaction.Exec(`
			INSERT INTO ci_gate_executions (
				job_id, workload_id, status, exit_code, started_at_unix_ms,
				completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.JobID, string(execution.GateID), string(execution.Status), execution.ExitCode,
			execution.StartedAt.UTC().UnixMilli(), execution.CompletedAt.UTC().UnixMilli(),
			execution.ArgvDigest, execution.LogDigest, string(testTimings), string(profile),
		); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI gate execution", err)
		}
	}
	return nil
}

// replaceSQLiteRemoteCIWorkloadExecutions 校验分片绑定后替换 workload 执行记录。
func replaceSQLiteRemoteCIWorkloadExecutions(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := transaction.Exec(`DELETE FROM ci_workload_executions WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI workload executions", err)
	}
	for _, execution := range record.WorkloadExecutions {
		shardIdentity, err := remoteCIRunWorkloadShardIdentity(record.Shards, execution.GateID)
		if err != nil {
			return err
		}
		if execution.ShardIdentity != shardIdentity {
			return fmt.Errorf("remote CI workload execution %q shard binding is invalid", execution.GateID)
		}
		profile, err := json.Marshal(execution.ExecutionProfile)
		if err != nil {
			return fmt.Errorf("encode remote CI workload execution profile: %w", err)
		}
		testTimings, err := json.Marshal(execution.TestTimings)
		if err != nil {
			return fmt.Errorf("encode remote CI workload execution test timings: %w", err)
		}
		if _, err := transaction.Exec(`
			INSERT INTO ci_workload_executions (
				job_id, shard_identity, workload_id, status, exit_code, started_at_unix_ms,
				completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.JobID, shardIdentity, string(execution.GateID), string(execution.Status), execution.ExitCode,
			execution.StartedAt.UTC().UnixMilli(), execution.CompletedAt.UTC().UnixMilli(),
			execution.ArgvDigest, execution.LogDigest, string(testTimings), string(profile),
		); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI workload execution", err)
		}
	}
	return nil
}

func remoteCIRunWorkloadShardIdentity(shards []RemoteCIShardRecord, workloadID GateID) (string, error) {
	for _, shard := range shards {
		if slices.Contains(shard.Workloads, workloadID) {
			return shard.ShardIdentity, nil
		}
	}
	return "", fmt.Errorf("remote CI workload %q lacks shard identity", workloadID)
}

// replaceSQLiteTimingObservations 原子替换远程运行的阶段计时及编译观测，拒绝超出 SQLite 范围的计数。
func replaceSQLiteTimingObservations(transaction *sql.Tx, jobID string, observations []TimingObservation) error {
	if _, err := transaction.Exec(`DELETE FROM ci_timing_observations WHERE job_id = ?`, jobID); err != nil {
		return mapDurationLedgerSQLiteError("clear timing observations", err)
	}
	for _, observation := range observations {
		cacheEvidenceJSON, err := json.Marshal(observation.CacheEvidence)
		if err != nil {
			return fmt.Errorf("encode timing cache evidence: %w", err)
		}
		compileWorkloadIDsJSON, err := json.Marshal(append([]GateID{}, observation.CompileWorkloadIDs...))
		if err != nil {
			return fmt.Errorf("encode compile group workload IDs: %w", err)
		}
		maxSQLiteInteger := uint64(^uint64(0) >> 1)
		if observation.CompileCacheHits > maxSQLiteInteger || observation.CompileCacheMisses > maxSQLiteInteger || observation.CompileCachePuts > maxSQLiteInteger {
			return errors.New("compile group cache counter exceeds SQLite integer range")
		}
		if _, err := transaction.Exec(`INSERT INTO ci_timing_observations (
			job_id, scope, shard_identity, workload_id, phase, started_at_unix_ms, completed_at_unix_ms, duration_ms,
			measurement, reason, aggregation, cache_evidence_json, compile_group_id, compile_artifact_key,
			compile_package_target, compile_workload_ids_json, compile_artifact_sha256, compile_artifact_size,
			compile_cache_hits, compile_cache_misses, compile_cache_puts, compile_cache_status, compile_status,
			compile_exit_code, compile_error_text, compile_command_digest, compile_profile_digest,
			compile_resource_class_id, compile_resource_cpu, compile_resource_memory_gib, compile_execution_mode
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			observation.JobID, string(observation.Scope), observation.ShardIdentity, string(observation.WorkloadID), string(observation.Phase),
			timingObservationUnixMillis(observation.StartedAt), timingObservationUnixMillis(observation.CompletedAt), observation.DurationMS,
			string(observation.Measurement), observation.Reason, string(observation.Aggregation), string(cacheEvidenceJSON), observation.CompileGroupID,
			observation.CompileArtifactKey, observation.CompilePackageTarget, string(compileWorkloadIDsJSON), observation.CompileArtifactSHA256,
			observation.CompileArtifactSize, int64(observation.CompileCacheHits), int64(observation.CompileCacheMisses), int64(observation.CompileCachePuts),
			observation.CompileCacheStatus, observation.CompileStatus, observation.CompileExitCode, observation.CompileErrorText,
			observation.CompileCommandDigest, observation.CompileProfileDigest, observation.CompileResourceClassID, observation.CompileResourceCPU,
			observation.CompileResourceMemoryGiB, observation.CompileExecutionMode); err != nil {
			return mapDurationLedgerSQLiteError("store timing observation", err)
		}
	}
	return nil
}

// timingObservationUnixMillis 将零值时间编码为 SQLite 零，避免 not_applicable 回读成负 epoch。
func timingObservationUnixMillis(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}
