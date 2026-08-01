package gate

import (
	"database/sql"
	"errors"
	"strconv"
)

// replaceSQLiteLedger 原子替换账本内容和元数据。
func replaceSQLiteLedger(
	transaction *sql.Tx,
	generation uint64,
	ledger DurationLedger,
	legacyDigest string,
) error {
	if _, err := transaction.Exec(`DELETE FROM duration_samples`); err != nil {
		return mapDurationLedgerSQLiteError("clear duration ledger SQLite samples", err)
	}
	if err := insertSQLiteDurationSamples(transaction, ledger.Samples); err != nil {
		return err
	}
	if err := replaceSQLiteCalibration(transaction, ledger.Calibration); err != nil {
		return err
	}
	if _, err := transaction.Exec(`
		INSERT INTO duration_ledger_meta (
			singleton, schema_version, generation, ledger_version, legacy_source_sha256
		) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET
			schema_version = excluded.schema_version,
			generation = excluded.generation,
			ledger_version = excluded.ledger_version,
			legacy_source_sha256 = CASE
				WHEN excluded.legacy_source_sha256 = ''
				THEN duration_ledger_meta.legacy_source_sha256
				ELSE excluded.legacy_source_sha256
			END
	`,
		durationLedgerSQLiteSchemaVersion,
		strconv.FormatUint(generation, 10),
		ledger.Version,
		legacyDigest,
	); err != nil {
		return mapDurationLedgerSQLiteError("store duration ledger SQLite metadata", err)
	}
	return nil
}

const insertSQLiteDurationSampleSQL = `
	INSERT INTO duration_samples (
		workload_id, command_digest, platform, runner, toolchain,
		succeeded, duration_ms, target_kind, parent_workload_id,
		parent_command_digest, target_name, target_status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

func insertSQLiteDurationSamples(transaction *sql.Tx, samples []DurationSample) error {
	if len(samples) == 0 {
		return nil
	}
	statement, err := transaction.Prepare(insertSQLiteDurationSampleSQL)
	if err != nil {
		return mapDurationLedgerSQLiteError("prepare duration ledger SQLite sample insert", err)
	}
	for _, sample := range samples {
		if err := execSQLiteDurationSample(statement, sample); err != nil {
			return errors.Join(
				err,
				closeSQLiteStatement(statement, "close duration ledger SQLite sample insert"),
			)
		}
	}
	return closeSQLiteStatement(statement, "close duration ledger SQLite sample insert")
}

func closeSQLiteStatement(statement *sql.Stmt, operation string) error {
	if err := statement.Close(); err != nil {
		return mapDurationLedgerSQLiteError(operation, err)
	}
	return nil
}

func execSQLiteDurationSample(statement *sql.Stmt, sample DurationSample) error {
	_, err := statement.Exec(sqliteDurationSampleArguments(sample)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("insert duration ledger SQLite sample", err)
	}
	return nil
}

func sqliteDurationSampleArguments(sample DurationSample) []any {
	succeeded := 0
	if sample.Succeeded {
		succeeded = 1
	}
	return []any{
		sample.Bucket.WorkloadID,
		sample.Bucket.CommandDigest,
		sample.Bucket.Platform,
		sample.Bucket.Runner,
		sample.Bucket.Toolchain,
		succeeded,
		sample.DurationMS,
		string(sample.TargetKind),
		sample.ParentWorkloadID,
		sample.ParentCommandDigest,
		sample.TargetName,
		string(sample.TargetStatus),
	}
}

// compactSQLiteDurationSamples 按保留策略压缩样本。
func compactSQLiteDurationSamples(transaction *sql.Tx, samples []DurationSample) error {
	if err := seedSQLiteDurationRetentionScope(transaction, samples); err != nil {
		return err
	}
	predicate := `
			WHERE EXISTS (
				SELECT 1
				FROM temp.duration_sample_retention_scope AS retention
				WHERE retention.workload_id = duration_samples.workload_id
					AND retention.platform = duration_samples.platform
					AND retention.toolchain = duration_samples.toolchain
					AND retention.command_digest = duration_samples.command_digest
					AND retention.runner = duration_samples.runner
			)`
	if _, err := transaction.Exec(`
		WITH executions AS (
			SELECT workload_id, platform, toolchain, command_digest, runner, MAX(id) AS latest_id
			FROM duration_samples
			`+predicate+`
			GROUP BY workload_id, platform, toolchain, command_digest, runner
		),
		ranked AS (
			SELECT workload_id, platform, toolchain, command_digest, runner,
				ROW_NUMBER() OVER (
					PARTITION BY workload_id, platform, toolchain
					ORDER BY latest_id DESC, command_digest DESC, runner DESC
				) AS execution_rank
			FROM executions
		)
		DELETE FROM duration_samples
		WHERE EXISTS (
			SELECT 1
			FROM ranked
			WHERE ranked.execution_rank > ?
				AND ranked.workload_id = duration_samples.workload_id
				AND ranked.platform = duration_samples.platform
				AND ranked.toolchain = duration_samples.toolchain
				AND ranked.command_digest = duration_samples.command_digest
				AND ranked.runner = duration_samples.runner
		)
	`, durationLedgerExecutionsPerWorkload); err != nil {
		return mapDurationLedgerSQLiteError("compact old duration ledger executions", err)
	}
	if _, err := transaction.Exec(`
		WITH ranked AS (
			SELECT id, succeeded,
				ROW_NUMBER() OVER (
					PARTITION BY workload_id, platform, toolchain, command_digest, runner, succeeded
					ORDER BY id DESC
				) AS sample_rank
			FROM duration_samples
			`+predicate+`
		)
		DELETE FROM duration_samples
		WHERE id IN (
			SELECT id
			FROM ranked
			WHERE (succeeded = 1 AND sample_rank > ?)
				OR (succeeded = 0 AND sample_rank > ?)
		)
	`, durationLedgerSuccessSamplesPerBucket, durationLedgerFailureSamplesPerBucket); err != nil {
		return mapDurationLedgerSQLiteError("compact duration ledger bucket samples", err)
	}
	if _, err := transaction.Exec(`DELETE FROM temp.duration_sample_retention_scope`); err != nil {
		return mapDurationLedgerSQLiteError("clear duration ledger retention scope", err)
	}
	return nil
}

// seedSQLiteDurationRetentionScope 建立本次压缩涉及的保留范围。
func seedSQLiteDurationRetentionScope(transaction *sql.Tx, samples []DurationSample) error {
	if _, err := transaction.Exec(`
		CREATE TEMP TABLE IF NOT EXISTS duration_sample_retention_scope (
			workload_id TEXT NOT NULL,
			platform TEXT NOT NULL,
			toolchain TEXT NOT NULL,
			command_digest TEXT NOT NULL,
			runner TEXT NOT NULL,
			PRIMARY KEY (workload_id, platform, toolchain, command_digest, runner)
		) WITHOUT ROWID
	`); err != nil {
		return mapDurationLedgerSQLiteError("create duration ledger retention scope", err)
	}
	if _, err := transaction.Exec(`DELETE FROM temp.duration_sample_retention_scope`); err != nil {
		return mapDurationLedgerSQLiteError("reset duration ledger retention scope", err)
	}
	if len(samples) == 0 {
		if _, err := transaction.Exec(`
			INSERT OR IGNORE INTO temp.duration_sample_retention_scope (
				workload_id, platform, toolchain, command_digest, runner
			)
			SELECT DISTINCT workload_id, platform, toolchain, command_digest, runner
			FROM duration_samples
		`); err != nil {
			return mapDurationLedgerSQLiteError("seed duration ledger retention scope from stored samples", err)
		}
		return nil
	}
	statement, err := transaction.Prepare(`
		INSERT OR IGNORE INTO temp.duration_sample_retention_scope (
			workload_id, platform, toolchain, command_digest, runner
		) VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return mapDurationLedgerSQLiteError("prepare duration ledger retention scope insert", err)
	}
	seen := make(map[DurationBucket]struct{}, len(samples))
	for _, sample := range samples {
		bucket := sample.Bucket
		if _, exists := seen[bucket]; exists {
			continue
		}
		seen[bucket] = struct{}{}
		if _, err := statement.Exec(
			bucket.WorkloadID,
			bucket.Platform,
			bucket.Toolchain,
			bucket.CommandDigest,
			bucket.Runner,
		); err != nil {
			return errors.Join(
				mapDurationLedgerSQLiteError("insert duration ledger retention scope", err),
				closeSQLiteStatement(statement, "close duration ledger retention scope insert"),
			)
		}
	}
	return closeSQLiteStatement(statement, "close duration ledger retention scope insert")
}
func sqliteCurrentGeneration(transaction *sql.Tx) (uint64, bool, error) {
	var generationText string
	err := transaction.QueryRow(`
		SELECT generation FROM duration_ledger_meta WHERE singleton = 1
	`).Scan(&generationText)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, mapDurationLedgerSQLiteError("load current duration ledger generation", err)
	}
	generation, err := strconv.ParseUint(generationText, 10, 64)
	if err != nil || generation == 0 {
		return 0, false, errors.New("duration ledger SQLite generation is invalid")
	}
	return generation, true, nil
}

