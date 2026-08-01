package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

func (store *DurationLedgerStore) migrateLegacyJSON() (resultErr error) {
	if err := requireExistingDirectory(filepath.Dir(store.path)); err != nil {
		return err
	}
	lockContext, cancel := gateprivate.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	migrationLock, err := gateprivate.AcquireExclusiveFileLock(
		lockContext,
		store.path+".migration.lock",
	)
	if err != nil {
		return fmt.Errorf("acquire duration ledger migration lock: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, migrationLock.Release())
	}()
	legacyWriterLock, err := gateprivate.AcquireExclusiveFileLock(lockContext, store.legacyPath+".lock")
	if err != nil {
		return fmt.Errorf("acquire legacy duration ledger writer lock: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, legacyWriterLock.Release())
	}()
	return store.migrateLegacyJSONLocked()
}

func (store *DurationLedgerStore) migrateLegacyJSONLocked() error {
	database, transaction, exists, err := openLegacyDurationLedgerMigration(store.path)
	if err != nil {
		return err
	}
	defer database.Close()
	defer transaction.Rollback()
	if exists {
		return nil
	}
	metadata, sourceDigest, err := decodeLegacyDurationLedgerFile(store.legacyPath, transaction)
	if err != nil {
		return err
	}
	if err := commitLegacyDurationLedgerMigration(transaction, metadata, sourceDigest); err != nil {
		return err
	}
	return os.Chmod(store.path, 0o600)
}

func openLegacyDurationLedgerMigration(path string) (*sql.DB, *sql.Tx, bool, error) {
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(path))
	if err != nil {
		return nil, nil, false, fmt.Errorf("open duration ledger SQLite migration target: %w", err)
	}
	database.SetMaxOpenConns(1)
	if err := ensureDurationLedgerSQLiteSchema(database); err != nil {
		return nil, nil, false, errors.Join(err, database.Close())
	}
	transaction, err := database.Begin()
	if err != nil {
		return nil, nil, false, errors.Join(
			mapDurationLedgerSQLiteError("begin legacy duration ledger migration", err),
			database.Close(),
		)
	}
	_, exists, err := sqliteCurrentGeneration(transaction)
	if err != nil {
		return nil, nil, false, errors.Join(err, transaction.Rollback(), database.Close())
	}
	return database, transaction, exists, nil
}

func decodeLegacyDurationLedgerFile(
	legacyPath string,
	transaction *sql.Tx,
) (legacyDurationLedgerMigration, string, error) {
	legacy, err := os.Open(legacyPath)
	if err != nil {
		return legacyDurationLedgerMigration{}, "", fmt.Errorf("open legacy duration ledger JSON: %w", err)
	}
	hasher := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(legacy, hasher))
	decoder.DisallowUnknownFields()
	statement, err := transaction.Prepare(insertSQLiteDurationSampleSQL)
	if err != nil {
		prepareErr := mapDurationLedgerSQLiteError(
			"prepare legacy duration sample migration",
			err,
		)
		closeErr := legacy.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close legacy duration ledger JSON: %w", closeErr)
		}
		return legacyDurationLedgerMigration{}, "", errors.Join(prepareErr, closeErr)
	}
	metadata, decodeErr := decodeLegacyDurationLedgerMigration(decoder, statement)
	statementCloseErr := statement.Close()
	legacyCloseErr := legacy.Close()
	if err := errors.Join(decodeErr, statementCloseErr, legacyCloseErr); err != nil {
		return legacyDurationLedgerMigration{}, "", fmt.Errorf("stream legacy duration ledger JSON: %w", err)
	}
	return metadata, hex.EncodeToString(hasher.Sum(nil)), nil
}

func commitLegacyDurationLedgerMigration(
	transaction *sql.Tx,
	metadata legacyDurationLedgerMigration,
	sourceDigest string,
) error {
	if err := compactSQLiteDurationSamples(transaction, nil); err != nil {
		return err
	}
	if err := replaceSQLiteCalibration(transaction, metadata.Calibration); err != nil {
		return err
	}
	if _, err := transaction.Exec(`
		INSERT INTO duration_ledger_meta (
			singleton, schema_version, generation, ledger_version, legacy_source_sha256
		) VALUES (1, ?, ?, ?, ?)
	`,
		durationLedgerSQLiteSchemaVersion,
		strconv.FormatUint(metadata.Generation, 10),
		metadata.LedgerVersion,
		sourceDigest,
	); err != nil {
		return mapDurationLedgerSQLiteError("store migrated duration ledger metadata", err)
	}
	if err := transaction.Commit(); err != nil {
		return mapDurationLedgerSQLiteError("commit legacy duration ledger migration", err)
	}
	return nil
}

type legacyDurationLedgerMigration struct {
	Generation    uint64
	LedgerVersion int
	Calibration   *DurationCalibration
}

func decodeLegacyDurationLedgerMigration(
	decoder *json.Decoder,
	statement *sql.Stmt,
) (legacyDurationLedgerMigration, error) {
	if err := requireJSONDelimiter(decoder, '{', "legacy duration ledger document"); err != nil {
		return legacyDurationLedgerMigration{}, err
	}
	metadata, seen, err := decodeLegacyDurationLedgerMigrationFields(decoder, statement)
	if err != nil {
		return legacyDurationLedgerMigration{}, err
	}
	if err := requireJSONDelimiter(decoder, '}', "legacy duration ledger document"); err != nil {
		return legacyDurationLedgerMigration{}, err
	}
	if err := requireLegacyDurationLedgerEOF(decoder); err != nil {
		return legacyDurationLedgerMigration{}, err
	}
	if err := validateLegacyDurationLedgerMigration(metadata, seen); err != nil {
		return legacyDurationLedgerMigration{}, err
	}
	return metadata, nil
}

