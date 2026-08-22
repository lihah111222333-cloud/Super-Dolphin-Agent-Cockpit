package heterociexec

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

const postgreSQLReceiptMutationAttempts = 3

const (
	selectPostgreSQLReceiptRow = `SELECT schema_version, command_digest, state, claim_bytes, claim_digest, result_bytes, result_digest
FROM heteroci_receipt.batch_receipts WHERE batch_id = $1 AND material_digest = $2`
	insertPostgreSQLReceiptClaim = `INSERT INTO heteroci_receipt.batch_receipts(
schema_version, batch_id, material_digest, command_digest, state, claim_bytes, claim_digest, result_bytes, result_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, NULL)
ON CONFLICT (batch_id, material_digest) DO NOTHING`
	updatePostgreSQLTerminalReceipt = `UPDATE heteroci_receipt.batch_receipts
SET state = $1, result_bytes = $2, result_digest = $3
WHERE batch_id = $4 AND material_digest = $5 AND command_digest = $6 AND state = $7 AND result_bytes IS NULL AND result_digest IS NULL`
)

// Claim 原子创建 batch/material/command claim；完全相同的重放返回 false。
func (store *PostgreSQLReceiptStore) Claim(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (bool, error) {
	claim, claimBytes, claimDigest, err := prepareCanonicalReceiptClaim(ctx, batch, commandDigest)
	if err != nil {
		return false, postgreSQLReceiptDomainError(err)
	}
	for range postgreSQLReceiptMutationAttempts {
		claimed, ambiguous, err := store.claimOnce(ctx, claim, claimBytes, claimDigest)
		if err == nil {
			return claimed, nil
		}
		if !ambiguous {
			if postgreSQLReceiptRetryable(err) {
				continue
			}
			return false, err
		}
		resolved, retry, replayErr := store.reconcileClaim(ctx, claim, claimBytes, claimDigest, claimed)
		if replayErr != nil {
			return false, replayErr
		}
		if !retry {
			return resolved, nil
		}
	}
	return false, fmt.Errorf("%w: claim retry budget exhausted", ErrPostgreSQLReceiptStore)
}

func (store *PostgreSQLReceiptStore) claimOnce(ctx context.Context, claim canonicalReceiptClaim, claimBytes []byte, claimDigest string) (bool, bool, error) {
	tx, err := store.beginTransaction(ctx)
	if err != nil {
		return false, false, err
	}
	claimed, err := insertPostgreSQLClaim(ctx, tx, claim, claimBytes, claimDigest)
	if err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return claimed, true, fmt.Errorf("%w: claim commit result is ambiguous: %w", ErrPostgreSQLReceiptStore, err)
	}
	return claimed, false, nil
}

func insertPostgreSQLClaim(ctx context.Context, tx *sql.Tx, claim canonicalReceiptClaim, claimBytes []byte, claimDigest string) (bool, error) {
	result, err := tx.ExecContext(ctx, insertPostgreSQLReceiptClaim, canonicalReceiptRowSchema, claim.BatchID, claim.MaterialDigest, claim.CommandDigest, canonicalReceiptStateClaimed, claimBytes, claimDigest)
	if err != nil {
		return false, fmt.Errorf("%w: insert claim: %w", ErrPostgreSQLReceiptStore, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%w: inspect claim insert: %w", ErrPostgreSQLReceiptStore, err)
	}
	if rows == 1 {
		return true, nil
	}
	if rows != 0 {
		return false, fmt.Errorf("%w: claim insert affected %d rows", ErrPostgreSQLReceiptStore, rows)
	}
	row, found, err := readPostgreSQLReceiptRow(ctx, tx, claim.BatchID, claim.MaterialDigest, " FOR SHARE")
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("%w: conflicting claim disappeared", ErrPostgreSQLReceiptStore)
	}
	if err := validateCanonicalReceiptClaimRow(row, claim, claimBytes, claimDigest); err != nil {
		return false, postgreSQLReceiptDomainError(err)
	}
	return false, nil
}

func (store *PostgreSQLReceiptStore) reconcileClaim(ctx context.Context, claim canonicalReceiptClaim, claimBytes []byte, claimDigest string, claimed bool) (bool, bool, error) {
	tx, err := store.beginTransaction(ctx)
	if err != nil {
		return false, false, err
	}
	row, found, err := readPostgreSQLReceiptRow(ctx, tx, claim.BatchID, claim.MaterialDigest, " FOR SHARE")
	if err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if found {
		if err := validateCanonicalReceiptClaimRow(row, claim, claimBytes, claimDigest); err != nil {
			return false, false, rollbackPostgreSQLReceiptTransaction(tx, postgreSQLReceiptDomainError(err))
		}
	}
	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("%w: claim replay commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	if !found {
		return false, true, nil
	}
	return claimed, false, nil
}

