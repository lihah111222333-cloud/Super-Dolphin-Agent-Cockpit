package heterociexec

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

const (
	selectPostgreSQLReceiptEvent = `SELECT schema_version, operation_id, manifest_digest, plan_digest, executor_binding_digest, node_set_digest, authority_generation, seq, occurred_at, stage, state, code, shard_set_bytes, shard_set_digest, event_bytes, event_digest
FROM heteroci_receipt.receipt_events WHERE operation_id = $1 AND seq = $2`
	selectPostgreSQLReceiptEventHead = `SELECT schema_version, operation_id, manifest_digest, plan_digest, executor_binding_digest, node_set_digest, authority_generation, seq, occurred_at, stage, state, code, shard_set_bytes, shard_set_digest, event_bytes, event_digest
FROM heteroci_receipt.receipt_events WHERE operation_id = $1 ORDER BY seq DESC LIMIT 1`
	selectPostgreSQLReceiptEvents = `SELECT schema_version, operation_id, manifest_digest, plan_digest, executor_binding_digest, node_set_digest, authority_generation, seq, occurred_at, stage, state, code, shard_set_bytes, shard_set_digest, event_bytes, event_digest
FROM heteroci_receipt.receipt_events WHERE operation_id = $1 ORDER BY seq ASC`
	insertPostgreSQLReceiptEvent = `INSERT INTO heteroci_receipt.receipt_events(
schema_version, operation_id, manifest_digest, plan_digest, executor_binding_digest, node_set_digest, authority_generation, seq, occurred_at, stage, state, code, shard_set_bytes, shard_set_digest, event_bytes, event_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (operation_id, seq) DO NOTHING`
	selectPostgreSQLTerminalReceipt = `SELECT schema_version, operation_id, request_digest, manifest_digest, plan_digest, executor_binding_digest, node_set_digest, authority_generation, started_at, finished_at, exit_code, cleanup_state, error_code, capacity_code, status, shard_set_bytes, shard_set_digest, event_seq, event_digest, receipt_bytes, receipt_digest
FROM heteroci_receipt.terminal_receipts WHERE operation_id = $1`
	insertPostgreSQLTerminalReceipt = `INSERT INTO heteroci_receipt.terminal_receipts(
schema_version, operation_id, request_digest, manifest_digest, plan_digest, executor_binding_digest, node_set_digest, authority_generation, started_at, finished_at, exit_code, cleanup_state, error_code, capacity_code, status, shard_set_bytes, shard_set_digest, event_seq, event_digest, receipt_bytes, receipt_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
ON CONFLICT (operation_id) DO NOTHING`
	insertPostgreSQLReceiptEventShard = `INSERT INTO heteroci_receipt.receipt_event_shards(operation_id, seq, node_id, node_id_digest, executor_kind, shard_digest, batch_set_digest, unit_set_digest, state, code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (operation_id, seq, node_id) DO NOTHING`
	selectPostgreSQLReceiptEventShards = `SELECT node_id, node_id_digest, executor_kind, shard_digest, batch_set_digest, unit_set_digest, state, code
FROM heteroci_receipt.receipt_event_shards WHERE operation_id = $1 AND seq = $2 ORDER BY node_id ASC`
	insertPostgreSQLTerminalShard = `INSERT INTO heteroci_receipt.terminal_shards(operation_id, child_operation_id, node_id, node_id_digest, executor_kind, shard_digest, batch_set_digest, unit_set_digest, started_at, finished_at, exit_code, cleanup_state, error_code, capacity_code, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) ON CONFLICT (operation_id, node_id) DO NOTHING`
	selectPostgreSQLTerminalShards = `SELECT child_operation_id, node_id, node_id_digest, executor_kind, shard_digest, batch_set_digest, unit_set_digest, started_at, finished_at, exit_code, cleanup_state, error_code, capacity_code, status
FROM heteroci_receipt.terminal_shards WHERE operation_id = $1 ORDER BY node_id ASC`
)

type postgresqlReceiptEventRow struct {
	schemaVersion         string
	operationID           string
	manifestDigest        string
	planDigest            string
	executorBindingDigest string
	nodeSetDigest         string
	authorityGeneration   int64
	seq                   int64
	occurredAt            string
	stage                 string
	state                 string
	code                  string
	shardSetBytes         []byte
	shardSetDigest        string
	eventBytes            []byte
	eventDigest           string
}