func decodeLegacyDurationLedgerMigrationFields(
	decoder *json.Decoder,
	statement *sql.Stmt,
) (legacyDurationLedgerMigration, map[string]struct{}, error) {
	var metadata legacyDurationLedgerMigration
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		key, err := decodeJSONObjectKey(decoder, seen)
		if err != nil {
			return legacyDurationLedgerMigration{}, nil, err
		}
		switch key {
		case "generation":
			if err := decoder.Decode(&metadata.Generation); err != nil {
				return legacyDurationLedgerMigration{}, nil, fmt.Errorf("decode legacy generation: %w", err)
			}
		case "ledger":
			version, calibration, err := decodeLegacyDurationLedgerBody(decoder, statement)
			if err != nil {
				return legacyDurationLedgerMigration{}, nil, err
			}
			metadata.LedgerVersion = version
			metadata.Calibration = calibration
		default:
			return legacyDurationLedgerMigration{}, nil, fmt.Errorf(
				"legacy duration ledger field %q is unknown",
				key,
			)
		}
	}
	return metadata, seen, nil
}

func requireLegacyDurationLedgerEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("legacy duration ledger has trailing JSON")
		}
		return fmt.Errorf("decode legacy duration ledger trailer: %w", err)
	}
	return nil
}

func validateLegacyDurationLedgerMigration(
	metadata legacyDurationLedgerMigration,
	seen map[string]struct{},
) error {
	if _, ok := seen["generation"]; !ok || metadata.Generation == 0 {
		return errors.New("legacy duration ledger generation is invalid")
	}
	if _, ok := seen["ledger"]; !ok || metadata.LedgerVersion != durationLedgerVersion {
		return errors.New("legacy duration ledger version is invalid")
	}
	if metadata.Calibration != nil {
		if err := ValidateDurationCalibration(*metadata.Calibration); err != nil {
			return fmt.Errorf(
				"validate legacy duration calibration: %w",
				err,
			)
		}
	}
	return nil
}

func decodeLegacyDurationLedgerBody(
	decoder *json.Decoder,
	statement *sql.Stmt,
) (int, *DurationCalibration, error) {
	if err := requireJSONDelimiter(decoder, '{', "legacy duration ledger"); err != nil {
		return 0, nil, err
	}
	version, calibration, seen, err := decodeLegacyDurationLedgerBodyFields(decoder, statement)
	if err != nil {
		return 0, nil, err
	}
	if err := requireJSONDelimiter(decoder, '}', "legacy duration ledger"); err != nil {
		return 0, nil, err
	}
	if err := validateLegacyDurationLedgerBodyFields(seen); err != nil {
		return 0, nil, err
	}
	return version, calibration, nil
}

func decodeLegacyDurationLedgerBodyFields(
	decoder *json.Decoder,
	statement *sql.Stmt,
) (int, *DurationCalibration, map[string]struct{}, error) {
	var version int
	var calibration *DurationCalibration
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		key, err := decodeJSONObjectKey(decoder, seen)
		if err != nil {
			return 0, nil, nil, err
		}
		switch key {
		case "version":
			if err := decoder.Decode(&version); err != nil {
				return 0, nil, nil, fmt.Errorf("decode legacy ledger version: %w", err)
			}
		case "calibration":
			if err := decoder.Decode(&calibration); err != nil {
				return 0, nil, nil, fmt.Errorf("decode legacy calibration: %w", err)
			}
		case "samples":
			if err := decodeLegacyDurationSamples(decoder, statement); err != nil {
				return 0, nil, nil, err
			}
		default:
			return 0, nil, nil, fmt.Errorf("legacy duration ledger field %q is unknown", key)
		}
	}
	return version, calibration, seen, nil
}

func validateLegacyDurationLedgerBodyFields(seen map[string]struct{}) error {
	for _, required := range []string{"version", "samples"} {
		if _, ok := seen[required]; !ok {
			return fmt.Errorf("legacy duration ledger field %q is missing", required)
		}
	}
	return nil
}

func decodeLegacyDurationSamples(decoder *json.Decoder, statement *sql.Stmt) error {
	if err := requireJSONDelimiter(decoder, '[', "legacy duration samples"); err != nil {
		return err
	}
	for index := 0; decoder.More(); index++ {
		var sample DurationSample
		if err := decoder.Decode(&sample); err != nil {
			return fmt.Errorf("decode legacy duration sample %d: %w", index, err)
		}
		if err := ValidateDurationLedger(DurationLedger{
			Version: durationLedgerVersion,
			Samples: []DurationSample{sample},
		}); err != nil {
			return fmt.Errorf("validate legacy duration sample %d: %w", index, err)
		}
		if err := execSQLiteDurationSample(statement, sample); err != nil {
			return err
		}
	}
	return requireJSONDelimiter(decoder, ']', "legacy duration samples")
}

func decodeJSONObjectKey(decoder *json.Decoder, seen map[string]struct{}) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", fmt.Errorf("decode JSON object key: %w", err)
	}
	key, ok := token.(string)
	if !ok {
		return "", errors.New("JSON object key is not a string")
	}
	if _, duplicate := seen[key]; duplicate {
		return "", fmt.Errorf("JSON object field %q is duplicated", key)
	}
	seen[key] = struct{}{}
	return key, nil
}

func requireJSONDelimiter(decoder *json.Decoder, expected rune, context string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode %s delimiter: %w", context, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || rune(delimiter) != expected {
		return fmt.Errorf("%s delimiter must be %q", context, expected)
	}
	return nil
}
