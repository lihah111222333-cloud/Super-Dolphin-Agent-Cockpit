package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// loadSQLiteMetadata 读取并校验账本元数据。
func loadSQLiteMetadata(database sqliteRowQueryer) (DurationLedgerSnapshot, error) {
	var (
		generationText string
		version        int
		schemaVersion  int
		authorityID    string
	)
	err := database.QueryRow(`
		SELECT authority_id, schema_version, generation, ledger_version
		FROM duration_ledger_meta
		WHERE singleton = 1
	`).Scan(&authorityID, &schemaVersion, &generationText, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return DurationLedgerSnapshot{}, ErrDurationLedgerMetadataMissing
	}
	if err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError("load duration ledger SQLite metadata", err)
	}
	if authorityID != cicontract.SQLAuthorityID {
		return DurationLedgerSnapshot{}, fmt.Errorf(
			"duration ledger SQLite authority ID %q must equal %q",
			authorityID,
			cicontract.SQLAuthorityID,
		)
	}
	if schemaVersion != 1 {
		return DurationLedgerSnapshot{}, fmt.Errorf(
			"duration ledger SQLite schema version %d is unsupported",
			schemaVersion,
		)
	}
	generation, err := strconv.ParseUint(generationText, 10, 64)
	if err != nil || generation == 0 {
		return DurationLedgerSnapshot{}, errors.New("duration ledger SQLite generation is invalid")
	}
	ledger := DurationLedger{Version: version}
	calibration, err := loadSQLiteCalibration(database)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	ledger.Calibration = calibration
	if ledger.Version != durationLedgerVersion {
		return DurationLedgerSnapshot{}, fmt.Errorf(
			"duration ledger version must equal %d",
			durationLedgerVersion,
		)
	}
	return DurationLedgerSnapshot{Generation: generation, Ledger: ledger}, nil
}

// loadSQLiteDurationSamples 读取所有样本并按 accepted baseline 窗口逐行校验。
func loadSQLiteDurationSamples(database sqliteRowQueryer, currentAcceptedGeneration uint64) ([]DurationSample, error) {
	rows, err := database.Query(`
		SELECT accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain,
			execution_mode, resource_class_id, resource_cpu, resource_memory_gib,
			succeeded, duration_ms, target_kind, parent_workload_id,
			parent_command_digest, target_name, target_status
		FROM duration_samples
		ORDER BY id
	`)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query duration ledger SQLite samples", err)
	}
	defer rows.Close()

	samples := make([]DurationSample, 0)
	for rows.Next() {
		sample, err := scanSQLiteDurationSample(rows)
		if err != nil {
			return nil, err
		}
		if err := validateSQLiteDurationSampleGeneration(sample.AcceptedGeneration, currentAcceptedGeneration); err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate duration ledger SQLite samples", err)
	}
	return samples, nil
}

// loadSQLiteDurationSampleIndex 读取规划所需的样本索引。
func loadSQLiteDurationSampleIndex(
	database sqliteRowQueryer,
	planning PlanningContext,
	currentAcceptedGeneration uint64,
) (DurationSampleIndex, error) {
	index := DurationSampleIndex{
		context: planning,
		buckets: make(map[durationSampleIndexKey]durationSampleAggregate),
	}
	query := `
		SELECT accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain,
			execution_mode, resource_class_id, resource_cpu, resource_memory_gib,
			succeeded, duration_ms, target_kind, parent_workload_id, parent_command_digest,
			target_name, target_status
		FROM duration_samples
		WHERE execution_mode = ? AND platform = ? AND runner = ? AND toolchain = ?
		ORDER BY accepted_generation DESC, workload_id, command_digest, input_digest,
			resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms, id
	`
	args := []any{DurationExecutionModeNormal, planning.Platform, planning.Runner, planning.Toolchain}
	if planning.Calibration {
		query = `
			SELECT accepted_generation, workload_id, command_digest, input_digest, platform, runner, toolchain,
				execution_mode, resource_class_id, resource_cpu, resource_memory_gib,
				succeeded, duration_ms, target_kind, parent_workload_id, parent_command_digest,
				target_name, target_status
			FROM duration_samples
			WHERE execution_mode = ? AND resource_class_id = ? AND resource_cpu = ? AND resource_memory_gib = ?
				AND platform = ? AND runner = ? AND toolchain = ?
			ORDER BY accepted_generation DESC, workload_id, command_digest, input_digest,
				resource_class_id, resource_cpu, resource_memory_gib, succeeded, duration_ms, id
		`
		args = []any{DurationExecutionModeCalibration, planning.CalibrationResourceClassID, planning.CalibrationResourceCPU, planning.CalibrationResourceMemoryGiB, planning.Platform, planning.Runner, planning.Toolchain}
	}
	rows, err := database.Query(query, args...)
	if err != nil {
		return DurationSampleIndex{}, mapDurationLedgerSQLiteError("query duration sample planning index", err)
	}
	defer rows.Close()
	if err := appendSQLiteDurationIndexRows(&index, rows, planning, currentAcceptedGeneration); err != nil {
		return DurationSampleIndex{}, err
	}
	if err := index.finalizeGenerationWindow(); err != nil {
		return DurationSampleIndex{}, err
	}
	return index, nil
}

