package gate

import (
	"database/sql"
	"encoding/json"
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
	if err := validateSQLiteRemoteCIRunCatalogCoverage(transaction, record); err != nil {
		return err
	}
	if err := verifySQLiteRemoteCIRunIdentity(transaction, record); err != nil {
		return err
	}
	if err := upsertSQLiteRemoteCIRun(transaction, record); err != nil {
		return err
	}
	if err := insertSQLiteRemoteCIRunRequester(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteCIRunShards(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteCIRunExecutions(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteCIWorkloadExecutions(transaction, record); err != nil {
		return err
	}
	if err := replaceSQLiteTimingObservations(transaction, record.JobID, record.TimingObservations); err != nil {
		return err
	}
	if err := replaceSQLiteRemoteRunWarnings(transaction, record.JobID, record.Warnings); err != nil {
		return err
	}
	if err := advanceCIQueryRevision(transaction, now().UTC()); err != nil {
		return err
	}
	return nil
}

func upsertSQLiteRemoteCIRun(transaction *sql.Tx, record RemoteCIRunRecord) error {
	authoritative := boolToSQLite(record.Authoritative)
	cleanupComplete := boolToSQLite(record.CleanupComplete)
	if _, err := transaction.Exec(`
		INSERT INTO ci_runs (
			job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, source_tree_sha,
			candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status,
			authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			status = excluded.status,
			authoritative = excluded.authoritative,
			completed_at_unix_ms = excluded.completed_at_unix_ms,
			cleanup_complete = excluded.cleanup_complete,
			error_text = excluded.error_text
	`, record.JobID, string(record.Entrypoint), string(record.Profile), record.PlanDigest,
		record.CatalogDigest, strconv.FormatUint(record.AcceptedGeneration, 10), record.SourceTreeSHA, record.CandidateGateSourceSHA256, record.CandidateGateToolchainSHA256,
		record.RunnerImage, string(record.Status),
		authoritative, record.StartedAt.UTC().UnixMilli(), record.CompletedAt.UTC().UnixMilli(),
		cleanupComplete, record.ErrorText,
	); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI run", err)
	}
	return nil
}

func insertSQLiteRemoteCIRunRequester(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if record.RequesterFingerprint == "" {
		return nil
	}
	if _, err := transaction.Exec(`
		INSERT INTO ci_run_requesters (
			job_id, requester_fingerprint, started_at_unix_ms
		) VALUES (?, ?, ?)
		ON CONFLICT(job_id) DO NOTHING
	`, record.JobID, record.RequesterFingerprint.String(), record.StartedAt.UTC().UnixMilli()); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI requester", err)
	}
	return nil
}

func replaceSQLiteRemoteCIRunShards(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := transaction.Exec(`DELETE FROM ci_shards WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI shards", err)
	}
	for _, shard := range record.Shards {
		if err := insertSQLiteRemoteCIRunShard(transaction, record.JobID, shard); err != nil {
			return err
		}
	}
	return nil
}

func insertSQLiteRemoteCIRunShard(transaction *sql.Tx, jobID string, shard RemoteCIShardRecord) error {
	if err := validateRemoteCIShardMaterializationTiming(shard); err != nil {
		return fmt.Errorf("validate remote CI shard materialization timing: %w", err)
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
	`, jobID, shard.ShardIdentity, shard.ContainerGroup, shard.ContainerStatus, timingJSON, resourcesJSON); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI shard", err)
	}
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

func replaceSQLiteTimingObservations(transaction *sql.Tx, jobID string, observations []TimingObservation) error {
	if _, err := transaction.Exec(`DELETE FROM ci_timing_observations WHERE job_id = ?`, jobID); err != nil {
		return mapDurationLedgerSQLiteError("clear timing observations", err)
	}
	for _, observation := range observations {
		cacheEvidenceJSON, err := json.Marshal(observation.CacheEvidence)
		if err != nil {
			return fmt.Errorf("encode timing cache evidence: %w", err)
		}
		if _, err := transaction.Exec(`INSERT INTO ci_timing_observations (job_id, scope, shard_identity, workload_id, phase, started_at_unix_ms, completed_at_unix_ms, duration_ms, measurement, reason, aggregation, cache_evidence_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, observation.JobID, string(observation.Scope), observation.ShardIdentity, string(observation.WorkloadID), string(observation.Phase), observation.StartedAt.UTC().UnixMilli(), observation.CompletedAt.UTC().UnixMilli(), observation.DurationMS, string(observation.Measurement), observation.Reason, string(observation.Aggregation), string(cacheEvidenceJSON)); err != nil {
			return mapDurationLedgerSQLiteError("store timing observation", err)
		}
	}
	return nil
}
