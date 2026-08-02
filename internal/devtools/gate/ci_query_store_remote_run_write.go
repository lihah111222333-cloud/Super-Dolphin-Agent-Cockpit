package gate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// storeSQLiteRemoteCIRunProjection 按固定顺序写入全部查询投影并记录每一步耗时。
func storeSQLiteRemoteCIRunProjection(
	transaction *sql.Tx,
	record RemoteCIRunRecord,
	now func() time.Time,
) ([]RemoteCIPhaseTiming, error) {
	initialTimingCount := len(record.PhaseTimings)
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.catalog_coverage",
		func() error { return validateSQLiteRemoteCIRunCatalogCoverage(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.identity_verify",
		func() error { return verifySQLiteRemoteCIRunIdentity(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.run_upsert",
		func() error { return upsertSQLiteRemoteCIRun(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := replaceCandidateTestBinaryBuilds(transaction, record); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.requester_insert",
		func() error { return insertSQLiteRemoteCIRunRequester(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.shards_replace",
		func() error { return replaceSQLiteRemoteCIRunShards(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.executions_replace",
		func() error { return replaceSQLiteRemoteCIRunExecutions(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.workload_executions_replace",
		func() error { return replaceSQLiteRemoteCIWorkloadExecutions(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.workloads_replace",
		func() error { return replaceSQLiteRemoteRunWorkloads(transaction, record) },
	); err != nil {
		return nil, err
	}
	if err := measureSQLiteRemoteCIRunProjection(
		&record,
		now,
		"ledger.warnings_replace",
		func() error {
			return replaceSQLiteRemoteRunWarnings(transaction, record.JobID, record.Warnings)
		},
	); err != nil {
		return nil, err
	}
	if err := replaceSQLiteRemoteRunPhaseTimings(transaction, record.JobID, record.PhaseTimings); err != nil {
		return nil, err
	}
	if err := advanceCIQueryRevision(transaction, now().UTC()); err != nil {
		return nil, err
	}
	return append([]RemoteCIPhaseTiming(nil), record.PhaseTimings[initialTimingCount:]...), nil
}

func replaceCandidateTestBinaryBuilds(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if _, err := transaction.Exec(`DELETE FROM ci_candidate_test_binary_builds WHERE job_id=?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear candidate test binary builds", err)
	}
	for _, build := range record.CandidateTestBinaryBuilds {
		if err := validateCandidateTestBinaryBuildRecord(build); err != nil {
			return err
		}
		flags, err := json.Marshal(build.BuildFlags)
		if err != nil {
			return err
		}
		if _, err = transaction.Exec(`INSERT INTO ci_candidate_test_binary_builds (job_id,candidate_tree,package,mode,platform,go_toolchain,cgo_enabled,toolchain_sha256,build_flags_json,compile_closure_sha256,manifest_sha256,artifact_sha256,binary_size,go_list_wall_ms,build_wall_ms,compile_action_ms,link_action_ms,compile_critical_wall_ms,gocache_private_hits,gocache_oci_project_cache_hits,gocache_private_root_identity,gocache_misses,gocache_puts) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, record.JobID, build.CandidateTree, build.Package, build.Mode, build.Platform, build.GoToolchain, boolToSQLite(build.CGOEnabled), build.ToolchainSHA256, string(flags), build.CompileClosureSHA256, build.ManifestSHA256, build.ArtifactSHA256, build.BinarySize, build.GoListWallMS, build.BuildWallMS, build.CompileActionMS, build.LinkActionMS, build.CompileCriticalWallMS, build.GOCachePrivateHits, build.GOCacheOCIProjectCacheHits, build.GOCachePrivateRootIdentity, build.GOCacheMisses, build.GOCachePuts); err != nil {
			return mapDurationLedgerSQLiteError("store candidate test binary build", err)
		}
	}
	return nil
}

func measureSQLiteRemoteCIRunProjection(
	record *RemoteCIRunRecord,
	now func() time.Time,
	phase string,
	operation func() error,
) error {
	startedAt := now()
	err := operation()
	completedAt := now()
	duration := max(completedAt.Sub(startedAt), 0)
	outcome := RemoteCIPhaseOutcomeSucceeded
	if err != nil {
		outcome = RemoteCIPhaseOutcomeFailed
	}
	record.PhaseTimings = append(record.PhaseTimings, RemoteCIPhaseTiming{
		Phase:          phase,
		StartedAt:      startedAt.UTC(),
		DurationMillis: duration.Milliseconds(),
		Outcome:        outcome,
		WorkloadCount:  len(record.ReusedWorkloads) + len(record.CacheMisses),
		ShardCount:     len(record.Shards),
		CacheHitCount:  len(record.ReusedWorkloads),
		CacheMissCount: len(record.CacheMisses),
	})
	return err
}

func upsertSQLiteRemoteCIRun(transaction *sql.Tx, record RemoteCIRunRecord) error {
	authoritative := boolToSQLite(record.Authoritative)
	cleanupComplete := boolToSQLite(record.CleanupComplete)
	if _, err := transaction.Exec(`
		INSERT INTO ci_runs (
			job_id, entrypoint, profile, plan_digest, catalog_digest, source_tree_sha,
			candidate_cli_manifest_sha256, candidate_test_binary_receipt_binding_digest, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms,
			cleanup_complete, error_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id) DO UPDATE SET
			status = excluded.status,
			authoritative = excluded.authoritative,
			candidate_cli_manifest_sha256 = excluded.candidate_cli_manifest_sha256,
			candidate_test_binary_receipt_binding_digest = excluded.candidate_test_binary_receipt_binding_digest,
			completed_at_unix_ms = excluded.completed_at_unix_ms,
			cleanup_complete = excluded.cleanup_complete,
			error_text = excluded.error_text
	`, record.JobID, string(record.Entrypoint), string(record.Profile), record.PlanDigest,
		record.CatalogDigest, record.SourceTreeSHA, record.CandidateCLIManifestSHA256, record.CandidateTestBinaryReceiptBindingDigest, record.RunnerImage, string(record.Status),
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
	if _, err := transaction.Exec(`
		INSERT INTO ci_shards (
			job_id, shard_identity, container_group_id, container_status, materialization_timing_json
		) VALUES (?, ?, ?, ?, ?)
	`, jobID, shard.ShardIdentity, shard.ContainerGroup, shard.ContainerStatus, timingJSON); err != nil {
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
				job_id, workload_id, status, exit_code, started_at_unix_ms,
				completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, record.JobID, string(execution.GateID), string(execution.Status), execution.ExitCode,
			execution.StartedAt.UTC().UnixMilli(), execution.CompletedAt.UTC().UnixMilli(),
			execution.ArgvDigest, execution.LogDigest, string(testTimings), string(profile),
		); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI workload execution", err)
		}
	}
	return nil
}

func replaceSQLiteRemoteRunPhaseTimings(
	transaction *sql.Tx,
	jobID string,
	timings []RemoteCIPhaseTiming,
) error {
	if _, err := transaction.Exec(`DELETE FROM ci_run_phase_timings WHERE job_id = ?`, jobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI phase timings", err)
	}
	for ordinal, timing := range timings {
		if _, err := transaction.Exec(`
			INSERT INTO ci_run_phase_timings (
				job_id, ordinal, phase, started_at_unix_ms, duration_ms, outcome,
				workload_count, shard_count, cache_hit_count, cache_miss_count
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			jobID,
			ordinal,
			timing.Phase,
			timing.StartedAt.UTC().UnixMilli(),
			timing.DurationMillis,
			string(timing.Outcome),
			timing.WorkloadCount,
			timing.ShardCount,
			timing.CacheHitCount,
			timing.CacheMissCount,
		); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI phase timing", err)
		}
	}
	return nil
}
