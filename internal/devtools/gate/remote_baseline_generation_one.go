package gate

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

var ErrRemoteBaselineGenerationOneAlreadyInitialized = errors.New("remote baseline generation one is already initialized")

// InitializeRemoteBaselineGenerationOne 消费外部 ECI 回执并执行唯一首代 accepted INSERT。
func (store *DurationLedgerStore) InitializeRemoteBaselineGenerationOne(receiptJSON []byte) (RemoteBaselineStateRecord, error) {
	if store == nil {
		return RemoteBaselineStateRecord{}, errors.New("duration ledger store is nil")
	}
	receipt, err := cicontract.DecodeGenerationOneProvisionReceipt(receiptJSON)
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	state, err := cicontract.DecodeAcceptedBaselineState(receipt.StateJSON)
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	if err := cicontract.ValidateGenerationOneProvisionStateBinding(receipt, state); err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	database, err := openGenerationOneDatabase(store)
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	defer database.Close()
	return insertGenerationOneState(store, database, receipt, state)
}

// openGenerationOneDatabase 打开已经完成 schema migration 的 SQLite authority。
func openGenerationOneDatabase(store *DurationLedgerStore) (*sql.DB, error) {
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return nil, err
	}
	return database, nil
}

// insertGenerationOneState 在同一 SQLite 写事务中提交首代 singleton。
func insertGenerationOneState(store *DurationLedgerStore, database *sql.DB, receipt cicontract.GenerationOneProvisionReceipt, state cicontract.AcceptedBaselineState) (RemoteBaselineStateRecord, error) {
	var record RemoteBaselineStateRecord
	err := withSQLiteWriteTransaction(database, "initialize remote baseline generation one", func(transaction *sql.Tx) error {
		return insertGenerationOneStateTransaction(store, transaction, receipt, state, &record)
	})
	if err != nil {
		return RemoteBaselineStateRecord{}, err
	}
	return record, nil
}

// insertGenerationOneStateTransaction 检查空表前置条件并执行唯一 INSERT。
func insertGenerationOneStateTransaction(store *DurationLedgerStore, transaction *sql.Tx, receipt cicontract.GenerationOneProvisionReceipt, state cicontract.AcceptedBaselineState, record *RemoteBaselineStateRecord) error {
	existing, err := loadGenerationOneExistingState(transaction, receipt)
	if err != nil {
		return err
	}
	if existing != nil {
		if err := validateGenerationOneDurationLedgerMetadata(transaction, receipt.Generation); err != nil {
			return err
		}
		*record = *existing
		return nil
	}
	if err := ensureGenerationOneDurationLedgerMetadata(transaction, receipt.Generation); err != nil {
		return err
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal accepted baseline generation one: %w", err)
	}
	if !bytes.Equal(stateJSON, receipt.StateJSON) {
		return errors.New("accepted baseline generation one state drifted from receipt")
	}
	stateSHA256 := receipt.StateSHA256
	_, err = transaction.Exec(`INSERT INTO ci_remote_baseline_state(singleton,schema_version,generation,state_json,state_sha256,updated_at_unix_ms) VALUES(1,3,?,?,?,?)`,
		strconv.FormatUint(receipt.Generation, 10), string(stateJSON), stateSHA256, store.nowFunc().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("insert remote baseline generation one: %w", err)
	}
	*record = RemoteBaselineStateRecord{Generation: receipt.Generation, StateJSON: append([]byte(nil), stateJSON...), StateSHA256: stateSHA256}
	return nil
}

// ensureGenerationOneDurationLedgerMetadata 在首代 accepted 同一事务中补齐空账本元数据，并拒绝冲突 authority。
func ensureGenerationOneDurationLedgerMetadata(transaction *sql.Tx, generation uint64) error {
	currentGeneration, found, err := sqliteCurrentGeneration(transaction)
	if err != nil {
		return err
	}
	if found {
		if currentGeneration != generation {
			return errors.New("generation-one duration ledger metadata generation conflicts with the receipt")
		}
		var schemaVersion, ledgerVersion int
		if err := transaction.QueryRow(`SELECT schema_version, ledger_version FROM duration_ledger_meta WHERE singleton=1`).Scan(&schemaVersion, &ledgerVersion); err != nil {
			return fmt.Errorf("validate generation-one duration ledger metadata: %w", err)
		}
		if schemaVersion != 1 || ledgerVersion != durationLedgerVersion {
			return errors.New("generation-one duration ledger metadata schema is invalid")
		}
		return ensureGenerationOneAuthorityHistoryEmpty(transaction)
	}
	if err := ensureGenerationOneAuthorityHistoryEmpty(transaction); err != nil {
		return err
	}
	_, err = transaction.Exec(`INSERT INTO duration_ledger_meta(singleton,authority_id,schema_version,generation,ledger_version) VALUES(1,?,1,?,?)`,
		cicontract.SQLAuthorityID, strconv.FormatUint(generation, 10), durationLedgerVersion)
	if err != nil {
		return fmt.Errorf("initialize generation-one duration ledger metadata: %w", err)
	}
	return nil
}

