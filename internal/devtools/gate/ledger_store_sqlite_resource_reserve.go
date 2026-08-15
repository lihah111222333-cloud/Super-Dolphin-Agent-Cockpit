package gate

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func resolveSQLitePlanningContext(transaction *sql.Tx, generation uint64, planning PlanningContext, ledger DurationLedger) (PlanningContext, []cicontract.WorkloadResourceTierReserve, error) {
	reserves, err := loadSQLiteNormalResourceTierReserves(transaction, generation, planning, ledger.ShardOverhead)
	if err != nil {
		return PlanningContext{}, nil, err
	}
	planning.ResourceTierReserves = append([]cicontract.WorkloadResourceTierReserve(nil), reserves...)
	planning, err = ResolvePlanningContext(planning, ledger)
	return planning, reserves, err
}

// loadSQLiteNormalResourceTierReserves derives normal tier reserves from the
// existing immutable run/shard/timing projections. No new table or migration
// is introduced; missing or unverifiable tier evidence fails closed.
func loadSQLiteNormalResourceTierReserves(transaction *sql.Tx, generation uint64, planning PlanningContext, overhead *ShardOrchestrationOverhead) ([]cicontract.WorkloadResourceTierReserve, error) {
	if planning.Calibration {
		return nil, nil
	}
	if overhead == nil {
		return nil, errors.New("normal resource-tier reserve requires accepted shard overhead identity")
	}
	observations, shards, err := loadSQLiteNormalReserveEvidence(
		transaction, generation, planning, overhead.CalibrationResourceClassID,
	)
	if err != nil {
		return nil, err
	}
	reserves, err := DeriveNormalResourceTierReserves(observations, shards, overhead.CalibrationResourceClassID)
	if err != nil {
		return nil, err
	}
	return reserves, nil
}

// sqliteNormalReserveEvidenceQuery 在 SQLite 内完成 run identity 与 reserve 阶段投影。
const sqliteNormalReserveEvidenceQuery = `
	SELECT runs.job_id, shards.resources_json,
		timing.scope, timing.shard_identity, timing.workload_id, timing.phase,
		timing.started_at_unix_ms, timing.completed_at_unix_ms, timing.duration_ms,
		timing.measurement, timing.reason, timing.aggregation, timing.cache_evidence_json,
		timing.compile_group_id, timing.compile_artifact_key, timing.compile_package_target,
		timing.compile_workload_ids_json, timing.compile_artifact_sha256, timing.compile_artifact_size,
		timing.compile_cache_hits, timing.compile_cache_misses, timing.compile_cache_puts,
		timing.compile_cache_status, timing.compile_status, timing.compile_exit_code,
		timing.compile_error_text, timing.compile_command_digest, timing.compile_profile_digest,
		timing.compile_resource_class_id, timing.compile_resource_cpu,
		timing.compile_resource_memory_gib, timing.compile_execution_mode
	FROM ci_runs AS runs
	JOIN ci_remote_run_execution_identities AS run_identity ON run_identity.job_id = runs.job_id
	JOIN ci_shards AS shards ON shards.job_id = runs.job_id
	JOIN ci_timing_observations AS timing
		ON timing.job_id = shards.job_id AND timing.shard_identity = shards.shard_identity
	WHERE runs.accepted_generation IN (?, ?, ?)
		AND runs.image_cache_snapshot_id = ?
		AND runs.authoritative = 1 AND runs.status = 'passed' AND runs.cleanup_complete = 1
		AND run_identity.platform = ? AND run_identity.runner_identity_digest = ?
		AND run_identity.toolchain_digest = ? AND run_identity.execution_mode = ?
		AND (json_extract(shards.resources_json, '$.class_id') <> ?
			OR json_extract(shards.resources_json, '$.class_id') IS NULL)
		AND ((timing.scope = 'shard' AND timing.phase IN ('total', 'eci_wait', 'source_materialize', 'candidate_compile'))
			OR (timing.scope = 'workload' AND timing.phase = 'total')
			OR (timing.scope = 'compile_group' AND timing.phase = 'test_binary_compile'))`

