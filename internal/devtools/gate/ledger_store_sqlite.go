package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	durationLedgerSQLiteSchemaVersion = 1
	durationLedgerSQLiteBusyTimeoutMS = 5_000
)

func durationLedgerAuthorityPaths(configuredPath string) (string, string) {
	extension := strings.ToLower(filepath.Ext(configuredPath))
	switch extension {
	case ".json":
		return strings.TrimSuffix(configuredPath, filepath.Ext(configuredPath)) + ".sqlite", configuredPath
	case ".sqlite", ".sqlite3", ".db":
		return configuredPath, strings.TrimSuffix(configuredPath, filepath.Ext(configuredPath)) + ".json"
	default:
		return configuredPath + ".sqlite", configuredPath
	}
}

// loadSQLiteSnapshot 在单个只读事务中加载账本快照及其请求的数据投影。
func (store *DurationLedgerStore) loadSQLiteSnapshot(
	includeSamples bool,
	planning PlanningContext,
) (DurationLedgerSnapshot, error) {
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	defer database.Close()

	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError("begin duration ledger read snapshot", err)
	}
	defer transaction.Rollback()
	snapshot, err := loadSQLiteSnapshotPayload(transaction, includeSamples, planning)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	if err := transaction.Commit(); err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError("commit duration ledger read snapshot", err)
	}
	return snapshot, nil
}

// loadSQLiteSnapshotPayload 保持读取投影与调用方事务边界一致。
func loadSQLiteSnapshotPayload(transaction *sql.Tx, includeSamples bool, planning PlanningContext) (DurationLedgerSnapshot, error) {
	snapshot, err := loadSQLiteMetadata(transaction)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	if includeSamples {
		snapshot.Ledger.Samples, err = loadSQLiteDurationSamples(transaction)
		if err != nil {
			return DurationLedgerSnapshot{}, err
		}
		if err := ValidateDurationLedger(snapshot.Ledger); err != nil {
			return DurationLedgerSnapshot{}, fmt.Errorf("validate SQLite duration ledger: %w", err)
		}
		return snapshot, nil
	}
	if planning.Platform == "" {
		return snapshot, nil
	}
	index, err := loadSQLiteDurationSampleIndex(transaction, planning)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	snapshot.SampleIndex = &index
	return snapshot, nil
}

// compareAndSwapSQLite 维护 SQLite 权威账本的原子读写边界。
func (store *DurationLedgerStore) compareAndSwapSQLite(
	expectedGeneration uint64,
	ledger DurationLedger,
) (DurationLedgerSnapshot, error) {
	if expectedGeneration == math.MaxUint64 {
		return DurationLedgerSnapshot{}, errors.New("duration ledger generation overflow")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	defer database.Close()

	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError("begin duration ledger CAS", err)
	}
	defer transaction.Rollback()

	if err := validateSQLiteLedgerGeneration(transaction, expectedGeneration, true); err != nil {
		return DurationLedgerSnapshot{}, err
	}

	nextGeneration := expectedGeneration + 1
	if err := replaceSQLiteLedger(transaction, nextGeneration, ledger, ""); err != nil {
		return DurationLedgerSnapshot{}, err
	}
	if err := transaction.Commit(); err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError("commit duration ledger CAS", err)
	}
	return DurationLedgerSnapshot{Generation: nextGeneration, Ledger: ledger}, nil
}