// replaceSQLiteCalibration 替换或清空校准记录。
func replaceSQLiteCalibration(transaction *sql.Tx, calibration *DurationCalibration) error {
	if calibration == nil {
		if _, err := transaction.Exec(`DELETE FROM duration_calibrations WHERE singleton = 1`); err != nil {
			return mapDurationLedgerSQLiteError("clear duration calibration", err)
		}
		return nil
	}
	if _, err := transaction.Exec(`
		INSERT INTO duration_calibrations (
			singleton, schema_version, commit_sha, tree_sha, platform, runner, toolchain,
			commit_entrypoint, push_entrypoint, release_entrypoint,
			commit_catalog_digest, push_catalog_digest, release_catalog_digest,
			workload_count, race_package_count, completed_at_unix_ms
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET
			schema_version = excluded.schema_version,
			commit_sha = excluded.commit_sha,
			tree_sha = excluded.tree_sha,
			platform = excluded.platform,
			runner = excluded.runner,
			toolchain = excluded.toolchain,
			commit_entrypoint = excluded.commit_entrypoint,
			push_entrypoint = excluded.push_entrypoint,
			release_entrypoint = excluded.release_entrypoint,
			commit_catalog_digest = excluded.commit_catalog_digest,
			push_catalog_digest = excluded.push_catalog_digest,
			release_catalog_digest = excluded.release_catalog_digest,
			workload_count = excluded.workload_count,
			race_package_count = excluded.race_package_count,
			completed_at_unix_ms = excluded.completed_at_unix_ms
	`,
		calibration.SchemaVersion,
		calibration.Commit,
		calibration.Tree,
		calibration.Platform,
		calibration.Runner,
		calibration.Toolchain,
		string(calibration.CommitEntrypoint),
		string(calibration.PushEntrypoint),
		string(calibration.ReleaseEntrypoint),
		calibration.CommitCatalogDigest,
		calibration.PushCatalogDigest,
		calibration.ReleaseCatalogDigest,
		calibration.WorkloadCount,
		calibration.RacePackageCount,
		calibration.CompletedAt.UTC().UnixMilli(),
	); err != nil {
		return mapDurationLedgerSQLiteError("store duration calibration", err)
	}
	return nil
}
