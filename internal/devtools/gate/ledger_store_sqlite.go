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
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	durationLedgerSQLiteSchemaVersion = 5
	durationLedgerSQLiteBusyTimeoutMS = 5_000
)

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
	if err := replaceSQLiteLedger(transaction, nextGeneration, ledger); err != nil {
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
		WHERE singleton = 1 AND generation = ? AND authority_id = ?
	`,
		strconv.FormatUint(nextGeneration, 10),
		strconv.FormatUint(expectedGeneration, 10),
		cicontract.SQLAuthorityID,
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
func (store *DurationLedgerStore) appendSQLiteSamplesFast(acceptedGeneration uint64, samples []DurationSample) (uint64, error) {
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
		generation, err := store.appendSQLiteSamplesOnce(acceptedGeneration, samples)
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
func (store *DurationLedgerStore) appendSQLiteSamplesOnce(acceptedGeneration uint64, samples []DurationSample) (uint64, error) {
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

	nextGeneration, err := appendSQLiteDurationSamplesInTransaction(transaction, acceptedGeneration, samples)
	if err != nil {
		return 0, err
	}
	if err := compactDurationLedgerAuthority(transaction); err != nil {
		return 0, err
	}
	if err := transaction.Commit(); err != nil {
		return 0, mapDurationLedgerSQLiteError("commit duration ledger append", err)
	}
	return nextGeneration, nil
}

// appendSQLiteDurationSamplesInTransaction 在不提交调用方事务的前提下追加已接受代样本并推进权威修订。
func appendSQLiteDurationSamplesInTransaction(transaction *sql.Tx, acceptedGeneration uint64, samples []DurationSample) (uint64, error) {
	if err := requireHistoricallyAcceptedGeneration(transaction, acceptedGeneration); err != nil {
		return 0, err
	}
	generation, nextGeneration, err := nextSQLiteLedgerGeneration(transaction)
	if err != nil {
		return 0, err
	}
	if err := insertSQLiteDurationSamples(transaction, acceptedGeneration, samples); err != nil {
		return 0, err
	}
	result, err := transaction.Exec(
		`UPDATE duration_ledger_meta
		SET generation = ?
		WHERE singleton = 1 AND generation = ? AND authority_id = ?`,
		strconv.FormatUint(nextGeneration, 10),
		strconv.FormatUint(generation, 10),
		cicontract.SQLAuthorityID,
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
			return ErrDurationLedgerMetadataMissing
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
		return 0, 0, ErrDurationLedgerMetadataMissing
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
	database, err := openSQLiteLedgerDatabase(store.path, store.nowFunc, store.schemaValidator)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(store.path, 0o600); err != nil {
		database.Close()
		return nil, fmt.Errorf("restrict duration ledger SQLite authority permissions: %w", err)
	}
	return database, nil
}

// prepareSQLiteAuthorityPath 在打开数据库前校验 SQLite 权威路径。
func (store *DurationLedgerStore) prepareSQLiteAuthorityPath(create bool) error {
	if _, err := os.Stat(store.path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat duration ledger SQLite authority: %w", err)
		}
		if !create {
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

func openSQLiteLedgerDatabase(
	path string,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
) (*sql.DB, error) {
	for attempt := range 16 {
		database, err := openSQLiteLedgerDatabaseOnce(path, now, validator)
		if !errors.Is(err, ErrDurationLedgerBusy) {
			return database, err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return nil, fmt.Errorf("open duration ledger SQLite authority exceeded busy retry limit")
}

func openSQLiteLedgerDatabaseOnce(
	path string,
	now func() time.Time,
	validator *durationLedgerSQLiteSchemaValidator,
) (*sql.DB, error) {
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
	if err := ensureDurationLedgerSQLiteSchemaWithValidator(database, now, validator); err != nil {
		database.Close()
		return nil, err
	}
	return database, nil
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