type postgresqlTerminalReceiptRow struct {
	schemaVersion         string
	operationID           string
	requestDigest         string
	manifestDigest        string
	planDigest            string
	executorBindingDigest string
	nodeSetDigest         string
	authorityGeneration   int64
	startedAt             string
	finishedAt            string
	exitCode              int
	cleanupState          string
	errorCode             string
	capacityCode          string
	status                string
	shardSetBytes         []byte
	shardSetDigest        string
	eventSeq              int64
	eventDigest           string
	receiptBytes          []byte
	receiptDigest         string
}

func encodePostgreSQLReceiptEventShards(shards []PostgreSQLReceiptEventShard) ([]byte, string, error) {
	if _, err := postgreSQLReceiptEventNodeSetDigest(shards); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(shards)
	if err != nil {
		return nil, "", ErrPostgreSQLReceiptStore
	}
	return encoded, digestPostgreSQLReceiptBytes(encoded), nil
}

func encodePostgreSQLTerminalShards(shards []PostgreSQLTerminalShard) ([]byte, string, error) {
	if err := validatePostgreSQLTerminalShardBindings(shards); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(shards)
	if err != nil {
		return nil, "", ErrPostgreSQLReceiptStore
	}
	return encoded, digestPostgreSQLReceiptBytes(encoded), nil
}