// appendSQLiteDurationIndexRows 将权威 SQLite 原始行校验后写入时长索引。
func appendSQLiteDurationIndexRows(index *DurationSampleIndex, rows *sql.Rows, planning PlanningContext, currentAcceptedGeneration uint64) error {
	for rows.Next() {
		sample, err := scanSQLiteDurationSample(rows)
		if err != nil {
			return err
		}
		if err := validateSQLiteDurationSampleGeneration(sample.AcceptedGeneration, currentAcceptedGeneration); err != nil {
			return err
		}
		if err := ValidateDurationLedger(DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{sample}}); err != nil {
			return fmt.Errorf("validate duration sample planning row: %w", err)
		}
		if sample.Bucket.Platform != planning.Platform || sample.Bucket.Runner != planning.Runner || sample.Bucket.Toolchain != planning.Toolchain {
			return errors.New("duration sample planning query returned an environment mismatch")
		}
		if err := index.addSample(sample, mathMaxInt64); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate duration sample planning index", err)
	}
	return nil
}

func validateSQLiteDurationSampleGeneration(acceptedGeneration, currentAcceptedGeneration uint64) error {
	if currentAcceptedGeneration == 0 {
		return errors.New("duration ledger SQLite current generation is invalid")
	}
	window := cicontract.RetentionGenerationWindow(currentAcceptedGeneration)
	if acceptedGeneration > currentAcceptedGeneration {
		return fmt.Errorf("duration sample generation %d was never accepted; current accepted baseline generation is %d", acceptedGeneration, currentAcceptedGeneration)
	}
	oldest := window[len(window)-1]
	if acceptedGeneration < oldest {
		return fmt.Errorf("duration sample generation %d is older than accepted baseline retained window [%d,%d]", acceptedGeneration, oldest, currentAcceptedGeneration)
	}
	return nil
}

// scanSQLiteDurationSample 读取一行权威样本并严格解析 accepted_generation。
func scanSQLiteDurationSample(scanner interface{ Scan(...any) error }) (DurationSample, error) {
	var (
		sample                         DurationSample
		succeeded                      int
		resourceCPU, resourceMemory    float64
		executionMode, resourceClassID string
		inputDigest                    string
		targetKind                     string
		targetStatus                   string
		acceptedGenerationText         string
	)
	if err := scanner.Scan(
		&acceptedGenerationText,
		&sample.Bucket.WorkloadID,
		&sample.Bucket.CommandDigest,
		&inputDigest,
		&sample.Bucket.Platform,
		&sample.Bucket.Runner,
		&sample.Bucket.Toolchain,
		&executionMode,
		&resourceClassID,
		&resourceCPU,
		&resourceMemory,
		&succeeded,
		&sample.DurationMS,
		&targetKind,
		&sample.ParentWorkloadID,
		&sample.ParentCommandDigest,
		&sample.TargetName,
		&targetStatus,
	); err != nil {
		return DurationSample{}, mapDurationLedgerSQLiteError("scan duration ledger SQLite sample", err)
	}
	acceptedGeneration, err := strconv.ParseUint(acceptedGenerationText, 10, 64)
	if err != nil || acceptedGeneration == 0 {
		return DurationSample{}, errors.New("stored duration sample accepted generation is invalid")
	}
	sample.AcceptedGeneration = acceptedGeneration
	sample.Succeeded = succeeded == 1
	sample.Bucket.InputDigest = inputDigest
	sample.Bucket.ExecutionMode = executionMode
	sample.Bucket.ResourceClassID = resourceClassID
	sample.Bucket.ResourceCPU = resourceCPU
	sample.Bucket.ResourceMemoryGiB = resourceMemory
	sample.TargetKind = WorkloadKind(targetKind)
	sample.TargetStatus = GoTestStatus(targetStatus)
	return sample, nil
}