// compareAndSwapSQLiteCalibration 维护 SQLite 权威账本的原子读写边界。
func (store *DurationLedgerStore) compareAndSwapSQLiteCalibration(
	expectedGeneration uint64,
	calibration *DurationCalibration,
) (DurationLedgerSnapshot, error) {
	if expectedGeneration == math.MaxUint64 {
		return DurationLedgerSnapshot{}, errors.New("duration ledger generation overflow")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return DurationLedgerSnapshot{}, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError(
			"begin duration calibration CAS",
			err,
		)
	}
	defer transaction.Rollback()
	if err := validateSQLiteLedgerGeneration(transaction, expectedGeneration, false); err != nil {
		return DurationLedgerSnapshot{}, err
	}
	calibration = truncateDurationCalibrationMilliseconds(calibration)
	if err := replaceSQLiteCalibration(transaction, calibration); err != nil {
		return DurationLedgerSnapshot{}, err
	}
	nextGeneration := expectedGeneration + 1
	result, err := transaction.Exec(`
		UPDATE duration_ledger_meta
		SET generation = ?
		WHERE singleton = 1 AND generation = ?
	`,
		strconv.FormatUint(nextGeneration, 10),
		strconv.FormatUint(expectedGeneration, 10),
	)
	if err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError(
			"update duration calibration",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return DurationLedgerSnapshot{}, fmt.Errorf(
			"read duration calibration update count: %w",
			err,
		)
	}
	if affected != 1 {
		return DurationLedgerSnapshot{}, durationLedgerConflict(expectedGeneration, expectedGeneration)
	}
	if err := transaction.Commit(); err != nil {
		return DurationLedgerSnapshot{}, mapDurationLedgerSQLiteError(
			"commit duration calibration CAS",
			err,
		)
	}
	return DurationLedgerSnapshot{
		Generation: nextGeneration,
		Ledger: DurationLedger{
			Version:     durationLedgerVersion,
			Calibration: calibration,
		},
	}, nil
}

// appendSQLiteSamplesFast 维护 SQLite 权威账本的原子读写边界。
func (store *DurationLedgerStore) appendSQLiteSamplesFast(samples []DurationSample) (uint64, error) {
	if len(samples) == 0 {
		snapshot, err := store.LoadMetadata()
		return snapshot.Generation, err
	}
	if err := ValidateDurationLedger(DurationLedger{
		Version: durationLedgerVersion,
		Samples: samples,
	}); err != nil {
		return 0, fmt.Errorf("validate appended duration samples: %w", err)
	}
	for attempt := range 16 {
		generation, err := store.appendSQLiteSamplesOnce(samples)
		if err == nil {
			return generation, nil
		}
		if !errors.Is(err, ErrDurationLedgerBusy) {
			return 0, err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return 0, errors.New("append duration ledger samples exceeded retry limit")
}

// appendSQLiteSamplesOnce 维护 SQLite 权威账本的原子读写边界。
func (store *DurationLedgerStore) appendSQLiteSamplesOnce(samples []DurationSample) (uint64, error) {
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return 0, err
	}
	defer database.Close()

	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("begin duration ledger append", err)
	}
	defer transaction.Rollback()

	generation, nextGeneration, err := nextSQLiteLedgerGeneration(transaction)
	if err != nil {
		return 0, err
	}
	if err := insertSQLiteDurationSamples(transaction, samples); err != nil {
		return 0, err
	}
	if err := compactSQLiteDurationSamples(transaction, samples); err != nil {
		return 0, err
	}
	result, err := transaction.Exec(
		`UPDATE duration_ledger_meta SET generation = ? WHERE singleton = 1 AND generation = ?`,
		strconv.FormatUint(nextGeneration, 10),
		strconv.FormatUint(generation, 10),
	)
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("advance duration ledger generation", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read duration ledger generation update count: %w", err)
	}
	if affected != 1 {
		return 0, fmt.Errorf("advance duration ledger generation: %w", ErrDurationLedgerBusy)
	}
	if err := transaction.Commit(); err != nil {
		return 0, mapDurationLedgerSQLiteError("commit duration ledger append", err)
	}
	return nextGeneration, nil
}

// validateSQLiteLedgerGeneration 保持 CAS 对缺失账本和 generation 冲突的既有判定。
func validateSQLiteLedgerGeneration(transaction *sql.Tx, expected uint64, allowAbsent bool) error {
	current, exists, err := sqliteCurrentGeneration(transaction)
	if err != nil {
		return err
	}
	if !exists {
		if !allowAbsent {
			return errors.New("duration ledger SQLite metadata is missing")
		}
		if expected != 0 {
			return fmt.Errorf("%w: expected generation %d, ledger is absent", ErrDurationLedgerConflict, expected)
		}
		return nil
	}
	if current != expected {
		return durationLedgerConflict(expected, current)
	}
	return nil
}