func persistPostgreSQLReceiptEventShards(ctx context.Context, tx *sql.Tx, event PostgreSQLReceiptEvent) error {
	for _, shard := range event.Shards {
		if _, err := tx.ExecContext(ctx, insertPostgreSQLReceiptEventShard, event.OperationID, event.Seq, shard.NodeID, shard.NodeIDDigest, shard.ExecutorKind, shard.ShardDigest, shard.BatchSetDigest, shard.UnitSetDigest, shard.State, shard.Code); err != nil {
			return fmt.Errorf("%w: insert receipt event shard: %w", ErrPostgreSQLReceiptStore, err)
		}
	}
	persisted, err := readPostgreSQLReceiptEventShards(ctx, tx, event.OperationID, event.Seq)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(persisted, event.Shards) {
		return fmt.Errorf("%w: receipt event shard readback drifted", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func readPostgreSQLReceiptEventShards(ctx context.Context, tx *sql.Tx, operationID string, seq int64) ([]PostgreSQLReceiptEventShard, error) {
	rows, err := tx.QueryContext(ctx, selectPostgreSQLReceiptEventShards, operationID, seq)
	if err != nil {
		return nil, fmt.Errorf("%w: read receipt event shards: %w", ErrPostgreSQLReceiptStore, err)
	}
	defer rows.Close()
	shards := make([]PostgreSQLReceiptEventShard, 0)
	for rows.Next() {
		var shard PostgreSQLReceiptEventShard
		if err := rows.Scan(&shard.NodeID, &shard.NodeIDDigest, &shard.ExecutorKind, &shard.ShardDigest, &shard.BatchSetDigest, &shard.UnitSetDigest, &shard.State, &shard.Code); err != nil {
			return nil, fmt.Errorf("%w: scan receipt event shard: %w", ErrPostgreSQLReceiptStore, err)
		}
		shards = append(shards, shard)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate receipt event shards: %w", ErrPostgreSQLReceiptStore, err)
	}
	return shards, nil
}

func persistPostgreSQLTerminalShards(ctx context.Context, tx *sql.Tx, receipt PostgreSQLTerminalReceipt) error {
	for _, shard := range receipt.Shards {
		if _, err := tx.ExecContext(ctx, insertPostgreSQLTerminalShard, receipt.OperationID, shard.ChildOperationID, shard.NodeID, shard.NodeIDDigest, shard.ExecutorKind, shard.ShardDigest, shard.BatchSetDigest, shard.UnitSetDigest, shard.StartedAt.Format(time.RFC3339Nano), shard.FinishedAt.Format(time.RFC3339Nano), shard.ExitCode, shard.CleanupState, shard.ErrorCode, shard.CapacityCode, shard.Status); err != nil {
			return fmt.Errorf("%w: insert terminal shard: %w", ErrPostgreSQLReceiptStore, err)
		}
	}
	persisted, err := readPostgreSQLTerminalShards(ctx, tx, receipt.OperationID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(persisted, receipt.Shards) {
		return fmt.Errorf("%w: terminal shard readback drifted", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func readPostgreSQLTerminalShards(ctx context.Context, tx *sql.Tx, operationID string) ([]PostgreSQLTerminalShard, error) {
	rows, err := tx.QueryContext(ctx, selectPostgreSQLTerminalShards, operationID)
	if err != nil {
		return nil, fmt.Errorf("%w: read terminal shards: %w", ErrPostgreSQLReceiptStore, err)
	}
	defer rows.Close()
	shards := make([]PostgreSQLTerminalShard, 0)
	for rows.Next() {
		var shard PostgreSQLTerminalShard
		var startedAt, finishedAt string
		if err := rows.Scan(&shard.ChildOperationID, &shard.NodeID, &shard.NodeIDDigest, &shard.ExecutorKind, &shard.ShardDigest, &shard.BatchSetDigest, &shard.UnitSetDigest, &startedAt, &finishedAt, &shard.ExitCode, &shard.CleanupState, &shard.ErrorCode, &shard.CapacityCode, &shard.Status); err != nil {
			return nil, fmt.Errorf("%w: scan terminal shard: %w", ErrPostgreSQLReceiptStore, err)
		}
		shard.StartedAt, err = parsePostgreSQLReceiptTime(startedAt)
		if err != nil {
			return nil, err
		}
		shard.FinishedAt, err = parsePostgreSQLReceiptTime(finishedAt)
		if err != nil {
			return nil, err
		}
		shards = append(shards, shard)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate terminal shards: %w", ErrPostgreSQLReceiptStore, err)
	}
	return shards, nil
}

// AppendEvent 原子追加严格递增的 event sequence；相同 operation、seq、digest 重放成功。
func (store *PostgreSQLReceiptStore) AppendEvent(ctx context.Context, event PostgreSQLReceiptEvent) error {
	encoded, digest, err := canonicalPostgreSQLReceiptEvent(event)
	if err != nil {
		return err
	}
	ambiguous, err := store.appendEventOnce(ctx, event, encoded, digest)
	if err == nil {
		return nil
	}
	if !ambiguous {
		return wrapPostgreSQLReceiptMutationError("append event", err)
	}
	resolved, reconcileErr := store.reconcileEvent(ctx, event, encoded, digest)
	if reconcileErr != nil {
		return reconcileErr
	}
	if resolved {
		return nil
	}
	return errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
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
	persistedShards, err := readPostgreSQLReceiptEventShards(ctx, tx, event.OperationID, event.Seq)
	if err != nil || !reflect.DeepEqual(persistedShards, event.Shards) {
		return false, fmt.Errorf("%w: receipt event shard replay differs", ErrPostgreSQLReceiptConflict)
	}
	return true, nil
}

func insertAndVerifyPostgreSQLReceiptEvent(ctx context.Context, tx *sql.Tx, event PostgreSQLReceiptEvent, encoded []byte, digest string) error {
	shardSetBytes, shardSetDigest, err := encodePostgreSQLReceiptEventShards(event.Shards)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertPostgreSQLReceiptEvent, event.SchemaVersion, event.OperationID, event.ManifestDigest, event.PlanDigest, event.ExecutorBindingDigest, event.NodeSetDigest, event.AuthorityGeneration, event.Seq, event.OccurredAt.Format(time.RFC3339Nano), event.Stage, event.State, event.Code, shardSetBytes, shardSetDigest, encoded, digest); err != nil {
		return fmt.Errorf("%w: insert receipt event: %w", ErrPostgreSQLReceiptStore, err)
	}
	if err := persistPostgreSQLReceiptEventShards(ctx, tx, event); err != nil {
		return err
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
	if event.OperationID != head.operationID || event.ManifestDigest != head.manifestDigest || event.PlanDigest != head.planDigest || event.ExecutorBindingDigest != head.executorBindingDigest || event.AuthorityGeneration != head.authorityGeneration {
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

func (store *PostgreSQLReceiptStore) reconcileEvent(ctx context.Context, event PostgreSQLReceiptEvent, encoded []byte, digest string) (bool, error) {
	tx, err := store.beginStructuredReceiptTransaction(ctx, event.OperationID)
	if err != nil {
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
	}
	persisted, found, err := readPostgreSQLReceiptEvent(ctx, tx, event.OperationID, event.Seq)
	if err != nil {
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, rollbackPostgreSQLReceiptTransaction(tx, err))
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
		}
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, errors.New("event commit was not observed"))
	}
	if err := validatePostgreSQLReceiptEventReplay(persisted, event, encoded, digest); err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, postgreSQLReceiptDomainError(err))
	}
	persistedShards, shardErr := readPostgreSQLReceiptEventShards(ctx, tx, event.OperationID, event.Seq)
	if shardErr != nil || !reflect.DeepEqual(persistedShards, event.Shards) {
		return false, rollbackPostgreSQLReceiptTransaction(tx, postgreSQLReceiptDomainError(ErrPostgreSQLReceiptConflict))
	}
	if err := tx.Commit(); err != nil {
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
	}
	return true, nil
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
	events, err := readPostgreSQLReceiptEventChain(ctx, tx, operationID)
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
	ambiguous, err := store.writeTerminalReceiptOnce(ctx, receipt, encoded, digest)
	if err == nil {
		return nil
	}
	if !ambiguous {
		return wrapPostgreSQLReceiptMutationError("write terminal receipt", err)
	}
	resolved, reconcileErr := store.reconcileTerminalReceipt(ctx, receipt, encoded, digest)
	if reconcileErr != nil {
		return reconcileErr
	}
	if resolved {
		return nil
	}
	return errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
}

func persistPostgreSQLTerminalReceipt(ctx context.Context, tx *sql.Tx, receipt PostgreSQLTerminalReceipt, encoded []byte, digest string) (bool, error) {
	persisted, found, err := readPostgreSQLTerminalReceipt(ctx, tx, receipt.OperationID)
	if err != nil {
		return false, err
	}
	if found {
		return validatePersistedPostgreSQLTerminalReceipt(ctx, tx, persisted, receipt, encoded, digest)
	}
	return insertPostgreSQLTerminalReceiptAndVerify(ctx, tx, receipt, encoded, digest)
}

func validatePersistedPostgreSQLTerminalReceipt(ctx context.Context, tx *sql.Tx, persisted, receipt PostgreSQLTerminalReceipt, encoded []byte, digest string) (bool, error) {
	if err := validatePostgreSQLTerminalReceiptReplay(persisted, receipt, encoded, digest); err != nil {
		return false, err
	}
	persistedShards, err := readPostgreSQLTerminalShards(ctx, tx, receipt.OperationID)
	if err != nil || !reflect.DeepEqual(persistedShards, receipt.Shards) {
		return false, fmt.Errorf("%w: terminal shard replay differs", ErrPostgreSQLReceiptConflict)
	}
	return true, nil
}

func insertPostgreSQLTerminalReceiptAndVerify(ctx context.Context, tx *sql.Tx, receipt PostgreSQLTerminalReceipt, encoded []byte, digest string) (bool, error) {
	shardSetBytes, shardSetDigest, err := encodePostgreSQLTerminalShards(receipt.Shards)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, insertPostgreSQLTerminalReceipt, receipt.SchemaVersion, receipt.OperationID, receipt.RequestDigest, receipt.ManifestDigest, receipt.PlanDigest, receipt.ExecutorBindingDigest, receipt.NodeSetDigest, receipt.AuthorityGeneration, receipt.StartedAt.Format(time.RFC3339Nano), receipt.FinishedAt.Format(time.RFC3339Nano), receipt.ExitCode, receipt.CleanupState, receipt.ErrorCode, receipt.CapacityCode, receipt.Status, shardSetBytes, shardSetDigest, receipt.EventSeq, receipt.EventDigest, encoded, digest); err != nil {
		return false, fmt.Errorf("%w: insert terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	if err := persistPostgreSQLTerminalShards(ctx, tx, receipt); err != nil {
		return false, err
	}
	persisted, found, err := readPostgreSQLTerminalReceipt(ctx, tx, receipt.OperationID)
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

func (store *PostgreSQLReceiptStore) reconcileTerminalReceipt(ctx context.Context, receipt PostgreSQLTerminalReceipt, encoded []byte, digest string) (bool, error) {
	tx, err := store.beginStructuredReceiptTransaction(ctx, receipt.OperationID)
	if err != nil {
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
	}
	persisted, found, err := readPostgreSQLTerminalReceipt(ctx, tx, receipt.OperationID)
	if err != nil {
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, rollbackPostgreSQLReceiptTransaction(tx, err))
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
		}
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, errors.New("terminal receipt commit was not observed"))
	}
	events, chainErr := readPostgreSQLReceiptEventChain(ctx, tx, receipt.OperationID)
	if chainErr != nil {
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, rollbackPostgreSQLReceiptTransaction(tx, chainErr))
	}
	if chainErr := validatePostgreSQLReceiptEventChain(events, receipt); chainErr != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, postgreSQLReceiptDomainError(chainErr))
	}
	if err := validatePostgreSQLTerminalReceiptReplay(persisted, receipt, encoded, digest); err != nil {
		return false, rollbackPostgreSQLReceiptTransaction(tx, postgreSQLReceiptDomainError(err))
	}
	if err := tx.Commit(); err != nil {
		return false, errors.Join(ErrPostgreSQLReceiptCommitUncertain, err)
	}
	return true, nil
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