// loadSQLiteCalibration 读取并校验校准记录。
func loadSQLiteCalibration(database sqliteRowQueryer) (*DurationCalibration, error) {
	var (
		calibration                                         DurationCalibration
		commitEntrypoint, pushEntrypoint, releaseEntrypoint string
		completedAtMS                                       int64
	)
	err := database.QueryRow(`
		SELECT schema_version, commit_sha, tree_sha, platform, runner, toolchain,
			commit_entrypoint, push_entrypoint, release_entrypoint,
			commit_catalog_digest, push_catalog_digest, release_catalog_digest,
			calibration_resource_class_id, calibration_resource_cpu, calibration_resource_memory_gib,
			workload_count, race_package_count, accepted_snapshot_id, completed_at_unix_ms
		FROM duration_calibrations
		WHERE singleton = 1
	`).Scan(
		&calibration.SchemaVersion,
		&calibration.Commit,
		&calibration.Tree,
		&calibration.Platform,
		&calibration.Runner,
		&calibration.Toolchain,
		&commitEntrypoint,
		&pushEntrypoint,
		&releaseEntrypoint,
		&calibration.CommitCatalogDigest,
		&calibration.PushCatalogDigest,
		&calibration.ReleaseCatalogDigest,
		&calibration.CalibrationResourceClassID,
		&calibration.CalibrationResourceCPU,
		&calibration.CalibrationResourceMemoryGiB,
		&calibration.WorkloadCount,
		&calibration.RacePackageCount,
		&calibration.AcceptedSnapshotID,
		&completedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("load duration calibration", err)
	}
	calibration.CommitEntrypoint = CIEntrypointID(commitEntrypoint)
	calibration.PushEntrypoint = CIEntrypointID(pushEntrypoint)
	calibration.ReleaseEntrypoint = CIEntrypointID(releaseEntrypoint)
	calibration.CompletedAt = time.UnixMilli(completedAtMS).UTC()
	if err := ValidateDurationCalibration(calibration); err != nil {
		return nil, fmt.Errorf("validate duration ledger SQLite calibration: %w", err)
	}
	return &calibration, nil
}

// loadSQLiteShardOverhead 只选择同环境、accepted baseline 保留窗口内最新的权威 aggregate，
// 并以样本表行数复核 aggregate 的 sample_count，拒绝空或不完整 provenance。
func loadSQLiteShardOverhead(database sqliteRowQueryer, planning PlanningContext, currentAcceptedGeneration uint64) (*ShardOrchestrationOverhead, error) {
	if currentAcceptedGeneration == 0 {
		return nil, errors.New("shard orchestration overhead requires accepted baseline generation")
	}
	var err error
	if planning, err = resolveShardOverheadCalibrationResource(database, planning); err != nil {
		return nil, err
	}
	if planning.CalibrationResourceClassID == "" {
		return nil, nil
	}
	retained := cicontract.RetentionGenerationWindow(currentAcceptedGeneration)
	retainedText := [3]string{
		strconv.FormatUint(retained[0], 10),
		strconv.FormatUint(retained[1], 10),
		strconv.FormatUint(retained[2], 10),
	}
	covered, err := sqliteShardOverheadIdentityCoverage(database, retainedText, planning)
	if err != nil {
		return nil, err
	}
	if !covered {
		return nil, nil
	}
	overhead, generationText, err := querySQLiteShardOverhead(database, planning, retainedText)
	if err != nil {
		return nil, err
	}
	if generationText == "" {
		return nil, nil
	}
	if err := validateSQLiteShardOverheadEvidence(database, &overhead, generationText, currentAcceptedGeneration); err != nil {
		return nil, err
	}
	return &overhead, nil
}

// resolveShardOverheadCalibrationResource 从 accepted calibration singleton 补齐开销查询的固定资源。
func resolveShardOverheadCalibrationResource(database sqliteRowQueryer, planning PlanningContext) (PlanningContext, error) {
	empty := planning.CalibrationResourceClassID == "" && planning.CalibrationResourceCPU == 0 && planning.CalibrationResourceMemoryGiB == 0
	partial := planning.CalibrationResourceClassID == "" || planning.CalibrationResourceCPU <= 0 || planning.CalibrationResourceMemoryGiB <= 0
	if !empty && partial {
		return PlanningContext{}, errors.New("shard overhead calibration resource identity is incomplete")
	}
	if !empty {
		return planning, nil
	}
	calibration, err := loadSQLiteCalibration(database)
	if err != nil {
		return PlanningContext{}, err
	}
	if calibration == nil {
		return planning, nil
	}
	planning.CalibrationResourceClassID = calibration.CalibrationResourceClassID
	planning.CalibrationResourceCPU = calibration.CalibrationResourceCPU
	planning.CalibrationResourceMemoryGiB = calibration.CalibrationResourceMemoryGiB
	return planning, nil
}