func nextSQLiteLedgerGeneration(transaction *sql.Tx) (uint64, uint64, error) {
	generation, exists, err := sqliteCurrentGeneration(transaction)
	if err != nil {
		return 0, 0, err
	}
	if !exists {
		return 0, 0, errors.New("duration ledger SQLite metadata is missing")
	}
	if generation == math.MaxUint64 {
		return 0, 0, errors.New("duration ledger generation overflow")
	}
	return generation, generation + 1, nil
}

// openSQLiteAuthority 维护 SQLite 权威账本的原子读写边界。
func (store *DurationLedgerStore) openSQLiteAuthority(create bool) (*sql.DB, error) {
	if err := store.prepareSQLiteAuthorityPath(create); err != nil {
		return nil, err
	}
	database, err := openSQLiteLedgerDatabase(store.path)
	if err != nil {
		return nil, err
	}
	migrated, err := store.migrateMissingSQLiteAuthority(database)
	if err != nil {
		database.Close()
		return nil, err
	}
	if migrated {
		return store.openSQLiteAuthority(create)
	}
	if err := os.Chmod(store.path, 0o600); err != nil {
		database.Close()
		return nil, fmt.Errorf("restrict duration ledger SQLite authority permissions: %w", err)
	}
	return database, nil
}

// prepareSQLiteAuthorityPath 在打开数据库前处理路径和遗留 JSON 迁移。
func (store *DurationLedgerStore) prepareSQLiteAuthorityPath(create bool) error {
	if _, err := os.Stat(store.path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat duration ledger SQLite authority: %w", err)
		}
		if legacyExists, legacyErr := regularFileExists(store.legacyPath); legacyErr != nil {
			return legacyErr
		} else if legacyExists {
			if err := store.migrateLegacyJSON(); err != nil {
				return err
			}
		} else if !create {
			return fmt.Errorf("open duration ledger SQLite authority: %w", os.ErrNotExist)
		}
	}
	if create {
		if err := requireExistingDirectory(filepath.Dir(store.path)); err != nil {
			return err
		}
	}
	return nil
}

func openSQLiteLedgerDatabase(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open duration ledger SQLite authority: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, mapDurationLedgerSQLiteError("ping duration ledger SQLite authority", err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
}

// migrateMissingSQLiteAuthority 在空 SQLite 库仍有遗留 JSON 时完成迁移。
func (store *DurationLedgerStore) migrateMissingSQLiteAuthority(database *sql.DB) (bool, error) {
	metadataExists, err := sqliteLedgerMetadataExists(database)
	if err != nil {
		return false, err
	}
	if metadataExists {
		return false, nil
	}
	legacyExists, err := regularFileExists(store.legacyPath)
	if err != nil || !legacyExists {
		return false, err
	}
	if err := database.Close(); err != nil {
		return false, fmt.Errorf("close duration ledger before migration: %w", err)
	}
	if err := store.migrateLegacyJSON(); err != nil {
		return false, err
	}
	return true, nil
}

func sqliteLedgerMetadataExists(database *sql.DB) (bool, error) {
	var marker int
	err := database.QueryRow(`
		SELECT singleton FROM duration_ledger_meta WHERE singleton = 1
	`).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapDurationLedgerSQLiteError("probe duration ledger metadata", err)
	}
	if marker != 1 {
		return false, errors.New("duration ledger SQLite metadata marker is invalid")
	}
	return true, nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat legacy duration ledger JSON: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("legacy duration ledger path %q is not a regular file", path)
	}
	return true, nil
}

func requireExistingDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat duration ledger directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("duration ledger parent %q is not a directory", path)
	}
	return nil
}

func durationLedgerConflict(expected uint64, current uint64) error {
	return fmt.Errorf(
		"%w: expected generation %d, current generation %d",
		ErrDurationLedgerConflict,
		expected,
		current,
	)
}
