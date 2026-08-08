package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// replaceSQLiteCompileTimingObservations 原子替换一个 run 的 compile 观测。
// generation、状态与 authority 只从同一事务内的 ci_runs 读取，避免调用方
// 携带可漂移的重复字段写出矛盾行。
func replaceSQLiteCompileTimingObservations(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if err := validateCompileTimingRunContext(transaction, record); err != nil {
		return err
	}
	if _, err := transaction.Exec(`DELETE FROM ci_compile_timing_observations WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear compile timing observations", err)
	}
	for _, observation := range record.CompileTimingObservations {
		if err := insertSQLiteCompileTimingObservation(transaction, record.JobID, observation); err != nil {
			return err
		}
	}
	return nil
}

// validateCompileTimingRunContext 将写入请求绑定到同一事务中的 ci_runs 行。
func validateCompileTimingRunContext(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if err := validateCompileTimingRecordIdentity(record); err != nil {
		return err
	}
	var acceptedGenerationText, status string
	var authoritative, cleanupComplete int
	if err := transaction.QueryRow(`
		SELECT accepted_generation, status, authoritative, cleanup_complete
		FROM ci_runs WHERE job_id = ?
	`, record.JobID).Scan(&acceptedGenerationText, &status, &authoritative, &cleanupComplete); err != nil {
		return mapDurationLedgerSQLiteError("load compile timing run authority", err)
	}
	return validateCompileTimingStoredRun(record.AcceptedGeneration, acceptedGenerationText, status, authoritative, cleanupComplete)
}

func validateCompileTimingRecordIdentity(record RemoteCIRunRecord) error {
	if record.JobID == "" {
		return errors.New("compile timing run job ID is required")
	}
	if record.AcceptedGeneration == 0 {
		return errors.New("compile timing run accepted generation is required")
	}
	return nil
}

// validateCompileTimingStoredRun 校验 SQLite run 的 generation 与 authority 标志。
func validateCompileTimingStoredRun(expectedGeneration uint64, acceptedGenerationText, status string, authoritative, cleanupComplete int) error {
	acceptedGeneration, err := strconv.ParseUint(acceptedGenerationText, 10, 64)
	if err != nil || acceptedGeneration == 0 || acceptedGeneration != expectedGeneration {
		return errors.New("compile timing run accepted generation does not match ci_runs")
	}
	if authoritative != 0 && authoritative != 1 || cleanupComplete != 0 && cleanupComplete != 1 {
		return errors.New("compile timing run authority flags are invalid")
	}
	if status == "" {
		return errors.New("compile timing run status is missing")
	}
	return nil
}

func insertSQLiteCompileTimingObservation(transaction *sql.Tx, jobID string, observation CompileTimingObservation) error {
	if err := observation.Validate(); err != nil {
		return fmt.Errorf("validate compile timing observation: %w", err)
	}
	startedAtMS := observation.StartedAt.UTC().UnixMilli()
	completedAtMS := observation.CompletedAt.UTC().UnixMilli()
	if startedAtMS <= 0 || completedAtMS <= startedAtMS {
		return errors.New("compile timing observation timestamps are outside SQLite range")
	}
	if _, err := transaction.Exec(`
		INSERT INTO ci_compile_timing_observations (
			job_id, package_target, semantic_key, platform, runner_identity_digest,
			toolchain_digest, execution_mode, resource_class_id, resource_cpu,
			resource_memory_gib, duration_ms, started_at_unix_ms,
			completed_at_unix_ms, measurement, aggregation
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, jobID, observation.Identity.PackageTarget, observation.Identity.SemanticKey,
		observation.Identity.Platform, observation.Identity.RunnerIdentityDigest,
		observation.Identity.ToolchainDigest, observation.Identity.ExecutionMode,
		observation.Identity.ResourceClassID, observation.Identity.ResourceCPU,
		observation.Identity.ResourceMemoryGiB, observation.DurationMS, startedAtMS,
		completedAtMS, string(observation.Measurement), string(observation.Aggregation)); err != nil {
		return mapDurationLedgerSQLiteError("store compile timing observation", err)
	}
	return nil
}
