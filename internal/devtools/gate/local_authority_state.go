package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const localAuthorityStateSchemaVersion = 1

type localAuthorityStateProjection struct {
	Domain     string `json:"domain"`
	Generation uint64 `json:"generation"`
}

// LoadLocalAuthorityGeneration 读取已验证的本地代际，不触碰远程 baseline authority。
func (store *DurationLedgerStore) LoadLocalAuthorityGeneration() (uint64, error) {
	if store == nil {
		return 0, errors.New("duration ledger store is nil")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	return loadLocalAuthorityGeneration(database)
}

func loadLocalAuthorityGeneration(database *sql.DB) (uint64, error) {
	if database == nil {
		return 0, errors.New("local authority database is nil")
	}
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("begin local authority generation lookup", err)
	}
	defer transaction.Rollback()
	generation, err := currentLocalAuthorityGeneration(transaction)
	if err != nil {
		return 0, err
	}
	if err := transaction.Commit(); err != nil {
		return 0, mapDurationLedgerSQLiteError("commit local authority generation lookup", err)
	}
	return generation, nil
}

// InitializeLocalAuthority creates only a missing current SQLite authority and
// verifies its local generation. Existing corrupt or unknown-schema files stay
// fail-fast through the strict SQLite initializer.
func (store *DurationLedgerStore) InitializeLocalAuthority() (uint64, error) {
	if store == nil {
		return 0, errors.New("duration ledger store is nil")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return 0, err
	}
	defer database.Close()
	return loadLocalAuthorityGeneration(database)
}

func initializeLocalAuthorityStateOnConnection(connection *sql.Conn, nowFunc func() time.Time) error {
	if nowFunc == nil {
		return errors.New("local authority state clock is required")
	}
	var stateJSON string
	err := connection.QueryRowContext(context.Background(), `SELECT state_json FROM ci_local_authority_state WHERE singleton = 1`).Scan(&stateJSON)
	if err == nil {
		return validateLocalAuthorityStateProjection(stateJSON, "")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return mapDurationLedgerSQLiteError("load local authority state during initialization", err)
	}
	stateJSON, stateSHA256, err := encodeLocalAuthorityStateProjection(1)
	if err != nil {
		return err
	}
	if _, err := connection.ExecContext(context.Background(), `INSERT INTO ci_local_authority_state(singleton, schema_version, generation, state_json, state_sha256, updated_at_unix_ms) VALUES(1, ?, ?, ?, ?, ?)`, localAuthorityStateSchemaVersion, "1", stateJSON, stateSHA256, nowFunc().UTC().UnixMilli()); err != nil {
		return mapDurationLedgerSQLiteError("initialize local authority state", err)
	}
	return nil
}

func encodeLocalAuthorityStateProjection(generation uint64) (string, string, error) {
	if generation == 0 {
		return "", "", errors.New("local authority generation is required")
	}
	payload, err := json.Marshal(localAuthorityStateProjection{Domain: "local-authority-state/v1", Generation: generation})
	if err != nil {
		return "", "", fmt.Errorf("encode local authority state projection: %w", err)
	}
	digest := sha256.Sum256(payload)
	return string(payload), fmt.Sprintf("sha256:%x", digest), nil
}

func currentLocalAuthorityGeneration(transaction *sql.Tx) (uint64, error) {
	var schemaVersion int
	var generation, stateJSON, stateSHA256 string
	err := transaction.QueryRow(`SELECT schema_version, generation, state_json, state_sha256 FROM ci_local_authority_state WHERE singleton = 1`).Scan(&schemaVersion, &generation, &stateJSON, &stateSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("local authority state is missing")
	}
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("load local authority generation", err)
	}
	if schemaVersion != localAuthorityStateSchemaVersion {
		return 0, errors.New("local authority state schema version is invalid")
	}
	parsed, err := parseCanonicalAuthorityGeneration(generation)
	if err != nil {
		return 0, fmt.Errorf("local authority generation: %w", err)
	}
	if err := validateLocalAuthorityStateProjection(stateJSON, stateSHA256); err != nil {
		return 0, err
	}
	var projection localAuthorityStateProjection
	if err := decodeLocalAuthorityStateProjection(stateJSON, &projection); err != nil {
		return 0, err
	}
	if projection.Generation != parsed {
		return 0, errors.New("local authority state generation projection does not match row")
	}
	return parsed, nil
}

func requireLocalAuthorityGeneration(transaction *sql.Tx, generation uint64) error {
	if generation == 0 {
		return errors.New("local authority generation is required")
	}
	current, err := currentLocalAuthorityGeneration(transaction)
	if err != nil {
		return err
	}
	if generation != current {
		return fmt.Errorf("local authority generation %d is not current; current generation is %d", generation, current)
	}
	return nil
}

func verifyLocalAuthorityStateDatabase(database *sql.DB) error {
	if database == nil {
		return errors.New("local authority state database is nil")
	}
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return mapDurationLedgerSQLiteError("begin local authority state verification", err)
	}
	defer transaction.Rollback()
	if _, err := currentLocalAuthorityGeneration(transaction); err != nil {
		return err
	}
	return transaction.Commit()
}

func validateLocalAuthorityStateProjection(stateJSON, expectedSHA256 string) error {
	if strings.TrimSpace(stateJSON) == "" {
		return errors.New("local authority state projection is empty")
	}
	if expectedSHA256 != "" {
		digest := sha256.Sum256([]byte(stateJSON))
		if expectedSHA256 != fmt.Sprintf("sha256:%x", digest) {
			return errors.New("local authority state projection digest does not match")
		}
	}
	var projection localAuthorityStateProjection
	if err := decodeLocalAuthorityStateProjection(stateJSON, &projection); err != nil {
		return err
	}
	if projection.Domain != "local-authority-state/v1" || projection.Generation == 0 {
		return errors.New("local authority state projection is invalid")
	}
	canonical, _, err := encodeLocalAuthorityStateProjection(projection.Generation)
	if err != nil {
		return err
	}
	if canonical != stateJSON {
		return errors.New("local authority state projection is not canonical")
	}
	return nil
}

func decodeLocalAuthorityStateProjection(value string, projection *localAuthorityStateProjection) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(projection); err != nil {
		return fmt.Errorf("decode local authority state projection: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("local authority state projection has trailing JSON")
		}
		return fmt.Errorf("decode local authority state trailing JSON: %w", err)
	}
	return nil
}

func parseCanonicalAuthorityGeneration(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || value != strconv.FormatUint(parsed, 10) {
		return 0, errors.New("generation must be a canonical positive uint64")
	}
	return parsed, nil
}
