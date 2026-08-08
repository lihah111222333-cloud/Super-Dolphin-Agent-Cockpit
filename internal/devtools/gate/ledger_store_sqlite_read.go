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

func loadSQLiteDurationSamples(database sqliteRowQueryer) ([]DurationSample, error) {
	rows, err := database.Query(`
		SELECT workload_id, command_digest, input_digest, platform, runner, toolchain,
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
) (DurationSampleIndex, error) {
	index := DurationSampleIndex{
		context: planning,
		buckets: make(map[durationSampleIndexKey]durationSampleAggregate),
	}
	query := `
		SELECT workload_id, command_digest, input_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib,
			COALESCE(SUM(CASE WHEN succeeded = 1 THEN duration_ms ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(MAX(CASE WHEN succeeded = 0 THEN duration_ms ELSE 0 END), 0)
		FROM duration_samples
		WHERE execution_mode = ? AND platform = ? AND runner = ? AND toolchain = ?
		GROUP BY workload_id, command_digest, input_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib
	`
	args := []any{DurationExecutionModeNormal, planning.Platform, planning.Runner, planning.Toolchain}
	if planning.Calibration {
		query = `
			SELECT workload_id, command_digest, input_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib,
				COALESCE(SUM(CASE WHEN succeeded = 1 THEN duration_ms ELSE 0 END), 0),
				COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0),
				COALESCE(MAX(CASE WHEN succeeded = 0 THEN duration_ms ELSE 0 END), 0)
			FROM duration_samples
			WHERE execution_mode = ? AND resource_class_id = ? AND resource_cpu = ? AND resource_memory_gib = ?
				AND platform = ? AND runner = ? AND toolchain = ?
			GROUP BY workload_id, command_digest, input_digest, execution_mode, resource_class_id, resource_cpu, resource_memory_gib
		`
		args = []any{DurationExecutionModeCalibration, planning.CalibrationResourceClassID, planning.CalibrationResourceCPU, planning.CalibrationResourceMemoryGiB, planning.Platform, planning.Runner, planning.Toolchain}
	}
	rows, err := database.Query(query, args...)
	if err != nil {
		return DurationSampleIndex{}, mapDurationLedgerSQLiteError("query duration sample planning index", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			key       durationSampleIndexKey
			aggregate durationSampleAggregate
		)
		if err := rows.Scan(
			&key.workloadID,
			&key.commandDigest,
			&key.inputDigest,
			&key.executionMode,
			&key.resourceClassID,
			&key.resourceCPU,
			&key.resourceMemoryGiB,
			&aggregate.successTotalMS,
			&aggregate.successCount,
			&aggregate.maxFailureDuration,
		); err != nil {
			return DurationSampleIndex{}, mapDurationLedgerSQLiteError("scan duration sample planning index", err)
		}
		index.buckets[key] = aggregate
	}
	if err := rows.Err(); err != nil {
		return DurationSampleIndex{}, mapDurationLedgerSQLiteError("iterate duration sample planning index", err)
	}
	return index, nil
}
func scanSQLiteDurationSample(scanner interface{ Scan(...any) error }) (DurationSample, error) {
	var (
		sample                         DurationSample
		succeeded                      int
		resourceCPU, resourceMemory    float64
		executionMode, resourceClassID string
		inputDigest                    string
		targetKind                     string
		targetStatus                   string
	)
	if err := scanner.Scan(
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

// loadSQLiteShardOverhead 只选择同环境、最新 accepted generation 的权威 aggregate，
// 并以样本表行数复核 aggregate 的 sample_count，拒绝空或不完整 provenance。
func loadSQLiteShardOverhead(database sqliteRowQueryer, planning PlanningContext) (*ShardOrchestrationOverhead, error) {
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
		ORDER BY length(accepted_generation) DESC, accepted_generation DESC, id DESC
		LIMIT 1
	`, planning.Platform, planning.Runner, planning.Toolchain, planning.AcceptedSnapshotID).Scan(
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
		return nil, nil
	}
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("load shard orchestration overhead", err)
	}
	acceptedGeneration, err := strconv.ParseUint(generationText, 10, 64)
	if err != nil || acceptedGeneration == 0 {
		return nil, errors.New("shard orchestration overhead accepted generation is invalid")
	}
	overhead.AcceptedGeneration = acceptedGeneration
	overhead.CalibrationResourceClassID = resourceClassID
	if err := ValidateShardOrchestrationOverhead(overhead); err != nil {
		return nil, fmt.Errorf("validate shard orchestration overhead: %w", err)
	}
	samples, err := loadSQLiteShardOverheadSamples(database, generationText, overhead.ProvenanceDigest)
	if err != nil {
		return nil, err
	}
	if len(samples) != overhead.SampleCount {
		return nil, fmt.Errorf("shard orchestration overhead sample count %d does not match selected evidence rows %d", overhead.SampleCount, len(samples))
	}
	durations := make([]int64, len(samples))
	for index, sample := range samples {
		durations[index] = sample.OverheadMS
	}
	if nearestRankP95(durations) != overhead.P95MS {
		return nil, errors.New("shard orchestration overhead p95 does not match selected evidence rows")
	}
	return &overhead, nil
}

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
