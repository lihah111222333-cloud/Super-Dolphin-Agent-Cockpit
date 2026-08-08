package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const compileTimingIndexQuery = `
	WITH retained_generations AS (
		SELECT DISTINCT runs.accepted_generation
		FROM ci_runs AS runs
		WHERE runs.status = 'passed'
		  AND runs.authoritative = 1
		  AND runs.cleanup_complete = 1
		ORDER BY length(runs.accepted_generation) DESC, runs.accepted_generation DESC
		LIMIT 3
	)
	SELECT observations.package_target, observations.semantic_key,
		observations.platform, observations.runner_identity_digest,
		observations.toolchain_digest, observations.execution_mode,
		observations.resource_class_id, observations.resource_cpu,
		observations.resource_memory_gib, observations.duration_ms,
		observations.started_at_unix_ms, observations.completed_at_unix_ms,
		runs.accepted_generation, runs.job_id
	FROM ci_compile_timing_observations AS observations
	INNER JOIN ci_runs AS runs ON runs.job_id = observations.job_id
	INNER JOIN retained_generations
		ON retained_generations.accepted_generation = runs.accepted_generation
	WHERE observations.measurement = 'measured'
	  AND observations.aggregation = 'raw'
	  AND runs.status = 'passed'
	  AND runs.authoritative = 1
	  AND runs.cleanup_complete = 1
	ORDER BY length(runs.accepted_generation) DESC, runs.accepted_generation DESC,
		observations.package_target, observations.semantic_key,
		observations.platform, observations.runner_identity_digest,
		observations.toolchain_digest, observations.execution_mode,
		observations.resource_class_id, observations.resource_cpu,
		observations.resource_memory_gib, observations.started_at_unix_ms,
		observations.completed_at_unix_ms, runs.job_id
`

// loadSQLiteCompileTimingIndex 只读取 authoritative、passed、cleanup-complete
// 的 measured/raw 行。三代窗口和运行 authority 条件与规划快照在同一
// SQLite 读事务内计算，不读取 JSON 或内存兜底数据。
func loadSQLiteCompileTimingIndex(database sqliteRowQueryer) (CompileTimingIndex, error) {
	rows, err := database.Query(compileTimingIndexQuery)
	if err != nil {
		return CompileTimingIndex{}, mapDurationLedgerSQLiteError("query compile timing history", err)
	}
	defer rows.Close()

	samples := make([]CompileTimingSample, 0)
	for rows.Next() {
		sample, err := scanSQLiteCompileTimingSample(rows)
		if err != nil {
			return CompileTimingIndex{}, err
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return CompileTimingIndex{}, mapDurationLedgerSQLiteError("iterate compile timing history", err)
	}
	return BuildCompileTimingIndex(samples)
}

func scanSQLiteCompileTimingSample(rows *sql.Rows) (CompileTimingSample, error) {
	var (
		identity                               CompileTimingIdentity
		durationMS, startedAtMS, completedAtMS int64
		acceptedGenerationText, jobID          string
	)
	if err := rows.Scan(
		&identity.PackageTarget, &identity.SemanticKey, &identity.Platform,
		&identity.RunnerIdentityDigest, &identity.ToolchainDigest,
		&identity.ExecutionMode, &identity.ResourceClassID, &identity.ResourceCPU,
		&identity.ResourceMemoryGiB, &durationMS, &startedAtMS, &completedAtMS,
		&acceptedGenerationText, &jobID,
	); err != nil {
		return CompileTimingSample{}, mapDurationLedgerSQLiteError("scan compile timing history", err)
	}
	acceptedGeneration, err := strconv.ParseUint(acceptedGenerationText, 10, 64)
	if err != nil || acceptedGeneration == 0 {
		return CompileTimingSample{}, errors.New("stored compile timing accepted generation is invalid")
	}
	sample := CompileTimingSample{
		Identity: identity, DurationMS: durationMS, AcceptedGeneration: acceptedGeneration,
		JobID: jobID, StartedAt: time.UnixMilli(startedAtMS).UTC(),
		CompletedAt: time.UnixMilli(completedAtMS).UTC(),
	}
	if err := sample.Validate(); err != nil {
		return CompileTimingSample{}, fmt.Errorf("validate compile timing history row: %w", err)
	}
	return sample, nil
}

// loadSQLiteCompileTimingObservations 恢复指定 run 的写入侧行，包括
// provisional/failed run。行内不复制 authority，调用方从父 ci_runs 读取。
func loadSQLiteCompileTimingObservations(database sqliteRowQueryer, jobID string) ([]CompileTimingObservation, error) {
	rows, err := database.Query(`
		SELECT package_target, semantic_key, platform, runner_identity_digest,
			toolchain_digest, execution_mode, resource_class_id, resource_cpu,
			resource_memory_gib, duration_ms, started_at_unix_ms,
			completed_at_unix_ms, measurement, aggregation
		FROM ci_compile_timing_observations
		WHERE job_id = ?
		ORDER BY package_target, semantic_key, platform, runner_identity_digest,
			toolchain_digest, execution_mode, resource_class_id, resource_cpu,
			resource_memory_gib, started_at_unix_ms, completed_at_unix_ms
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query compile timing observations", err)
	}
	defer rows.Close()
	observations := make([]CompileTimingObservation, 0)
	for rows.Next() {
		var (
			observation                CompileTimingObservation
			startedAtMS, completedAtMS int64
			measurement, aggregation   string
		)
		if err := rows.Scan(
			&observation.Identity.PackageTarget,
			&observation.Identity.SemanticKey,
			&observation.Identity.Platform,
			&observation.Identity.RunnerIdentityDigest,
			&observation.Identity.ToolchainDigest,
			&observation.Identity.ExecutionMode,
			&observation.Identity.ResourceClassID,
			&observation.Identity.ResourceCPU,
			&observation.Identity.ResourceMemoryGiB,
			&observation.DurationMS,
			&startedAtMS,
			&completedAtMS,
			&measurement,
			&aggregation,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan compile timing observation", err)
		}
		observation.StartedAt = time.UnixMilli(startedAtMS).UTC()
		observation.CompletedAt = time.UnixMilli(completedAtMS).UTC()
		observation.Measurement = cicontract.ObservationState(measurement)
		observation.Aggregation = cicontract.TimingAggregation(aggregation)
		if err := observation.Validate(); err != nil {
			return nil, fmt.Errorf("validate compile timing observation: %w", err)
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate compile timing observations", err)
	}
	return observations, nil
}
