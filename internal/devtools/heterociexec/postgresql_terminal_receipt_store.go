package heterociexec

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	selectPostgreSQLReceiptEvent = `SELECT schema_version, operation_id, manifest_digest, plan_digest, node_id, executor_kind, authority_generation, seq, occurred_at, stage, state, node_id_digest, code, event_bytes, event_digest
FROM heteroci_receipt.receipt_events WHERE operation_id = $1 AND seq = $2`
	selectPostgreSQLReceiptEventHead = `SELECT schema_version, operation_id, manifest_digest, plan_digest, node_id, executor_kind, authority_generation, seq, occurred_at, stage, state, node_id_digest, code, event_bytes, event_digest
FROM heteroci_receipt.receipt_events WHERE operation_id = $1 ORDER BY seq DESC LIMIT 1`
	selectPostgreSQLReceiptEvents = `SELECT schema_version, operation_id, manifest_digest, plan_digest, node_id, executor_kind, authority_generation, seq, occurred_at, stage, state, node_id_digest, code, event_bytes, event_digest
FROM heteroci_receipt.receipt_events WHERE operation_id = $1 ORDER BY seq ASC`
	insertPostgreSQLReceiptEvent = `INSERT INTO heteroci_receipt.receipt_events(
schema_version, operation_id, manifest_digest, plan_digest, node_id, executor_kind, authority_generation, seq, occurred_at, stage, state, node_id_digest, code, event_bytes, event_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (operation_id, seq) DO NOTHING`
	selectPostgreSQLTerminalReceipt = `SELECT schema_version, operation_id, request_digest, manifest_digest, plan_digest, node_id, executor_kind, authority_generation, started_at, finished_at, exit_code, cleanup_state, error_code, capacity_code, status, event_seq, event_digest, receipt_bytes, receipt_digest
FROM heteroci_receipt.terminal_receipts WHERE operation_id = $1`
	insertPostgreSQLTerminalReceipt = `INSERT INTO heteroci_receipt.terminal_receipts(
schema_version, operation_id, request_digest, manifest_digest, plan_digest, node_id, executor_kind, authority_generation, started_at, finished_at, exit_code, cleanup_state, error_code, capacity_code, status, event_seq, event_digest, receipt_bytes, receipt_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
ON CONFLICT (operation_id) DO NOTHING`
)

type postgresqlReceiptEventRow struct {
	schemaVersion       string
	operationID         string
	manifestDigest      string
	planDigest          string
	nodeID              string
	executorKind        string
	authorityGeneration int64
	seq                 int64
	occurredAt          string
	stage               string
	state               string
	nodeIDDigest        string
	code                string
	eventBytes          []byte
	eventDigest         string
}

type postgresqlTerminalReceiptRow struct {
	schemaVersion       string
	operationID         string
	requestDigest       string
	manifestDigest      string
	planDigest          string
	nodeID              string
	executorKind        string
	authorityGeneration int64
	startedAt           string
	finishedAt          string
	exitCode            int
	cleanupState        string
	errorCode           string
	capacityCode        string
	status              string
	eventSeq            int64
	eventDigest         string
	receiptBytes        []byte
	receiptDigest       string
}

// AppendEvent 原子追加严格递增的 event sequence；相同 operation、seq、digest 重放成功。
func (store *PostgreSQLReceiptStore) AppendEvent(ctx context.Context, event PostgreSQLReceiptEvent) error {
	encoded, digest, err := canonicalPostgreSQLReceiptEvent(event)
	if err != nil {
		return err
	}
	for range postgreSQLReceiptMutationAttempts {
		ambiguous, err := store.appendEventOnce(ctx, event, encoded, digest)
		if err == nil {
			return nil
		}
		if ambiguous {
			resolved, retry, reconcileErr := store.reconcileEvent(ctx, event, encoded, digest)
			if reconcileErr != nil {
				return reconcileErr
			}
			if resolved {
				return nil
			}
			if retry {
				continue
			}
		}
		if postgreSQLReceiptRetryable(err) {
			continue
		}
		return err
	}
	return fmt.Errorf("%w: event retry budget exhausted", ErrPostgreSQLReceiptStore)
}

