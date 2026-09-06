package taskdagwriterfence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS taskdag_writer_fence (
  singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
  schema_version INTEGER NOT NULL CHECK (schema_version = 1),
  writer_epoch INTEGER NOT NULL CHECK (writer_epoch > 0),
  deployment_fence TEXT NOT NULL CHECK (
    length(deployment_fence) BETWEEN 1 AND 512 AND
    trim(deployment_fence) = deployment_fence AND instr(deployment_fence, char(0)) = 0
  )
) STRICT;`

const (
	selectFenceSQL  = `SELECT schema_version, writer_epoch, deployment_fence FROM taskdag_writer_fence WHERE singleton = 1`
	casFenceSQL     = `UPDATE taskdag_writer_fence SET writer_epoch = writer_epoch WHERE singleton = 1 AND schema_version = ? AND writer_epoch = ? AND deployment_fence = ?`
	advanceFenceSQL = `UPDATE taskdag_writer_fence SET writer_epoch = ?, deployment_fence = ? WHERE singleton = 1 AND schema_version = ? AND writer_epoch = ? AND deployment_fence = ?`
)

// Bootstrap 只在调用方提供的候选 DB 中创建并 seed fence 表。
// Callers must pass a dedicated candidate database; this function does not open
// or migrate the production shared taskdag database.
func Bootstrap(ctx context.Context, db *sql.DB, initial WriterFence) error {
	if ctx == nil || db == nil {
		return fmt.Errorf("%w: nil database or context", ErrInvalidAdmission)
	}
	if err := initial.Validate(); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("bootstrap fence transaction: %w", err)
	}
	return bootstrapFenceTx(ctx, tx, initial)
}

// bootstrapFenceTx 在已开启事务中创建并校验唯一 fence 行。
func bootstrapFenceTx(ctx context.Context, tx *sql.Tx, initial WriterFence) error {
	rollback := func(cause error) error { _ = tx.Rollback(); return cause }
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return rollback(fmt.Errorf("bootstrap fence schema: %w", err))
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM taskdag_writer_fence`).Scan(&count); err != nil {
		return rollback(fmt.Errorf("inspect fence row: %w", err))
	}
	if count == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO taskdag_writer_fence(singleton, schema_version, writer_epoch, deployment_fence) VALUES(1, ?, ?, ?)`, initial.SchemaVersion, initial.WriterEpoch, initial.DeploymentFence); err != nil {
			return rollback(fmt.Errorf("seed fence row: %w", err))
		}
	} else if count != 1 {
		return rollback(fmt.Errorf("%w: expected one fence row, got %d", ErrInvalidAdmission, count))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("bootstrap fence commit: %w", err)
	}
	return nil
}

// Owner is a process-local view of a DB-resident writer fence.
type Owner struct {
	db     *sql.DB
	fence  WriterFence
	fenced atomic.Bool
}

// Open 校验 admission 与 DB 中持久化 fence 完全相等。
func Open(ctx context.Context, db *sql.DB, admission Admission) (*Owner, error) {
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("%w: nil database or context", ErrInvalidAdmission)
	}
	if err := admission.Fence.Validate(); err != nil {
		return nil, err
	}
	got, err := readFence(ctx, db)
	if err != nil {
		return nil, err
	}
	if got != admission.Fence {
		return nil, fmt.Errorf("%w: persisted fence mismatch", ErrInvalidAdmission)
	}
	return &Owner{db: db, fence: admission.Fence}, nil
}

func readFence(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (WriterFence, error) {
	var got WriterFence
	if err := q.QueryRowContext(ctx, selectFenceSQL).Scan(&got.SchemaVersion, &got.WriterEpoch, &got.DeploymentFence); err != nil {
		return WriterFence{}, fmt.Errorf("%w: missing or unreadable fence row: %v", ErrInvalidAdmission, err)
	}
	if err := got.Validate(); err != nil {
		return WriterFence{}, err
	}
	return got, nil
}

// WriterFence 返回 owner 打开时捕获的不可变 token。
func (o *Owner) WriterFence() WriterFence {
	if o == nil {
		return WriterFence{}
	}
	return o.fence
}

// Mutate 先执行 DB-resident CAS，再运行业务 mutation；stale 时整笔事务回滚。
func (o *Owner) Mutate(ctx context.Context, callback func(*sql.Tx) error) error {
	if err := validateMutationOwner(ctx, o, callback); err != nil {
		return err
	}
	if o.fenced.Load() {
		return ErrAlreadyFenced
	}
	tx, err := o.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin writer mutation: %w", err)
	}
	// 无论 callback 是否 panic，事务都必须在连接归还前结束；Commit 后
	// 的 Rollback 只返回 sql.ErrTxDone，不会改写已提交结果。
	defer tx.Rollback()
	if err := casWriterFenceTx(ctx, tx, o.fence); err != nil {
		_ = tx.Rollback()
		if errors.Is(err, ErrStaleWriter) {
			o.fenced.Store(true)
		}
		return err
	}
	if err := callback(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		o.fenced.Store(true)
		return fmt.Errorf("writer mutation commit: %w", err)
	}
	return nil
}

// validateMutationOwner 在开启事务前拒绝空依赖，避免 database/sql 接收 nil context。
func validateMutationOwner(ctx context.Context, owner *Owner, callback func(*sql.Tx) error) error {
	if ctx == nil || owner == nil || owner.db == nil || callback == nil {
		return fmt.Errorf("%w: owner or callback", ErrInvalidAdmission)
	}
	return nil
}

// Advance 原子安装严格更高的 deployment fence，供 successor cutover 使用。
func Advance(ctx context.Context, db *sql.DB, expected, next WriterFence) error {
	if ctx == nil || db == nil {
		return fmt.Errorf("%w: nil database or context", ErrInvalidAdmission)
	}
	if err := expected.Validate(); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if next.WriterEpoch <= expected.WriterEpoch {
		return fmt.Errorf("%w: next epoch must increase", ErrInvalidAdmission)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin fence advance: %w", err)
	}
	return advanceFenceTx(ctx, tx, expected, next)
}

func casWriterFenceTx(ctx context.Context, tx *sql.Tx, fence WriterFence) error {
	return casWriterFenceDBTX(ctx, tx, fence)
}

func advanceFenceTx(ctx context.Context, tx *sql.Tx, expected, next WriterFence) error {
	result, err := tx.ExecContext(ctx, advanceFenceSQL, next.WriterEpoch, next.DeploymentFence, expected.SchemaVersion, expected.WriterEpoch, expected.DeploymentFence)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("advance fence: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("inspect fence advance: %w", err)
	}
	if rows != 1 {
		_ = tx.Rollback()
		return ErrStaleWriter
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("advance fence commit: %w", err)
	}
	return nil
}
