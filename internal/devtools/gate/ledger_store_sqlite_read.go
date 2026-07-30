package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// loadSQLiteMetadata 读取并校验账本元数据。
func loadSQLiteMetadata(database sqliteRowQueryer) (DurationLedgerSnapshot, error) {
	var (
		generationText string
		version        int
		schemaVersion  int
	)
	err := database.QueryRow(`
		SELECT schema_version, generation, ledger_version
		FROM duration_ledger_meta
		WHERE singleton = 1
	`).Scan(&schemaVersion, &generationText, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return DurationLedgerSnapshot{}, errors.New("duration ledger SQLite metadata is missing")
	}
	if err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError("load duration ledger SQLite metadata", err)
	}
	if schemaVersion != durationLedgerSQLiteSchemaVersion {
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
		SELECT workload_id, command_digest, platform, runner, toolchain,
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
	rows, err := database.Query(`
		SELECT workload_id, command_digest,
			COALESCE(SUM(CASE WHEN succeeded = 1 THEN duration_ms ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN succeeded = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(MAX(CASE WHEN succeeded = 0 THEN duration_ms ELSE 0 END), 0)
		FROM duration_samples INDEXED BY idx_duration_samples_planning
		WHERE platform = ? AND runner = ? AND toolchain = ?
		GROUP BY workload_id, command_digest
	`, planning.Platform, planning.Runner, planning.Toolchain)
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
		sample       DurationSample
		succeeded    int
		targetKind   string
		targetStatus string
	)
	if err := scanner.Scan(
		&sample.Bucket.WorkloadID,
		&sample.Bucket.CommandDigest,
		&sample.Bucket.Platform,
		&sample.Bucket.Runner,
		&sample.Bucket.Toolchain,
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
			workload_count, race_package_count, completed_at_unix_ms
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
		&calibration.WorkloadCount,
		&calibration.RacePackageCount,
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
func truncateDurationCalibrationMilliseconds(calibration *DurationCalibration) *DurationCalibration {
	if calibration == nil {
		return nil
	}
	copy := *calibration
	copy.CompletedAt = copy.CompletedAt.UTC().Truncate(time.Millisecond)
	return &copy
}