// validateGenerationOneDurationLedgerMetadata 严格重读已存在的首代元数据，不要求历史根仍为空。
func validateGenerationOneDurationLedgerMetadata(transaction *sql.Tx, generation uint64) error {
	currentGeneration, found, err := sqliteCurrentGeneration(transaction)
	if err != nil {
		return err
	}
	if !found {
		return ErrDurationLedgerMetadataMissing
	}
	if currentGeneration != generation {
		return errors.New("generation-one duration ledger metadata generation does not match the receipt")
	}
	var authorityID string
	var schemaVersion, ledgerVersion int
	if err := transaction.QueryRow(`SELECT authority_id, schema_version, ledger_version FROM duration_ledger_meta WHERE singleton=1`).Scan(&authorityID, &schemaVersion, &ledgerVersion); err != nil {
		return fmt.Errorf("validate generation-one duration ledger metadata: %w", err)
	}
	if authorityID != cicontract.SQLAuthorityID || schemaVersion != 1 || ledgerVersion != durationLedgerVersion {
		return errors.New("generation-one duration ledger metadata schema is invalid")
	}
	return nil
}

// ensureGenerationOneAuthorityHistoryEmpty 只允许 schema-only 空库建立首代元数据。
func ensureGenerationOneAuthorityHistoryEmpty(transaction *sql.Tx) error {
	for _, table := range generationOneAuthorityEmptyTableNames() {
		var found bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s LIMIT 1)", table)
		if err := transaction.QueryRow(query).Scan(&found); err != nil {
			return fmt.Errorf("check generation-one history table %s: %w", table, err)
		}
		if found {
			return fmt.Errorf("generation-one SQLite contains orphan history in %s", table)
		}
	}
	return nil
}

// generationOneAuthorityEmptyTableNames 从 cicontract 唯一 owner 派生七个历史根，
// 再追加首代 schema-only 所需为空的 supporting tables，拒绝漂移的第二根列表。
func generationOneAuthorityEmptyTableNames() []string {
	bindings := cicontract.RetentionRootBindings()
	tables := make([]string, 0, len(bindings)+len(cicontract.GenerationOneAuthoritySupportingTables()))
	for _, binding := range bindings {
		tables = append(tables, binding.Table)
	}
	return append(tables, cicontract.GenerationOneAuthoritySupportingTables()...)
}

// loadGenerationOneExistingState 严格重读已有 singleton；同一 state digest 幂等返回，异态继续阻断。
func loadGenerationOneExistingState(transaction *sql.Tx, receipt cicontract.GenerationOneProvisionReceipt) (*RemoteBaselineStateRecord, error) {
	var (
		singleton, schemaVersion           int
		generation, stateJSON, stateSHA256 string
	)
	err := transaction.QueryRow(`SELECT singleton, schema_version, generation, state_json, state_sha256 FROM ci_remote_baseline_state WHERE singleton=1`).Scan(&singleton, &schemaVersion, &generation, &stateJSON, &stateSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read remote baseline generation one existing state: %w", err)
	}
	if singleton != 1 || schemaVersion != 3 {
		return nil, errors.New("remote baseline generation one singleton is invalid")
	}
	parsedGeneration, parseErr := strconv.ParseUint(generation, 10, 64)
	if parseErr != nil || parsedGeneration == 0 {
		return nil, errors.New("remote baseline generation one stored generation is invalid")
	}
	if parsedGeneration != receipt.Generation || stateSHA256 != receipt.StateSHA256 || !bytes.Equal([]byte(stateJSON), receipt.StateJSON) {
		return nil, fmt.Errorf("%w: stored accepted state differs from receipt", ErrRemoteBaselineGenerationOneAlreadyInitialized)
	}
	return &RemoteBaselineStateRecord{
		Generation:  parsedGeneration,
		StateJSON:   []byte(stateJSON),
		StateSHA256: stateSHA256,
	}, nil
}