// loadSQLiteNormalReserveEvidence 以单次集合查询读取当前规划身份的 normal reserve 证据。
func loadSQLiteNormalReserveEvidence(database sqliteRowQueryer, generation uint64, planning PlanningContext, calibrationClassID string) ([]TimingObservation, []RemoteCIShardRecord, error) {
	if generation == 0 || planning.Calibration || planning.AcceptedSnapshotID == "" || calibrationClassID == "" {
		return nil, nil, errors.New("normal resource-tier reserve query identity is incomplete")
	}
	if err := validateDurationEnvironment(planning.Platform, planning.Runner, planning.Toolchain); err != nil {
		return nil, nil, fmt.Errorf("normal resource-tier reserve query environment: %w", err)
	}
	retained := retainedWorkloadPassGenerations(generation)
	rows, err := database.Query(sqliteNormalReserveEvidenceQuery,
		retained[0], retained[1], retained[2], planning.AcceptedSnapshotID,
		planning.Platform, planning.Runner, planning.Toolchain, DurationExecutionModeNormal,
		calibrationClassID)
	if err != nil {
		return nil, nil, mapDurationLedgerSQLiteError("query normal resource-tier reserve evidence", err)
	}
	defer rows.Close()
	return scanSQLiteNormalReserveEvidence(rows)
}

// scanSQLiteNormalReserveEvidence 严格解码集合查询返回的 timing 与资源身份。
func scanSQLiteNormalReserveEvidence(rows *sql.Rows) ([]TimingObservation, []RemoteCIShardRecord, error) {
	observations := make([]TimingObservation, 0)
	shards := make([]RemoteCIShardRecord, 0)
	resourcesByShard := make(map[shardOverheadObservationKey]RemoteCIShardResources)
	for rows.Next() {
		row := normalReserveEvidenceRow{rows: rows}
		observation, resources, err := row.scan()
		if err != nil {
			return nil, nil, err
		}
		key := shardOverheadObservationKey{jobID: observation.JobID, shard: observation.ShardIdentity}
		prior, exists := resourcesByShard[key]
		if exists && prior != resources {
			return nil, nil, fmt.Errorf("normal reserve shard %s/%s has conflicting resources", key.jobID, key.shard)
		}
		if !exists {
			resourcesByShard[key] = resources
			shards = append(shards, RemoteCIShardRecord{ShardIdentity: key.shard, Resources: resources})
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapDurationLedgerSQLiteError("iterate normal resource-tier reserve evidence", err)
	}
	return observations, shards, nil
}

// normalReserveEvidenceRow 把 join 前缀列适配到共享 timing scanner。
type normalReserveEvidenceRow struct {
	rows          *sql.Rows
	jobID         string
	resourcesJSON string
}

// Scan 先消费 job/resource join 列，再消费 timing observation 列。
func (row *normalReserveEvidenceRow) Scan(destinations ...any) error {
	values := make([]any, 0, len(destinations)+2)
	values = append(values, &row.jobID, &row.resourcesJSON)
	return row.rows.Scan(append(values, destinations...)...)
}

// timingObservationJobID 返回当前 join 行所属的权威 job。
func (row *normalReserveEvidenceRow) timingObservationJobID() string { return row.jobID }

// scan 复用严格 timing decoder，并验证 normal 固定资源与编译模式。
func (row *normalReserveEvidenceRow) scan() (TimingObservation, RemoteCIShardResources, error) {
	observation, err := scanTimingObservation(row, "")
	if err != nil {
		return TimingObservation{}, RemoteCIShardResources{}, err
	}
	var resources RemoteCIShardResources
	if err := DecodeStrictJSON([]byte(row.resourcesJSON), &resources); err != nil {
		return TimingObservation{}, RemoteCIShardResources{}, fmt.Errorf("decode normal reserve shard resources: %w", err)
	}
	if _, err := resourceTierForReserve(resources); err != nil {
		return TimingObservation{}, RemoteCIShardResources{}, fmt.Errorf("validate normal reserve shard resources: %w", err)
	}
	if observation.Scope == cicontract.TimingScopeCompileGroup && observation.CompileExecutionMode != DurationExecutionModeNormal {
		return TimingObservation{}, RemoteCIShardResources{}, errors.New("normal reserve compile timing execution mode is not normal")
	}
	return observation, resources, nil
}