// validateSQLiteShardOverheadEvidence 校验 aggregate、样本闭包和 P95 一致性。
func validateSQLiteShardOverheadEvidence(database sqliteRowQueryer, overhead *ShardOrchestrationOverhead, generationText string, currentAcceptedGeneration uint64) error {
	acceptedGeneration, err := strconv.ParseUint(generationText, 10, 64)
	if err != nil || acceptedGeneration == 0 {
		return errors.New("shard orchestration overhead accepted generation is invalid")
	}
	if err := validateSQLiteDurationSampleGeneration(acceptedGeneration, currentAcceptedGeneration); err != nil {
		return err
	}
	overhead.AcceptedGeneration = acceptedGeneration
	if err := ValidateShardOrchestrationOverhead(*overhead); err != nil {
		return fmt.Errorf("validate shard orchestration overhead: %w", err)
	}
	samples, err := loadSQLiteShardOverheadSamples(database, generationText, overhead.ProvenanceDigest)
	if err != nil {
		return err
	}
	if len(samples) != overhead.SampleCount {
		return fmt.Errorf("shard orchestration overhead sample count %d does not match selected evidence rows %d", overhead.SampleCount, len(samples))
	}
	durations := make([]int64, len(samples))
	for index, sample := range samples {
		durations[index] = sample.OverheadMS
	}
	if nearestRankP95(durations) != overhead.P95MS {
		return errors.New("shard orchestration overhead p95 does not match selected evidence rows")
	}
	return nil
}

// sqliteShardOverheadIdentityCoverage 排除缺少 exact calibration execution identity 的样本 aggregate。
func sqliteShardOverheadIdentityCoverage(database sqliteRowQueryer, retained [3]string, planning PlanningContext) (bool, error) {
	var invalid int
	err := database.QueryRow(`
		SELECT COUNT(*)
		FROM duration_shard_overhead_samples AS samples
		LEFT JOIN ci_runs AS runs ON runs.job_id = samples.job_id
		LEFT JOIN ci_remote_run_execution_identities AS run_identity ON run_identity.job_id = samples.job_id
			WHERE samples.accepted_generation IN (?, ?, ?)
			  AND samples.provenance_digest IN (
				SELECT provenance_digest FROM duration_shard_overheads
				WHERE accepted_generation IN (?, ?, ?)
				  AND platform = ? AND runner = ? AND toolchain = ? AND accepted_snapshot_id = ?
				  AND calibration_resource_class_id = ? AND calibration_resource_cpu = ? AND calibration_resource_memory_gib = ?
			)
			  AND (
				runs.job_id IS NULL OR runs.status <> 'passed' OR runs.authoritative <> 1 OR runs.cleanup_complete <> 1
				OR runs.accepted_generation IS NULL OR runs.accepted_generation <> samples.accepted_generation
				OR runs.image_cache_snapshot_id IS NULL OR runs.image_cache_snapshot_id <> ?
				OR run_identity.job_id IS NULL
			OR run_identity.platform <> ? OR length(run_identity.runner_identity_digest) <> 71
			OR substr(run_identity.runner_identity_digest, 1, 7) <> 'sha256:'
			OR length(run_identity.toolchain_digest) <> 71 OR substr(run_identity.toolchain_digest, 1, 7) <> 'sha256:'
			OR run_identity.runner_identity_digest <> ? OR run_identity.toolchain_digest <> ?
			OR run_identity.execution_mode <> ?
		  )
		`, retained[0], retained[1], retained[2], retained[0], retained[1], retained[2],
		planning.Platform, planning.Runner, planning.Toolchain, planning.AcceptedSnapshotID,
		planning.CalibrationResourceClassID, planning.CalibrationResourceCPU, planning.CalibrationResourceMemoryGiB,
		planning.AcceptedSnapshotID,
		planning.Platform, planning.Runner, planning.Toolchain, DurationExecutionModeCalibration).Scan(&invalid)
	if err != nil {
		return false, mapDurationLedgerSQLiteError("audit shard overhead execution identity coverage", err)
	}
	return invalid == 0, nil
}