// Read 只从已验证 primary 读取同一完整 batch/material/command 的 terminal receipt。
func (store *PostgreSQLReceiptStore) Read(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (heterociworkflow.BatchResult, bool, error) {
	claim, claimBytes, claimDigest, err := prepareCanonicalReceiptClaim(ctx, batch, commandDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, postgreSQLReceiptDomainError(err)
	}
	tx, err := store.beginTransaction(ctx)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	result, found, err := readPostgreSQLTerminal(ctx, tx, batch, commandDigest, claim, claimBytes, claimDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return heterociworkflow.BatchResult{}, false, fmt.Errorf("%w: read commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	return result, found, nil
}

func readPostgreSQLTerminal(ctx context.Context, tx *sql.Tx, batch heterociworkflow.ExecutionBatch, commandDigest string, claim canonicalReceiptClaim, claimBytes []byte, claimDigest string) (heterociworkflow.BatchResult, bool, error) {
	row, found, err := readPostgreSQLReceiptRow(ctx, tx, batch.BatchID, batch.MaterialDigest, "")
	if err != nil || !found {
		return heterociworkflow.BatchResult{}, false, err
	}
	if err := validateCanonicalReceiptClaimRow(row, claim, claimBytes, claimDigest); err != nil {
		return heterociworkflow.BatchResult{}, false, postgreSQLReceiptDomainError(err)
	}
	if row.State == canonicalReceiptStateClaimed {
		return heterociworkflow.BatchResult{}, false, nil
	}
	result, err := decodeCanonicalBatchResult(row.ResultJSON, row.ResultDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, postgreSQLReceiptDomainError(err)
	}
	if err := validateBatchResult(batch, commandDigest, result); err != nil {
		return heterociworkflow.BatchResult{}, false, errors.Join(ErrPostgreSQLReceiptStore, err)
	}
	return cloneBatchResult(reorderBatchResult(batch, result)), true, nil
}

// WriteIfAbsent 只允许同一 claim 原子迁移为唯一 terminal receipt。
func (store *PostgreSQLReceiptStore) WriteIfAbsent(ctx context.Context, result heterociworkflow.BatchResult) error {
	resultBytes, resultDigest, err := prepareCanonicalBatchResult(ctx, result)
	if err != nil {
		return postgreSQLReceiptDomainError(err)
	}
	for range postgreSQLReceiptMutationAttempts {
		ambiguous, err := store.writeTerminalOnce(ctx, result, resultBytes, resultDigest)
		if err == nil {
			return nil
		}
		if !ambiguous {
			if postgreSQLReceiptRetryable(err) {
				continue
			}
			return err
		}
		resolved, retry, replayErr := store.reconcileTerminal(ctx, result, resultBytes, resultDigest)
		if replayErr != nil {
			return replayErr
		}
		if resolved {
			return nil
		}
		if !retry {
			return err
		}
	}
	return fmt.Errorf("%w: terminal retry budget exhausted", ErrPostgreSQLReceiptStore)
}

func (store *PostgreSQLReceiptStore) writeTerminalOnce(ctx context.Context, result heterociworkflow.BatchResult, resultBytes []byte, resultDigest string) (bool, error) {
	tx, err := store.beginTransaction(ctx)
	if err != nil {
		return false, err
	}
	if err := writePostgreSQLTerminal(ctx, tx, result, resultBytes, resultDigest); err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return true, fmt.Errorf("%w: terminal commit result is ambiguous: %w", ErrPostgreSQLReceiptStore, err)
	}
	return false, nil
}

func writePostgreSQLTerminal(ctx context.Context, tx *sql.Tx, result heterociworkflow.BatchResult, resultBytes []byte, resultDigest string) error {
	row, found, err := readPostgreSQLReceiptRow(ctx, tx, result.BatchID, result.MaterialDigest, " FOR UPDATE")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: terminal receipt has no claim", ErrPostgreSQLReceiptConflict)
	}
	if err := validatePostgreSQLResultClaim(row, result); err != nil {
		return err
	}
	if row.State == canonicalReceiptStateTerminal {
		return validatePostgreSQLTerminalReplay(row, result, resultBytes, resultDigest)
	}
	if row.State != canonicalReceiptStateClaimed {
		return fmt.Errorf("%w: receipt state is invalid", ErrPostgreSQLReceiptStore)
	}
	updated, err := tx.ExecContext(ctx, updatePostgreSQLTerminalReceipt, canonicalReceiptStateTerminal, resultBytes, resultDigest, result.BatchID, result.MaterialDigest, result.CommandDigest, canonicalReceiptStateClaimed)
	if err != nil {
		return fmt.Errorf("%w: write terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: inspect terminal CAS: %w", ErrPostgreSQLReceiptStore, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: terminal CAS affected %d rows", ErrPostgreSQLReceiptConflict, rows)
	}
	return nil
}

func (store *PostgreSQLReceiptStore) reconcileTerminal(ctx context.Context, result heterociworkflow.BatchResult, resultBytes []byte, resultDigest string) (bool, bool, error) {
	tx, err := store.beginTransaction(ctx)
	if err != nil {
		return false, false, err
	}
	row, found, err := readPostgreSQLReceiptRow(ctx, tx, result.BatchID, result.MaterialDigest, " FOR SHARE")
	if err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if !found {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, fmt.Errorf("%w: terminal replay lost claim", ErrPostgreSQLReceiptConflict))
	}
	if err := validatePostgreSQLResultClaim(row, result); err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if row.State == canonicalReceiptStateClaimed {
		if err := tx.Commit(); err != nil {
			return false, false, fmt.Errorf("%w: terminal replay commit: %w", ErrPostgreSQLReceiptStore, err)
		}
		return false, true, nil
	}
	if err := validatePostgreSQLTerminalReplay(row, result, resultBytes, resultDigest); err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("%w: terminal replay commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	return true, false, nil
}

func validatePostgreSQLResultClaim(row canonicalReceiptRow, result heterociworkflow.BatchResult) error {
	claim, err := decodeCanonicalReceiptClaim(row.ClaimJSON, row.ClaimDigest)
	if err != nil {
		return postgreSQLReceiptDomainError(err)
	}
	if row.SchemaVersion != canonicalReceiptRowSchema || row.CommandDigest != claim.CommandDigest {
		return fmt.Errorf("%w: persisted claim envelope drifted", ErrPostgreSQLReceiptStore)
	}
	if err := validateCanonicalResultAgainstClaim(result, claim); err != nil {
		return postgreSQLReceiptDomainError(err)
	}
	return nil
}

func validatePostgreSQLTerminalReplay(row canonicalReceiptRow, result heterociworkflow.BatchResult, resultBytes []byte, resultDigest string) error {
	if row.State != canonicalReceiptStateTerminal {
		return fmt.Errorf("%w: receipt state is invalid", ErrPostgreSQLReceiptStore)
	}
	persisted, err := decodeCanonicalBatchResult(row.ResultJSON, row.ResultDigest)
	if err != nil {
		return postgreSQLReceiptDomainError(err)
	}
	if !bytes.Equal(row.ResultJSON, resultBytes) || row.ResultDigest.String != resultDigest || !sameBatchResult(persisted, result) {
		return fmt.Errorf("%w: terminal receipt already differs", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func readPostgreSQLReceiptRow(ctx context.Context, queryer canonicalReceiptQueryer, batchID heterociworkflow.BatchID, materialDigest, lockClause string) (canonicalReceiptRow, bool, error) {
	var row canonicalReceiptRow
	err := queryer.QueryRowContext(ctx, selectPostgreSQLReceiptRow+lockClause, batchID, materialDigest).Scan(&row.SchemaVersion, &row.CommandDigest, &row.State, &row.ClaimJSON, &row.ClaimDigest, &row.ResultJSON, &row.ResultDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return canonicalReceiptRow{}, false, nil
	}
	if err != nil {
		return canonicalReceiptRow{}, false, fmt.Errorf("%w: read receipt row: %w", ErrPostgreSQLReceiptStore, err)
	}
	return row, true, nil
}

func postgreSQLReceiptDomainError(err error) error {
	if errors.Is(err, ErrCanonicalReceiptConflict) {
		return fmt.Errorf("%w: canonical receipt identity conflicts", ErrPostgreSQLReceiptConflict)
	}
	return fmt.Errorf("%w: canonical receipt is invalid", ErrPostgreSQLReceiptStore)
}

func postgreSQLReceiptRetryable(err error) bool {
	type sqlStateError interface {
		SQLState() string
	}
	var state sqlStateError
	if !errors.As(err, &state) {
		return false
	}
	return state.SQLState() == "40001" || state.SQLState() == "40P01"
}

var _ ReceiptStore = (*PostgreSQLReceiptStore)(nil)
