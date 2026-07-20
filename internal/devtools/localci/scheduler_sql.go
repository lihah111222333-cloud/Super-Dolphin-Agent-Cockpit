package localci

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func schedulerDatabaseEmpty(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'",
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("inspect scheduler schema: %w", err)
	}
	return count == 0, nil
}

func createSchedulerSchema(ctx context.Context, db *sql.DB, daemonKey string) (retErr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin scheduler schema creation: %w", err)
	}
	defer rollbackTransaction(tx, &retErr, "scheduler schema creation")
	if _, err := tx.ExecContext(ctx, schedulerSchemaSQL); err != nil {
		return fmt.Errorf("create scheduler schema: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO scheduler_schema (id, version, daemon_key) VALUES (1, ?, ?)",
		schedulerSchemaVersion,
		daemonKey,
	); err != nil {
		return fmt.Errorf("record scheduler schema identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scheduler schema creation: %w", err)
	}
	return nil
}

// validateSchedulerSchema 拒绝版本、daemon key 或 SQLite 完整性漂移。
func validateSchedulerSchema(ctx context.Context, db *sql.DB, daemonKey string) error {
	var version int
	var storedDaemonKey string
	if err := db.QueryRowContext(
		ctx,
		"SELECT version, daemon_key FROM scheduler_schema WHERE id = 1",
	).Scan(&version, &storedDaemonKey); err != nil {
		return fmt.Errorf("read scheduler schema identity: %w", err)
	}
	if version == 1 {
		return errSchedulerSchemaV1Unsupported
	}
	if version != schedulerSchemaVersion {
		return fmt.Errorf("scheduler schema version %d does not match required %d", version, schedulerSchemaVersion)
	}
	if storedDaemonKey != daemonKey {
		return errors.New("scheduler state daemon identity mismatch")
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("check scheduler state integrity: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("scheduler state integrity check failed: %s", integrity)
	}
	return nil
}

// saveKernel 将 queue、DAG 与 lease 作为一个 daemon 快照原子持久化。
func (s *schedulerState) saveKernel(ctx context.Context, kernel *schedulerKernel) (retErr error) {
	if kernel == nil || kernel.identity.key != s.daemonKey {
		return errors.New("scheduler kernel daemon identity mismatch")
	}
	if err := kernel.validateDAG(); err != nil {
		return err
	}
	tx, err := s.beginVerifiedSnapshot(ctx)
	if err != nil {
		return err
	}
	defer rollbackTransaction(tx, &retErr, "scheduler snapshot")
	if err := clearSchedulerSnapshot(ctx, tx); err != nil {
		return err
	}
	if err := insertSchedulerNodes(ctx, tx, kernel); err != nil {
		return err
	}
	if err := insertSchedulerDependencies(ctx, tx, kernel); err != nil {
		return err
	}
	if err := insertSchedulerLeases(ctx, tx, kernel); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scheduler snapshot: %w", err)
	}
	return nil
}

// beginVerifiedSnapshot 在开启 SQLite 事务前确认运行时数据库路径未漂移。
func (s *schedulerState) beginVerifiedSnapshot(ctx context.Context) (*sql.Tx, error) {
	if err := s.validateStatePathIdentity(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin scheduler snapshot: %w", err)
	}
	return tx, nil
}

func clearSchedulerSnapshot(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"scheduler_dependencies", "scheduler_leases", "scheduler_workloads"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}

// insertSchedulerNodes 原子写入 workload 与完整 group identity。
func insertSchedulerNodes(ctx context.Context, tx *sql.Tx, kernel *schedulerKernel) error {
	const query = `INSERT INTO scheduler_workloads
		(id, invocation_id, enqueue_seq, sub_seq, kind, service_count,
		 group_identity, group_size, shard_identities, status,
		 failed_shard_identity, gang_bypasses)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, node := range kernel.nodes {
		shardIdentities, err := json.Marshal(node.spec.shardIDs)
		if err != nil {
			return fmt.Errorf("encode workload %q shard identities: %w", node.spec.id, err)
		}
		if _, err := tx.ExecContext(
			ctx,
			query,
			node.spec.id,
			node.spec.invocationID,
			node.spec.enqueueSeq,
			node.spec.subSeq,
			node.spec.kind,
			node.spec.serviceCount,
			node.spec.groupID,
			node.spec.groupSize,
			shardIdentities,
			node.state,
			node.failedShardID,
			node.gangBypasses,
		); err != nil {
			return fmt.Errorf("persist workload %q: %w", node.spec.id, err)
		}
	}
	return nil
}

// insertSchedulerDependencies 在 workload 全部存在后写入 DAG 边。
func insertSchedulerDependencies(ctx context.Context, tx *sql.Tx, kernel *schedulerKernel) error {
	for _, node := range kernel.nodes {
		for _, dependencyID := range node.spec.dependencies {
			if _, err := tx.ExecContext(
				ctx,
				"INSERT INTO scheduler_dependencies (workload_id, dependency_id) VALUES (?, ?)",
				node.spec.id,
				dependencyID,
			); err != nil {
				return fmt.Errorf("persist dependency %q -> %q: %w", node.spec.id, dependencyID, err)
			}
		}
	}
	return nil
}

func insertSchedulerLeases(ctx context.Context, tx *sql.Tx, kernel *schedulerKernel) error {
	for _, lease := range kernel.leases {
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO scheduler_leases (id, workload_id, kind, group_identity, shard_identity) VALUES (?, ?, ?, ?, ?)",
			lease.id,
			lease.workloadID,
			lease.kind,
			lease.groupID,
			lease.shardID,
		); err != nil {
			return fmt.Errorf("persist lease %q: %w", lease.id, err)
		}
	}
	return nil
}

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
