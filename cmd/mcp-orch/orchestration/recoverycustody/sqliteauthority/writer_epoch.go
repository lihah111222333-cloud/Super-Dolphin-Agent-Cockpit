package sqliteauthority

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// WriterFenceSchemaVersion 标识 writer-fence 行合同，独立于数据库 revision。
const WriterFenceSchemaVersion int64 = 1

const (
	selectWriterFenceSQL = `SELECT schema_version, writer_epoch, deployment_fence
FROM authority_writer_fence WHERE singleton = 1`
	insertWriterFenceSQL = `INSERT INTO authority_writer_fence(singleton, schema_version, writer_epoch, deployment_fence)
VALUES(1, ?, ?, ?)`
	casWriterFenceSQL = `UPDATE authority_writer_fence SET writer_epoch = writer_epoch
WHERE singleton = 1 AND schema_version = ? AND writer_epoch = ? AND deployment_fence = ?`
	advanceWriterFenceSQL = `UPDATE authority_writer_fence SET writer_epoch = ?, deployment_fence = ?
WHERE singleton = 1 AND schema_version = ? AND writer_epoch = ? AND deployment_fence = ?`
)

var (
	// ErrStaleWriter 表示 DB-resident fence 已不再匹配调用方。
	ErrStaleWriter = errors.New("recovery custody SQLite writer is stale")
	// ErrWriterFenceSchema 表示 fence 元数据缺失或格式错误。
	ErrWriterFenceSchema = errors.New("recovery custody SQLite writer fence schema is invalid")
)

// WriterFence 是每个 authority mutation 携带的不可变 token。
type WriterFence struct {
	SchemaVersion   int64
	WriterEpoch     int64
	DeploymentFence string
}

// Validate 拒绝未知 row 版本、非正 epoch 或非规范 deployment fence。
func (f WriterFence) Validate() error {
	if f.SchemaVersion != WriterFenceSchemaVersion || f.WriterEpoch <= 0 {
		return fmt.Errorf("%w: version or epoch", ErrWriterFenceSchema)
	}
	if err := validateText(ErrWriterFenceSchema, "deployment_fence", f.DeploymentFence, 512); err != nil {
		return err
	}
	return nil
}

type writerFenceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// seedWriterFence 在 bootstrap 事务中插入唯一必需的 fence 行；schema 与 token
// 由调用方持有的同一事务一起提交。
func seedWriterFence(ctx context.Context, conn *sql.Conn, initial WriterFence) error {
	if conn == nil {
		return fmt.Errorf("%w: nil connection", ErrWriterFenceSchema)
	}
	if err := initial.Validate(); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, insertWriterFenceSQL, initial.SchemaVersion, initial.WriterEpoch, initial.DeploymentFence); err != nil {
		return fmt.Errorf("%w: seed row", ErrWriterFenceSchema)
	}
	return nil
}

// readWriterFence 读取并校验唯一持久化 writer token。
func readWriterFence(ctx context.Context, queryer writerFenceQueryer) (WriterFence, error) {
	if dependencyIsNil(queryer) {
		return WriterFence{}, fmt.Errorf("%w: nil queryer", ErrWriterFenceSchema)
	}
	var fence WriterFence
	if err := queryer.QueryRowContext(ctx, selectWriterFenceSQL).Scan(
		&fence.SchemaVersion, &fence.WriterEpoch, &fence.DeploymentFence,
	); err != nil {
		return WriterFence{}, fmt.Errorf("%w: read row", ErrWriterFenceSchema)
	}
	if err := fence.Validate(); err != nil {
		return WriterFence{}, err
	}
	return fence, nil
}

// verifyAuthorityWriterFence 要求数据库 token 与配置 token 精确匹配。
func verifyAuthorityWriterFence(ctx context.Context, queryer writerFenceQueryer, expected WriterFence) (WriterFence, error) {
	if err := expected.Validate(); err != nil {
		return WriterFence{}, err
	}
	got, err := readWriterFence(ctx, queryer)
	if err != nil {
		return WriterFence{}, fmt.Errorf("%w: read authority writer fence", ErrSchemaMismatch)
	}
	if got != expected {
		return WriterFence{}, fmt.Errorf("%w: authority writer fence identity", ErrSchemaMismatch)
	}
	return got, nil
}

// takeoverWriterFence 校验前后 token 并原子推进 DB token；调用方应使用
// Authority.Takeover，避免绕过 lease 绑定。
func takeoverWriterFence(ctx context.Context, conn *sql.Conn, expected, next WriterFence) error {
	if conn == nil {
		return fmt.Errorf("%w: nil connection", ErrWriterFenceSchema)
	}
	if err := expected.Validate(); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if next.WriterEpoch <= expected.WriterEpoch {
		return fmt.Errorf("%w: takeover epoch must increase", ErrWriterFenceSchema)
	}
	result, err := conn.ExecContext(ctx, advanceWriterFenceSQL, next.WriterEpoch, next.DeploymentFence,
		expected.SchemaVersion, expected.WriterEpoch, expected.DeploymentFence)
	if err != nil {
		return fmt.Errorf("%w: takeover", ErrWriterFenceSchema)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: inspect takeover", ErrWriterFenceSchema)
	}
	if rows != 1 {
		return ErrStaleWriter
	}
	return nil
}

// casWriterFence 执行每个 authority 写事务的首条 DB-resident CAS 语句。
func casWriterFence(ctx context.Context, conn *sql.Conn, fence WriterFence) error {
	if conn == nil {
		return fmt.Errorf("%w: nil connection", ErrWriterFenceSchema)
	}
	if err := fence.Validate(); err != nil {
		return err
	}
	result, err := conn.ExecContext(ctx, casWriterFenceSQL, fence.SchemaVersion, fence.WriterEpoch, fence.DeploymentFence)
	if err != nil {
		return fmt.Errorf("%w: writer CAS", ErrWriterFenceSchema)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: inspect writer CAS", ErrWriterFenceSchema)
	}
	if rows != 1 {
		return ErrStaleWriter
	}
	return nil
}

// Takeover 在 authority lease 下推进持久 writer token，并立即熔断当前实例；
// successor 必须用 next 重新打开 authority。
func (a *Authority) Takeover(ctx context.Context, next WriterFence) error {
	if a == nil || a.db == nil || a.lease == nil {
		return ErrAuthorityTransaction
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if a.queryOnly.Load() {
		return ErrAuthorityReadOnly
	}
	if next.WriterEpoch <= a.writerFence.WriterEpoch {
		return fmt.Errorf("%w: takeover epoch must increase", ErrWriterFenceSchema)
	}
	return a.writeTransaction(ctx, func(conn *sql.Conn) error {
		if err := takeoverWriterFence(ctx, conn, a.writerFence, next); err != nil {
			return err
		}
		// Mark before COMMIT so no concurrent caller can enter another mutation
		// while the lease callback is still held. A commit error remains sticky.
		a.queryOnly.Store(true)
		return nil
	})
}

// WriterFence 返回 authority 打开时捕获的不可变 writer token。
func (a *Authority) WriterFence() WriterFence {
	if a == nil {
		return WriterFence{}
	}
	return a.writerFence
}