// querySQLiteShardOverhead 在 accepted generation 窗口内读取单个 aggregate。
func querySQLiteShardOverhead(database sqliteRowQueryer, planning PlanningContext, retained [3]string) (ShardOrchestrationOverhead, string, error) {
	var (
		overhead        ShardOrchestrationOverhead
		generationText  string
		resourceClassID string
	)
	err := database.QueryRow(`
		SELECT schema_version, accepted_generation, policy_version, platform, runner, toolchain,
			calibration_resource_class_id, calibration_resource_cpu, calibration_resource_memory_gib,
			p95_ms, sample_count, provenance_digest, accepted_snapshot_id
		FROM duration_shard_overheads
			WHERE platform = ? AND runner = ? AND toolchain = ? AND accepted_snapshot_id = ?
			  AND calibration_resource_class_id = ? AND calibration_resource_cpu = ? AND calibration_resource_memory_gib = ?
			  AND accepted_generation IN (?, ?, ?)
			ORDER BY length(accepted_generation) DESC, accepted_generation DESC, id DESC
		LIMIT 1
	`, planning.Platform, planning.Runner, planning.Toolchain, planning.AcceptedSnapshotID,
		planning.CalibrationResourceClassID, planning.CalibrationResourceCPU, planning.CalibrationResourceMemoryGiB,
		retained[0], retained[1], retained[2]).Scan(
		&overhead.SchemaVersion,
		&generationText,
		&overhead.PolicyVersion,
		&overhead.Platform,
		&overhead.Runner,
		&overhead.Toolchain,
		&resourceClassID,
		&overhead.CalibrationResourceCPU,
		&overhead.CalibrationResourceMemoryGiB,
		&overhead.P95MS,
		&overhead.SampleCount,
		&overhead.ProvenanceDigest,
		&overhead.AcceptedSnapshotID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ShardOrchestrationOverhead{}, "", nil
	}
	if err != nil {
		return ShardOrchestrationOverhead{}, "", mapDurationLedgerSQLiteError("load shard orchestration overhead", err)
	}
	overhead.CalibrationResourceClassID = resourceClassID
	return overhead, generationText, nil
}

// loadSQLiteShardOverheadSamples 读取并校验指定代次的 overhead 原始样本。
func loadSQLiteShardOverheadSamples(database sqliteRowQueryer, generation, provenanceDigest string) ([]ShardOrchestrationOverheadSample, error) {
	rows, err := database.Query(`
		SELECT accepted_generation, provenance_digest, job_id, shard_identity,
			total_started_at_unix_ms, total_completed_at_unix_ms,
			workload_envelope_start_unix_ms, workload_envelope_end_unix_ms,
			accounted_duration_ms, accounted_interval_count, overhead_ms
		FROM duration_shard_overhead_samples
		WHERE accepted_generation = ? AND provenance_digest = ?
		ORDER BY job_id, shard_identity
	`, generation, provenanceDigest)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query shard orchestration overhead samples", err)
	}
	defer rows.Close()
	samples := make([]ShardOrchestrationOverheadSample, 0)
	for rows.Next() {
		var (
			sample                                                 ShardOrchestrationOverheadSample
			generationText                                         string
			startedMS, completedMS, envelopeStartMS, envelopeEndMS int64
		)
		if err := rows.Scan(&generationText, &sample.ProvenanceDigest, &sample.JobID, &sample.ShardIdentity, &startedMS, &completedMS, &envelopeStartMS, &envelopeEndMS, &sample.AccountedDurationMS, &sample.AccountedIntervalCount, &sample.OverheadMS); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan shard orchestration overhead sample", err)
		}
		sample.AcceptedGeneration, err = strconv.ParseUint(generationText, 10, 64)
		if err != nil {
			return nil, errors.New("shard orchestration overhead sample accepted generation is invalid")
		}
		sample.TotalStartedAt = time.UnixMilli(startedMS).UTC()
		sample.TotalCompletedAt = time.UnixMilli(completedMS).UTC()
		sample.WorkloadEnvelopeStart = time.UnixMilli(envelopeStartMS).UTC()
		sample.WorkloadEnvelopeEnd = time.UnixMilli(envelopeEndMS).UTC()
		if err := ValidateShardOrchestrationOverheadSample(sample); err != nil {
			return nil, fmt.Errorf("validate shard orchestration overhead sample: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate shard orchestration overhead samples", err)
	}
	return samples, nil
}

func truncateDurationCalibrationMilliseconds(calibration *DurationCalibration) *DurationCalibration {
	if calibration == nil {
		return nil
	}
	copy := *calibration
	copy.CompletedAt = copy.CompletedAt.UTC().Truncate(time.Millisecond)
	return &copy
}
