package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// replaceSQLiteLedger 原子替换账本内容和元数据。
func replaceSQLiteLedger(
	transaction *sql.Tx,
	generation uint64,
	ledger DurationLedger,
) error {
	if _, err := transaction.Exec(`DELETE FROM duration_samples`); err != nil {
		return mapDurationLedgerSQLiteError("clear duration ledger SQLite samples", err)
	}
	if err := insertSQLiteDurationSamples(transaction, generation, ledger.Samples); err != nil {
		return err
	}
	if err := replaceSQLiteCalibration(transaction, ledger.Calibration); err != nil {
		return err
	}
	if _, err := transaction.Exec(`
		INSERT INTO duration_ledger_meta (
			singleton, authority_id, schema_version, generation, ledger_version
		) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET
			authority_id = excluded.authority_id,
			schema_version = excluded.schema_version,
			generation = excluded.generation,
			ledger_version = excluded.ledger_version
	`,
		cicontract.SQLAuthorityID,
		1,
		strconv.FormatUint(generation, 10),
		ledger.Version,
	); err != nil {
		return mapDurationLedgerSQLiteError("store duration ledger SQLite metadata", err)
	}
	return nil
}

const insertSQLiteDurationSampleSQL = `
	INSERT INTO duration_samples (
		accepted_generation,
		workload_id, command_digest, platform, runner, toolchain,
		succeeded, duration_ms, target_kind, parent_workload_id,
		parent_command_digest, target_name, target_status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

func insertSQLiteDurationSamples(transaction *sql.Tx, acceptedGeneration uint64, samples []DurationSample) error {
	if len(samples) == 0 {
		return nil
	}
	statement, err := transaction.Prepare(insertSQLiteDurationSampleSQL)
	if err != nil {
		return mapDurationLedgerSQLiteError("prepare duration ledger SQLite sample insert", err)
	}
	for _, sample := range samples {
		if err := execSQLiteDurationSample(statement, acceptedGeneration, sample); err != nil {
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

func execSQLiteDurationSample(statement *sql.Stmt, acceptedGeneration uint64, sample DurationSample) error {
	_, err := statement.Exec(sqliteDurationSampleArguments(acceptedGeneration, sample)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("insert duration ledger SQLite sample", err)
	}
	return nil
}

func sqliteDurationSampleArguments(acceptedGeneration uint64, sample DurationSample) []any {
	succeeded := 0
	if sample.Succeeded {
		succeeded = 1
	}
	return []any{
		strconv.FormatUint(acceptedGeneration, 10),
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

func sqliteCurrentGeneration(transaction *sql.Tx) (uint64, bool, error) {
	var generationText, authorityID string
	err := transaction.QueryRow(`
		SELECT generation, authority_id FROM duration_ledger_meta WHERE singleton = 1
	`).Scan(&generationText, &authorityID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, mapDurationLedgerSQLiteError("load current duration ledger generation", err)
	}
	if authorityID != cicontract.SQLAuthorityID {
		return 0, false, fmt.Errorf(
			"duration ledger SQLite authority ID %q must equal %q",
			authorityID,
			cicontract.SQLAuthorityID,
		)
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