func (store *PostgreSQLReceiptStore) appendEventOnce(ctx context.Context, event PostgreSQLReceiptEvent, encoded []byte, digest string) (bool, error) {
	tx, err := store.beginStructuredReceiptTransaction(ctx, event.OperationID)
	if err != nil {
		return false, err
	}
	head, found, err := readPostgreSQLReceiptEventHead(ctx, tx, event.OperationID)
	if err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	replayed, err := replayPostgreSQLReceiptEvent(ctx, tx, event, encoded, digest)
	if err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if replayed {
		return commitPostgreSQLReceiptReplay(tx)
	}
	if err := ensurePostgreSQLReceiptOperationOpen(ctx, tx, event.OperationID); err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := validatePostgreSQLReceiptSequence(head, found, event); err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := insertAndVerifyPostgreSQLReceiptEvent(ctx, tx, event, encoded, digest); err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	return commitPostgreSQLReceiptMutation(tx)
}

func ensurePostgreSQLReceiptOperationOpen(ctx context.Context, tx *sql.Tx, operationID string) error {
	_, found, err := readPostgreSQLTerminalReceipt(ctx, tx, operationID)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("%w: terminal receipt already closes operation", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func replayPostgreSQLReceiptEvent(ctx context.Context, tx *sql.Tx, event PostgreSQLReceiptEvent, encoded []byte, digest string) (bool, error) {
	existing, found, err := readPostgreSQLReceiptEvent(ctx, tx, event.OperationID, event.Seq)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if err := validatePostgreSQLReceiptEventReplay(existing, event, encoded, digest); err != nil {
		return false, err
	}
	return true, nil
}

func insertAndVerifyPostgreSQLReceiptEvent(ctx context.Context, tx *sql.Tx, event PostgreSQLReceiptEvent, encoded []byte, digest string) error {
	if _, err := tx.ExecContext(ctx, insertPostgreSQLReceiptEvent, event.SchemaVersion, event.OperationID, event.ManifestDigest, event.PlanDigest, event.NodeID, event.ExecutorKind, event.AuthorityGeneration, event.Seq, event.OccurredAt.Format(time.RFC3339Nano), event.Stage, event.State, event.NodeIDDigest, event.Code, encoded, digest); err != nil {
		return fmt.Errorf("%w: insert receipt event: %w", ErrPostgreSQLReceiptStore, err)
	}
	persisted, found, err := readPostgreSQLReceiptEvent(ctx, tx, event.OperationID, event.Seq)
	if err != nil {
		return err
	}
	if !found {
		return ErrPostgreSQLReceiptStore
	}
	return validatePostgreSQLReceiptEventReplay(persisted, event, encoded, digest)
}

func validatePostgreSQLReceiptSequencePosition(head postgresqlReceiptEventRow, event PostgreSQLReceiptEvent) error {
	if event.Seq != head.seq+1 || postgreSQLReceiptStageOrder(event.Stage) < postgreSQLReceiptStageOrder(PostgreSQLReceiptEventStage(head.stage)) {
		return fmt.Errorf("%w: event sequence or stage regressed", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func validatePostgreSQLReceiptOccurredAt(head postgresqlReceiptEventRow, event PostgreSQLReceiptEvent) error {
	headOccurredAt, err := parsePostgreSQLReceiptTime(head.occurredAt)
	if err != nil {
		return fmt.Errorf("%w: persisted event occurred_at is invalid", ErrPostgreSQLReceiptStore)
	}
	if event.OccurredAt.Before(headOccurredAt) {
		return fmt.Errorf("%w: event occurred_at regressed", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func validatePostgreSQLReceiptStateAndIdentity(head postgresqlReceiptEventRow, event PostgreSQLReceiptEvent) error {
	if event.Stage == PostgreSQLReceiptEventStage(head.stage) && !validPostgreSQLReceiptStateTransition(PostgreSQLReceiptEventState(head.state), event.State) {
		return fmt.Errorf("%w: event state transition is invalid", ErrPostgreSQLReceiptConflict)
	}
	if event.OperationID != head.operationID || event.ManifestDigest != head.manifestDigest || event.PlanDigest != head.planDigest || event.NodeID != head.nodeID || event.ExecutorKind != head.executorKind || event.AuthorityGeneration != head.authorityGeneration {
		return fmt.Errorf("%w: event identity drifted", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func validatePostgreSQLReceiptSequence(head postgresqlReceiptEventRow, found bool, event PostgreSQLReceiptEvent) error {
	if !found {
		if event.Seq != 1 {
			return fmt.Errorf("%w: first event sequence must be one", ErrPostgreSQLReceiptConflict)
		}
		return nil
	}
	if err := validatePostgreSQLReceiptSequencePosition(head, event); err != nil {
		return err
	}
	if err := validatePostgreSQLReceiptOccurredAt(head, event); err != nil {
		return err
	}
	return validatePostgreSQLReceiptStateAndIdentity(head, event)
}

func (store *PostgreSQLReceiptStore) reconcileEvent(ctx context.Context, event PostgreSQLReceiptEvent, encoded []byte, digest string) (bool, bool, error) {
	tx, err := store.beginStructuredReceiptTransaction(ctx, event.OperationID)
	if err != nil {
		return false, false, err
	}
	persisted, found, err := readPostgreSQLReceiptEvent(ctx, tx, event.OperationID, event.Seq)
	if err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return false, true, fmt.Errorf("%w: event reconciliation read commit: %w", ErrPostgreSQLReceiptStore, err)
		}
		return false, true, nil
	}
	if err := validatePostgreSQLReceiptEventReplay(persisted, event, encoded, digest); err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("%w: event reconciliation commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	return true, false, nil
}

// ReadReceiptEvents 从同一 primary 的 SERIALIZABLE snapshot 返回完整有序 event sequence。
func (store *PostgreSQLReceiptStore) ReadReceiptEvents(ctx context.Context, operationID string) ([]PostgreSQLReceiptEvent, error) {
	if !validTerminalOperationID(operationID) {
		return nil, ErrPostgreSQLReceiptStore
	}
	tx, err := store.beginStructuredReceiptTransaction(ctx, operationID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, selectPostgreSQLReceiptEvents, operationID)
	if err != nil {
		return nil, rollbackPostgreSQLReceiptTransaction(tx, fmt.Errorf("%w: read receipt events: %w", ErrPostgreSQLReceiptStore, err))
	}
	defer rows.Close()
	events, err := scanPostgreSQLReceiptEvents(rows)
	if err != nil {
		return nil, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: read receipt events commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	return events, nil
}

// WriteTerminalReceipt 在 terminal event 已 exact readback 且清理已证明后写入唯一 receipt。
func (store *PostgreSQLReceiptStore) WriteTerminalReceipt(ctx context.Context, receipt PostgreSQLTerminalReceipt) error {
	encoded, digest, err := canonicalPostgreSQLTerminalReceipt(receipt)
	if err != nil {
		return err
	}
	for range postgreSQLReceiptMutationAttempts {
		ambiguous, err := store.writeTerminalReceiptOnce(ctx, receipt, encoded, digest)
		if err == nil {
			return nil
		}
		if ambiguous {
			resolved, retry, reconcileErr := store.reconcileTerminalReceipt(ctx, receipt, encoded, digest)
			if reconcileErr != nil {
				return reconcileErr
			}
			if resolved {
				return nil
			}
			if retry {
				continue
			}
		}
		if postgreSQLReceiptRetryable(err) {
			continue
		}
		return err
	}
	return fmt.Errorf("%w: terminal receipt retry budget exhausted", ErrPostgreSQLReceiptStore)
}

func persistPostgreSQLTerminalReceipt(ctx context.Context, tx *sql.Tx, receipt PostgreSQLTerminalReceipt, encoded []byte, digest string) (bool, error) {
	persisted, found, err := readPostgreSQLTerminalReceipt(ctx, tx, receipt.OperationID)
	if err != nil {
		return false, err
	}
	if found {
		if err := validatePostgreSQLTerminalReceiptReplay(persisted, receipt, encoded, digest); err != nil {
			return false, err
		}
		return true, nil
	}
	if _, err := tx.ExecContext(ctx, insertPostgreSQLTerminalReceipt, receipt.SchemaVersion, receipt.OperationID, receipt.RequestDigest, receipt.ManifestDigest, receipt.PlanDigest, receipt.NodeID, receipt.ExecutorKind, receipt.AuthorityGeneration, receipt.StartedAt.Format(time.RFC3339Nano), receipt.FinishedAt.Format(time.RFC3339Nano), receipt.ExitCode, receipt.CleanupState, receipt.ErrorCode, receipt.CapacityCode, receipt.Status, receipt.EventSeq, receipt.EventDigest, encoded, digest); err != nil {
		return false, fmt.Errorf("%w: insert terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	persisted, found, err = readPostgreSQLTerminalReceipt(ctx, tx, receipt.OperationID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, ErrPostgreSQLReceiptStore
	}
	return false, validatePostgreSQLTerminalReceiptReplay(persisted, receipt, encoded, digest)
}

func (store *PostgreSQLReceiptStore) writeTerminalReceiptOnce(ctx context.Context, receipt PostgreSQLTerminalReceipt, encoded []byte, digest string) (bool, error) {
	tx, err := store.beginStructuredReceiptTransaction(ctx, receipt.OperationID)
	if err != nil {
		return false, err
	}
	events, err := readPostgreSQLReceiptEventChain(ctx, tx, receipt.OperationID)
	if err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := validatePostgreSQLReceiptEventChain(events, receipt); err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	replayed, err := persistPostgreSQLTerminalReceipt(ctx, tx, receipt, encoded, digest)
	if err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if replayed {
		return commitPostgreSQLReceiptReplay(tx)
	}
	return commitPostgreSQLReceiptMutation(tx)
}

func (store *PostgreSQLReceiptStore) reconcileTerminalReceipt(ctx context.Context, receipt PostgreSQLTerminalReceipt, encoded []byte, digest string) (bool, bool, error) {
	tx, err := store.beginStructuredReceiptTransaction(ctx, receipt.OperationID)
	if err != nil {
		return false, false, err
	}
	persisted, found, err := readPostgreSQLTerminalReceipt(ctx, tx, receipt.OperationID)
	if err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return false, true, fmt.Errorf("%w: terminal reconciliation read commit: %w", ErrPostgreSQLReceiptStore, err)
		}
		return false, true, nil
	}
	events, chainErr := readPostgreSQLReceiptEventChain(ctx, tx, receipt.OperationID)
	if chainErr != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, chainErr)
	}
	if chainErr := validatePostgreSQLReceiptEventChain(events, receipt); chainErr != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, chainErr)
	}
	if err := validatePostgreSQLTerminalReceiptReplay(persisted, receipt, encoded, digest); err != nil {
		return false, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("%w: terminal reconciliation commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	return true, false, nil
}

// ReadTerminalReceipt 读取并验证单一 operation 的完整 terminal receipt。
func (store *PostgreSQLReceiptStore) ReadTerminalReceipt(ctx context.Context, operationID string) (PostgreSQLTerminalReceipt, bool, error) {
	if !validTerminalOperationID(operationID) {
		return PostgreSQLTerminalReceipt{}, false, ErrPostgreSQLReceiptStore
	}
	tx, err := store.beginStructuredReceiptTransaction(ctx, operationID)
	if err != nil {
		return PostgreSQLTerminalReceipt{}, false, err
	}
	receipt, found, err := readPostgreSQLTerminalReceipt(ctx, tx, operationID)
	if err != nil {
		return PostgreSQLTerminalReceipt{}, false, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if found {
		events, chainErr := readPostgreSQLReceiptEventChain(ctx, tx, operationID)
		if chainErr != nil {
			return PostgreSQLTerminalReceipt{}, false, rollbackPostgreSQLReceiptTransaction(tx, chainErr)
		}
		if chainErr := validatePostgreSQLReceiptEventChain(events, receipt); chainErr != nil {
			return PostgreSQLTerminalReceipt{}, false, rollbackPostgreSQLReceiptTransaction(tx, chainErr)
		}
	}
	if err := tx.Commit(); err != nil {
		return PostgreSQLTerminalReceipt{}, false, fmt.Errorf("%w: terminal receipt read commit: %w", ErrPostgreSQLReceiptStore, err)
	}
	return receipt, found, nil
}

func (store *PostgreSQLReceiptStore) beginStructuredReceiptTransaction(ctx context.Context, operationID string) (*sql.Tx, error) {
	if store == nil || store.database == nil || ctx == nil || !validTerminalOperationID(operationID) {
		return nil, ErrPostgreSQLReceiptStore
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := store.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("%w: begin serializable structured receipt transaction: %w", ErrPostgreSQLReceiptStore, err)
	}
	if err := validatePostgreSQLStructuredReceiptServer(ctx, tx); err != nil {
		return nil, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	if err := validatePostgreSQLStructuredReceiptObjects(ctx, tx); err != nil {
		return nil, rollbackPostgreSQLReceiptTransaction(tx, err)
	}
	left, right := postgreSQLReceiptAdvisoryKeys(operationID)
	var locked any
	if err := tx.QueryRowContext(ctx, "SELECT pg_advisory_xact_lock($1, $2)", left, right).Scan(&locked); err != nil {
		return nil, rollbackPostgreSQLReceiptTransaction(tx, fmt.Errorf("%w: lock receipt operation: %w", ErrPostgreSQLReceiptStore, err))
	}
	return tx, nil
}

func validatePostgreSQLStructuredReceiptServer(ctx context.Context, tx *sql.Tx) error {
	var authority postgreSQLReceiptAuthority
	if err := tx.QueryRowContext(ctx, postgreSQLReceiptAuthorityQuery).Scan(&authority.CurrentUser, &authority.TransactionReadOnly, &authority.SynchronousCommit, &authority.InRecovery, &authority.ServerAddress, &authority.TableExists, &authority.SchemaOwner, &authority.TableOwner); err != nil {
		return fmt.Errorf("%w: inspect structured receipt authority: %w", ErrPostgreSQLReceiptStore, err)
	}
	if err := validatePostgreSQLReceiptEndpoint(authority.ServerAddress); err != nil {
		return err
	}
	if authority.CurrentUser != PostgreSQLReceiptWriterRole || authority.TransactionReadOnly != "off" || authority.InRecovery || authority.SynchronousCommit != "remote_apply" {
		return fmt.Errorf("%w: structured receipt endpoint is not remote read-write primary", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func postgreSQLReceiptAdvisoryKeys(operationID string) (int32, int32) {
	digest := sha256.Sum256([]byte(operationID))
	return int32(binary.BigEndian.Uint32(digest[:4])), int32(binary.BigEndian.Uint32(digest[4:8]))
}

func commitPostgreSQLReceiptMutation(tx *sql.Tx) (bool, error) {
	if err := tx.Commit(); err != nil {
		return true, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
	}
	return false, nil
}

func commitPostgreSQLReceiptReplay(tx *sql.Tx) (bool, error) {
	if err := tx.Commit(); err != nil {
		return true, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
	}
	return false, nil
}
