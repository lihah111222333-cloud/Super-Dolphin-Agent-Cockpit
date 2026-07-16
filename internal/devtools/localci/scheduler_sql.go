package localci

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// schedulerOutboxMaximum 在 ACK 事务内读取该 subscriber invocation 已持久化的最大事件序号。
func schedulerOutboxMaximum(
	ctx context.Context,
	tx *sql.Tx,
	subscriberID string,
	invocationID invocationID,
) (uint64, error) {
	var maximum uint64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(event_seq), 0) FROM scheduler_outbox
		WHERE subscriber_id = ? AND invocation_id = ?`,
		subscriberID,
		invocationID,
	).Scan(&maximum); err != nil {
		return 0, fmt.Errorf("read scheduler outbox maximum: %w", err)
	}
	return maximum, nil
}

// advanceOutboxCursor 以数据库当前 cursor 为 CAS 条件，拒绝任何持久化回退。
func advanceOutboxCursor(
	ctx context.Context,
	tx *sql.Tx,
	subscriberID string,
	invocationID invocationID,
	sequence uint64,
) error {
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO scheduler_outbox_cursors (subscriber_id, invocation_id, ack_seq)
		VALUES (?, ?, ?)
		ON CONFLICT(subscriber_id, invocation_id) DO UPDATE SET ack_seq = excluded.ack_seq
		WHERE scheduler_outbox_cursors.ack_seq <= excluded.ack_seq`,
		subscriberID,
		invocationID,
		sequence,
	)
	if err != nil {
		return fmt.Errorf("persist scheduler outbox cursor: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read scheduler outbox ack rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("outbox ack sequence %d regresses persisted cursor", sequence)
	}
	if rows != 1 {
		return fmt.Errorf("persist scheduler outbox cursor affected %d rows, want 1", rows)
	}
	return nil
}

func requireOneRow(result sql.Result, action string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s affected %d rows, want 1", action, rows)
	}
	return nil
}

func rollbackTransaction(tx *sql.Tx, retErr *error, action string) {
	if tx == nil || retErr == nil {
		return
	}
	err := tx.Rollback()
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return
	}
	*retErr = errors.Join(*retErr, fmt.Errorf("rollback %s: %w", action, err))
}

func closeRows(rows *sql.Rows, retErr *error, action string) {
	if rows == nil || retErr == nil {
		return
	}
	if err := rows.Close(); err != nil {
		*retErr = errors.Join(*retErr, fmt.Errorf("close %s: %w", action, err))
	}
}
