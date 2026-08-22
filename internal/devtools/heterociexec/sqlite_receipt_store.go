package heterociexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

const (
	receiptRowSchema     = "heteroci-batch-receipt/v1"
	receiptClaimSchema   = "heteroci-batch-claim/v1"
	receiptStateClaimed  = "claimed"
	receiptStateTerminal = "terminal"
)

const selectSQLiteReceiptRow = `SELECT schema_version, command_digest, state, claim_json, claim_digest, result_json, result_digest
FROM heteroci_batch_receipts WHERE batch_id = ? AND material_digest = ?`

type receiptClaim struct {
	SchemaVersion  string                       `json:"schema_version"`
	BatchID        heterociworkflow.BatchID     `json:"batch_id"`
	RunID          string                       `json:"run_id"`
	Executor       heterociworkflow.ExecutorKey `json:"executor"`
	MaterialDigest string                       `json:"material_digest"`
	CommandDigest  string                       `json:"command_digest"`
	UnitIDs        []string                     `json:"unit_ids"`
}

type receiptRow struct {
	SchemaVersion string
	CommandDigest string
	State         string
	ClaimJSON     []byte
	ClaimDigest   string
	ResultJSON    []byte
	ResultDigest  sql.NullString
}

type receiptQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Claim 原子创建 batch_id+material_digest claim；既有 command 漂移必须失败。
func (store *SQLiteReceiptStore) Claim(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (bool, error) {
	claim, claimJSON, claimDigest, err := prepareReceiptClaim(ctx, batch, commandDigest)
	if err != nil {
		return false, err
	}
	database, err := store.databaseHandle(ctx)
	if err != nil {
		return false, err
	}
	claimed := false
	err = platformdb.WithImmediateTx(ctx, database, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO heteroci_batch_receipts(
schema_version, batch_id, material_digest, command_digest, state, claim_json, claim_digest, result_json, result_digest)
VALUES (?, ?, ?, ?, ?, ?, ?, NULL, NULL)
ON CONFLICT(batch_id, material_digest) DO NOTHING`, receiptRowSchema, claim.BatchID, claim.MaterialDigest, claim.CommandDigest, receiptStateClaimed, claimJSON, claimDigest)
		if err != nil {
			return fmt.Errorf("%w: insert claim: %w", ErrSQLiteReceiptStore, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("%w: inspect claim insert: %w", ErrSQLiteReceiptStore, err)
		}
		switch rows {
		case 1:
			claimed = true
			return nil
		case 0:
			persisted, found, err := readSQLiteReceiptRow(ctx, tx, batch.BatchID, batch.MaterialDigest)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%w: conflicting claim disappeared", ErrSQLiteReceiptStore)
			}
			return validateReceiptClaimRow(persisted, claim, claimJSON, claimDigest)
		default:
			return fmt.Errorf("%w: claim insert affected %d rows", ErrSQLiteReceiptStore, rows)
		}
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}

// Read 只读取同一完整 batch material 与 command digest 的 terminal receipt。
func (store *SQLiteReceiptStore) Read(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (heterociworkflow.BatchResult, bool, error) {
	claim, claimJSON, claimDigest, err := prepareReceiptClaim(ctx, batch, commandDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	database, err := store.databaseHandle(ctx)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	row, found, err := readSQLiteReceiptRow(ctx, database, batch.BatchID, batch.MaterialDigest)
	if err != nil || !found {
		return heterociworkflow.BatchResult{}, false, err
	}
	if err := validateReceiptClaimRow(row, claim, claimJSON, claimDigest); err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	if row.State == receiptStateClaimed {
		return heterociworkflow.BatchResult{}, false, nil
	}
	if row.State != receiptStateTerminal {
		return heterociworkflow.BatchResult{}, false, fmt.Errorf("%w: receipt state is invalid", ErrSQLiteReceiptStore)
	}
	result, err := decodeBatchResult(row.ResultJSON, row.ResultDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	if err := validateBatchResult(batch, commandDigest, result); err != nil {
		return heterociworkflow.BatchResult{}, false, errors.Join(ErrSQLiteReceiptStore, err)
	}
	return cloneBatchResult(reorderBatchResult(batch, result)), true, nil
}

// WriteIfAbsent 只允许已存在 claim 原子迁移为唯一 terminal receipt。
func (store *SQLiteReceiptStore) WriteIfAbsent(ctx context.Context, result heterociworkflow.BatchResult) error {
	resultJSON, resultDigest, err := prepareBatchResult(ctx, result)
	if err != nil {
		return err
	}
	database, err := store.databaseHandle(ctx)
	if err != nil {
		return err
	}
	return platformdb.WithImmediateTx(ctx, database, func(tx *sql.Tx) error {
		return writeSQLiteReceiptTx(ctx, tx, result, resultJSON, resultDigest)
	})
}

// writeSQLiteReceiptTx 在 immediate 事务中执行 claim→terminal 的唯一 CAS。
func writeSQLiteReceiptTx(ctx context.Context, tx *sql.Tx, result heterociworkflow.BatchResult, resultJSON []byte, resultDigest string) error {
	row, err := loadSQLiteReceiptForWrite(ctx, tx, result)
	if err != nil {
		return err
	}
	return finishSQLiteReceiptWrite(ctx, tx, row, result, resultJSON, resultDigest)
}

// loadSQLiteReceiptForWrite 读取并校验待写入结果对应的唯一 claim。
func loadSQLiteReceiptForWrite(ctx context.Context, tx *sql.Tx, result heterociworkflow.BatchResult) (receiptRow, error) {
	row, found, err := readSQLiteReceiptRow(ctx, tx, result.BatchID, result.MaterialDigest)
	if err != nil {
		return receiptRow{}, err
	}
	if !found {
		return receiptRow{}, fmt.Errorf("%w: terminal receipt has no claim", ErrSQLiteReceiptConflict)
	}
	claim, err := decodeReceiptClaim(row.ClaimJSON, row.ClaimDigest)
	if err != nil {
		return receiptRow{}, err
	}
	if row.SchemaVersion != receiptRowSchema || row.CommandDigest != claim.CommandDigest {
		return receiptRow{}, fmt.Errorf("%w: persisted claim envelope drifted", ErrSQLiteReceiptStore)
	}
	if err := validateResultAgainstClaim(result, claim); err != nil {
		return receiptRow{}, err
	}
	return row, nil
}

// finishSQLiteReceiptWrite 只允许 claimed→terminal，或确认完全相同的幂等重写。
func finishSQLiteReceiptWrite(ctx context.Context, tx *sql.Tx, row receiptRow, result heterociworkflow.BatchResult, resultJSON []byte, resultDigest string) error {
	if row.State == receiptStateClaimed {
		return writeSQLiteTerminalReceipt(ctx, tx, result, resultJSON, resultDigest)
	}
	if row.State != receiptStateTerminal {
		return fmt.Errorf("%w: receipt state is invalid", ErrSQLiteReceiptStore)
	}
	persisted, err := decodeBatchResult(row.ResultJSON, row.ResultDigest)
	if err != nil {
		return err
	}
	if !bytes.Equal(row.ResultJSON, resultJSON) || row.ResultDigest.String != resultDigest || !sameBatchResult(persisted, result) {
		return fmt.Errorf("%w: terminal receipt already differs", ErrSQLiteReceiptConflict)
	}
	return nil
}

// prepareReceiptClaim 校验完整 batch 后生成唯一 canonical claim 材料。
func prepareReceiptClaim(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (receiptClaim, []byte, string, error) {
	if err := validateSQLiteReceiptContext(ctx); err != nil {
		return receiptClaim{}, nil, "", err
	}
	if _, err := validateExecutionBatch(batch); err != nil {
		return receiptClaim{}, nil, "", errors.Join(ErrSQLiteReceiptStore, err)
	}
	if !validReceiptDigest(commandDigest) {
		return receiptClaim{}, nil, "", fmt.Errorf("%w: command digest is invalid", ErrSQLiteReceiptStore)
	}
	claim := receiptClaim{
		SchemaVersion:  receiptClaimSchema,
		BatchID:        batch.BatchID,
		RunID:          batch.RunID,
		Executor:       batch.Key,
		MaterialDigest: batch.MaterialDigest,
		CommandDigest:  commandDigest,
		UnitIDs:        make([]string, len(batch.Units)),
	}
	for index, unit := range batch.Units {
		claim.UnitIDs[index] = unit.UnitID
	}
	encoded, err := json.Marshal(claim)
	if err != nil {
		return receiptClaim{}, nil, "", fmt.Errorf("%w: encode claim: %w", ErrSQLiteReceiptStore, err)
	}
	return claim, encoded, digestReceiptBytes(encoded), nil
}

// prepareBatchResult 校验 terminal envelope 并生成 canonical JSON 与摘要。
func prepareBatchResult(ctx context.Context, result heterociworkflow.BatchResult) ([]byte, string, error) {
	if err := validateSQLiteReceiptContext(ctx); err != nil {
		return nil, "", err
	}
	if err := validateBatchResultShape(result); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode terminal receipt: %w", ErrSQLiteReceiptStore, err)
	}
	return encoded, digestReceiptBytes(encoded), nil
}

func writeSQLiteTerminalReceipt(ctx context.Context, tx *sql.Tx, result heterociworkflow.BatchResult, resultJSON []byte, resultDigest string) error {
	updated, err := tx.ExecContext(ctx, `UPDATE heteroci_batch_receipts
SET state = ?, result_json = ?, result_digest = ?
WHERE batch_id = ? AND material_digest = ? AND command_digest = ? AND state = ? AND result_json IS NULL AND result_digest IS NULL`, receiptStateTerminal, resultJSON, resultDigest, result.BatchID, result.MaterialDigest, result.CommandDigest, receiptStateClaimed)
	if err != nil {
		return fmt.Errorf("%w: write terminal receipt: %w", ErrSQLiteReceiptStore, err)
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: inspect terminal CAS: %w", ErrSQLiteReceiptStore, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: terminal CAS affected %d rows", ErrSQLiteReceiptConflict, rows)
	}
	return nil
}

func readSQLiteReceiptRow(ctx context.Context, queryer receiptQueryer, batchID heterociworkflow.BatchID, materialDigest string) (receiptRow, bool, error) {
	var row receiptRow
	err := queryer.QueryRowContext(ctx, selectSQLiteReceiptRow, batchID, materialDigest).Scan(&row.SchemaVersion, &row.CommandDigest, &row.State, &row.ClaimJSON, &row.ClaimDigest, &row.ResultJSON, &row.ResultDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return receiptRow{}, false, nil
	}
	if err != nil {
		return receiptRow{}, false, fmt.Errorf("%w: read receipt row: %w", ErrSQLiteReceiptStore, err)
	}
	return row, true, nil
}

// validateReceiptClaimRow 将数据库 claim 与当前完整 batch 材料逐字段比对。
func validateReceiptClaimRow(row receiptRow, want receiptClaim, wantJSON []byte, wantDigest string) error {
	if row.SchemaVersion != receiptRowSchema {
		return fmt.Errorf("%w: row schema drifted", ErrSQLiteReceiptStore)
	}
	if row.CommandDigest != want.CommandDigest {
		return fmt.Errorf("%w: command digest drifted", ErrSQLiteReceiptConflict)
	}
	if row.ClaimDigest != wantDigest || !bytes.Equal(row.ClaimJSON, wantJSON) {
		return fmt.Errorf("%w: claim material drifted", ErrSQLiteReceiptStore)
	}
	persisted, err := decodeReceiptClaim(row.ClaimJSON, row.ClaimDigest)
	if err != nil {
		return err
	}
	if !sameReceiptClaim(persisted, want) {
		return fmt.Errorf("%w: decoded claim material drifted", ErrSQLiteReceiptStore)
	}
	return validateReceiptRowState(row)
}

// validateReceiptRowState 保证 claimed 与 terminal 两种持久化状态互斥且完整。
func validateReceiptRowState(row receiptRow) error {
	switch row.State {
	case receiptStateClaimed:
		if row.ResultJSON != nil || row.ResultDigest.Valid {
			return fmt.Errorf("%w: claimed row carries terminal result", ErrSQLiteReceiptStore)
		}
	case receiptStateTerminal:
		if row.ResultJSON == nil || !row.ResultDigest.Valid {
			return fmt.Errorf("%w: terminal row lacks result", ErrSQLiteReceiptStore)
		}
	default:
		return fmt.Errorf("%w: receipt state is invalid", ErrSQLiteReceiptStore)
	}
	return nil
}

// decodeReceiptClaim 校验 claim 摘要、canonical JSON 和身份字段。
func decodeReceiptClaim(raw []byte, digest string) (receiptClaim, error) {
	if len(raw) == 0 || !validReceiptDigest(digest) || digestReceiptBytes(raw) != digest {
		return receiptClaim{}, fmt.Errorf("%w: claim digest mismatch", ErrSQLiteReceiptStore)
	}
	var claim receiptClaim
	if err := decodeCanonicalReceiptJSON(raw, &claim); err != nil {
		return receiptClaim{}, fmt.Errorf("%w: decode claim: %w", ErrSQLiteReceiptStore, err)
	}
	if err := validateReceiptClaimShape(claim); err != nil {
		return receiptClaim{}, err
	}
	return claim, nil
}

// decodeBatchResult 校验 terminal receipt 的摘要、canonical JSON 和结果形状。
func decodeBatchResult(raw []byte, digest sql.NullString) (heterociworkflow.BatchResult, error) {
	if len(raw) == 0 || !digest.Valid || !validReceiptDigest(digest.String) || digestReceiptBytes(raw) != digest.String {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: terminal receipt digest mismatch", ErrSQLiteReceiptStore)
	}
	var result heterociworkflow.BatchResult
	if err := decodeCanonicalReceiptJSON(raw, &result); err != nil {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: decode terminal receipt: %w", ErrSQLiteReceiptStore, err)
	}
	if err := validateBatchResultShape(result); err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	return result, nil
}

func decodeCanonicalReceiptJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON is invalid")
	}
	canonical, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, canonical) {
		return fmt.Errorf("JSON is not canonical")
	}
	return nil
}

// validateReceiptClaimShape 校验 claim 的 identity、digest 和有序 unit 集合。
func validateReceiptClaimShape(claim receiptClaim) error {
	if err := validateReceiptClaimIdentity(claim); err != nil {
		return err
	}
	return validateReceiptClaimUnits(claim.UnitIDs)
}

// validateReceiptClaimIdentity 校验 claim 的 schema、run、executor 与摘要字段。
func validateReceiptClaimIdentity(claim receiptClaim) error {
	if claim.SchemaVersion != receiptClaimSchema || !validCommandBatchID(claim.BatchID) || !validReceiptIdentity(claim.RunID) {
		return fmt.Errorf("%w: claim identity is invalid", ErrSQLiteReceiptStore)
	}
	if err := claim.Executor.Validate(); err != nil {
		return fmt.Errorf("%w: claim executor is invalid", ErrSQLiteReceiptStore)
	}
	if !validReceiptDigest(claim.MaterialDigest) || !validReceiptDigest(claim.CommandDigest) || len(claim.UnitIDs) == 0 {
		return fmt.Errorf("%w: claim digest or units are invalid", ErrSQLiteReceiptStore)
	}
	return nil
}

// validateReceiptClaimUnits 拒绝空白或重复 unit，保持 claim 顺序可重放。
func validateReceiptClaimUnits(unitIDs []string) error {
	seen := make(map[string]struct{}, len(unitIDs))
	for _, unitID := range unitIDs {
		if !validReceiptIdentity(unitID) {
			return fmt.Errorf("%w: claim unit identity is invalid", ErrSQLiteReceiptStore)
		}
		if _, duplicate := seen[unitID]; duplicate {
			return fmt.Errorf("%w: claim unit identity is duplicated", ErrSQLiteReceiptStore)
		}
		seen[unitID] = struct{}{}
	}
	return nil
}

// validateBatchResultShape 校验 terminal 结果的身份、状态和完整 coverage。
func validateBatchResultShape(result heterociworkflow.BatchResult) error {
	if err := validateResultIdentity(result); err != nil {
		return err
	}
	return validateReceiptOutcomes(result.Outcomes)
}

// validateResultIdentity 校验 terminal 结果的 batch、executor、摘要和 authority header。
func validateResultIdentity(result heterociworkflow.BatchResult) error {
	if !validCommandBatchID(result.BatchID) || !validReceiptIdentity(result.RunID) {
		return fmt.Errorf("%w: terminal result identity is invalid", ErrSQLiteReceiptStore)
	}
	if err := result.Executor.Validate(); err != nil {
		return fmt.Errorf("%w: terminal executor is invalid", ErrSQLiteReceiptStore)
	}
	if !validReceiptDigest(result.MaterialDigest) || !validReceiptDigest(result.CommandDigest) {
		return fmt.Errorf("%w: terminal digest is invalid", ErrSQLiteReceiptStore)
	}
	if err := validateReceiptHeader(result); err != nil {
		return errors.Join(ErrSQLiteReceiptStore, err)
	}
	return nil
}

// validateReceiptOutcomes 校验 terminal outcome 的状态、唯一性和非空覆盖。
func validateReceiptOutcomes(outcomes []heterociworkflow.BatchOutcome) error {
	if len(outcomes) == 0 {
		return fmt.Errorf("%w: terminal outcomes are empty", ErrSQLiteReceiptStore)
	}
	seen := make(map[string]struct{}, len(outcomes))
	for _, outcome := range outcomes {
		if !validReceiptIdentity(outcome.UnitID) || (outcome.State != heteroci.LeaseStateSucceeded && outcome.State != heteroci.LeaseStateFailed) {
			return fmt.Errorf("%w: terminal outcome is invalid", ErrSQLiteReceiptStore)
		}
		if _, duplicate := seen[outcome.UnitID]; duplicate {
			return fmt.Errorf("%w: terminal outcome is duplicated", ErrSQLiteReceiptStore)
		}
		seen[outcome.UnitID] = struct{}{}
	}
	return nil
}

// validateResultAgainstClaim 确保 terminal 结果只能完成同一 claim 的 unit 顺序。
func validateResultAgainstClaim(result heterociworkflow.BatchResult, claim receiptClaim) error {
	if result.BatchID != claim.BatchID || result.RunID != claim.RunID || result.Executor != claim.Executor || result.MaterialDigest != claim.MaterialDigest || result.CommandDigest != claim.CommandDigest {
		return fmt.Errorf("%w: terminal result identity drifted", ErrSQLiteReceiptConflict)
	}
	if len(result.Outcomes) != len(claim.UnitIDs) {
		return fmt.Errorf("%w: terminal outcome coverage drifted", ErrSQLiteReceiptConflict)
	}
	for index, outcome := range result.Outcomes {
		if outcome.UnitID != claim.UnitIDs[index] {
			return fmt.Errorf("%w: terminal outcome order drifted", ErrSQLiteReceiptConflict)
		}
	}
	return nil
}

// sameReceiptClaim 比较 claim 的完整 canonical identity，避免只比较主键。
func sameReceiptClaim(left, right receiptClaim) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.BatchID == right.BatchID &&
		left.RunID == right.RunID &&
		left.Executor == right.Executor &&
		left.MaterialDigest == right.MaterialDigest &&
		left.CommandDigest == right.CommandDigest &&
		slices.Equal(left.UnitIDs, right.UnitIDs)
}

func validReceiptIdentity(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	return true
}

// validReceiptDigest 校验 sha256: 前缀和 64 位小写十六进制摘要。
func validReceiptDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func digestReceiptBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
