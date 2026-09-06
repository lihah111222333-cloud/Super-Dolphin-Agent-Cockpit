package sqliteauthority

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type immediateTransaction struct {
	ctx       context.Context
	conn      *sql.Conn
	open      bool
	committed bool
}

// withRawImmediate 在单连接上执行显式 BEGIN IMMEDIATE；commit error 始终分类为结果未知。
func withRawImmediate(ctx context.Context, db *sql.DB, callback func(*sql.Conn) error) (resultErr error) {
	if ctx == nil || db == nil || callback == nil {
		return ErrAuthorityTransaction
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return safeAuthorityError("acquire authority connection", err)
	}
	transaction := &immediateTransaction{ctx: ctx, conn: conn}
	defer transaction.finish(&resultErr)
	if err := transaction.begin(); err != nil {
		return err
	}
	if err := callback(conn); err != nil {
		return transaction.rollback(err)
	}
	return transaction.commit()
}

// withWriterFenceImmediate 在 BEGIN IMMEDIATE 后先执行 DB-resident fence CAS，
// 再进入业务 callback；该顺序是 authority 写入的唯一默认入口。
func withWriterFenceImmediate(ctx context.Context, db *sql.DB, fence WriterFence, callback func(*sql.Conn) error) error {
	if ctx == nil || db == nil || callback == nil {
		return ErrAuthorityTransaction
	}
	return withRawImmediate(ctx, db, func(conn *sql.Conn) error {
		if err := casWriterFence(ctx, conn, fence); err != nil {
			return err
		}
		return callback(conn)
	})
}

// finish 在返回或 panic 展开时关闭连接；不调用 recover，让原 panic 自动继续传播。
func (t *immediateTransaction) finish(resultErr *error) {
	if t.open {
		t.rollbackAfterPanic()
	}
	t.close(resultErr)
}

// rollbackAfterPanic 在事务仍打开时以不可取消上下文尽力回滚。
func (t *immediateTransaction) rollbackAfterPanic() {
	if !t.open {
		return
	}
	_, rollbackErr := t.conn.ExecContext(context.WithoutCancel(t.ctx), "ROLLBACK")
	t.open = false
	if rollbackErr != nil {
		t.discard()
	}
}

// close 关闭独占连接；已成功提交后的关闭失败不得改写 mutation 成功结果。
func (t *immediateTransaction) close(resultErr *error) {
	closeErr := t.conn.Close()
	if closeErr == nil || *resultErr != nil || t.committed {
		return
	}
	*resultErr = safeAuthorityError("close authority connection", closeErr)
}

// begin 在独占连接上启动 BEGIN IMMEDIATE 事务。
func (t *immediateTransaction) begin() error {
	if _, err := t.conn.ExecContext(t.ctx, "BEGIN IMMEDIATE"); err != nil {
		return safeAuthorityError("begin authority transaction", err)
	}
	t.open = true
	return nil
}

// rollback 回滚 callback 失败的事务，并保留 callback 与回滚错误。
func (t *immediateTransaction) rollback(callbackErr error) error {
	rollbackErr := t.execRollback()
	if rollbackErr != nil {
		t.discard()
		return errors.Join(callbackErr, safeAuthorityError("rollback authority transaction", rollbackErr))
	}
	return callbackErr
}

// commit 提交事务；COMMIT 报错时回滚或丢弃连接，结果仍始终分类为未知。
func (t *immediateTransaction) commit() error {
	_, commitErr := t.conn.ExecContext(context.WithoutCancel(t.ctx), "COMMIT")
	if commitErr == nil {
		t.open = false
		t.committed = true
		return nil
	}
	commitOutcomeErr := errors.Join(ErrCommitOutcomeUnknown, safeAuthorityError("commit authority transaction", commitErr))
	if rollbackErr := t.execRollback(); rollbackErr != nil {
		t.discard()
		return errors.Join(commitOutcomeErr, safeAuthorityError("rollback uncertain authority transaction", rollbackErr))
	}
	return commitOutcomeErr
}

// execRollback 在不可取消上下文中结束当前事务。
func (t *immediateTransaction) execRollback() error {
	_, err := t.conn.ExecContext(context.WithoutCancel(t.ctx), "ROLLBACK")
	t.open = false
	return err
}

// discard 将回滚失败的底层连接标记为不可复用，避免未决事务污染连接池。
func (t *immediateTransaction) discard() {
	_ = t.conn.Raw(func(any) error { return driver.ErrBadConn })
}

// writeTransaction 在租约临界区内执行 authority 写，并在租约或 commit 不确定时永久熔断实例。
func (a *Authority) writeTransaction(ctx context.Context, callback func(*sql.Conn) error) error {
	if err := validateWriteTransaction(a, callback); err != nil {
		return err
	}
	if a.queryOnly.Load() {
		return ErrAuthorityReadOnly
	}
	err := a.lease.WithValidatedScope(ctx, a.scope, func() error {
		if err := a.validateDatabaseIdentity(); err != nil {
			return err
		}
		transactionErr := a.runLeaseProtectedTransaction(ctx, callback)
		if err := a.validateDatabaseIdentity(); err != nil {
			return errors.Join(transactionErr, err, ErrCommitOutcomeUnknown)
		}
		return transactionErr
	})
	if writerFailure(err) {
		a.queryOnly.Store(true)
	}
	return err
}

// validateDatabaseIdentity 在每次 authority 读写前后重验主库及 WAL/SHM
// 文件对象身份；sidecar 缺失会解除旧绑定，重新出现时绑定新对象。
func (a *Authority) validateDatabaseIdentity() error {
	if a == nil || a.db == nil {
		return ErrAuthorityTransaction
	}
	a.identityMu.Lock()
	defer a.identityMu.Unlock()
	expected := a.pathIdentity
	if !expected.main.present {
		expected.main = a.identity
	}
	observed, err := observeAuthorityPathIdentity(a.config.Path, expected, false)
	if err != nil {
		return err
	}
	a.pathIdentity = observed
	a.identity = observed.main
	return nil
}

// withAuthorityReadPath 让 kernel lease 在 SQLite 读取期间保持临界区；
// 非 kernel fake lease 保留原有无锁测试 seam，同时执行前后身份复核。
func (a *Authority) withAuthorityReadPath(ctx context.Context, callback func() error) error {
	if a == nil || a.db == nil || a.lease == nil || callback == nil {
		return ErrAuthorityTransaction
	}
	read := func() error {
		if err := a.validateDatabaseIdentity(); err != nil {
			return err
		}
		callbackErr := callback()
		if err := a.validateDatabaseIdentity(); err != nil {
			return errors.Join(callbackErr, err)
		}
		return callbackErr
	}
	if _, ok := a.lease.(*KernelAuthorityLease); ok {
		err := a.lease.WithValidatedScope(ctx, a.scope, read)
		if writerFailure(err) {
			a.queryOnly.Store(true)
		}
		return err
	}
	err := read()
	if writerFailure(err) {
		a.queryOnly.Store(true)
	}
	return err
}

// runLeaseProtectedTransaction 在 lease mutex 仍持有时先记录不可恢复故障，
// 防止下一位等待者在熔断标记写入前尝试第二次 mutation。
func (a *Authority) runLeaseProtectedTransaction(ctx context.Context, callback func(*sql.Conn) error) error {
	err := a.runFencedTransaction(ctx, callback)
	if writerFailure(err) {
		a.queryOnly.Store(true)
	}
	return err
}

// validateWriteTransaction 拒绝缺失 authority 依赖或业务 callback。
func validateWriteTransaction(a *Authority, callback func(*sql.Conn) error) error {
	if a == nil || a.db == nil || a.lease == nil || a.transactionRunner == nil || callback == nil {
		return ErrAuthorityTransaction
	}
	return nil
}

// runFencedTransaction 在租约临界区内执行带首句 DB fence CAS 的事务。
func (a *Authority) runFencedTransaction(ctx context.Context, callback func(*sql.Conn) error) error {
	if a.queryOnly.Load() {
		return ErrAuthorityReadOnly
	}
	return a.transactionRunner(ctx, a.db, a.writerFence, callback)
}

// writerFailure 列出必须把 authority 永久熔断为 query-only 的故障分类。
func writerFailure(err error) bool {
	return errors.Is(err, ErrLeaseRejected) || errors.Is(err, ErrCommitOutcomeUnknown) ||
		errors.Is(err, ErrStaleWriter) || errors.Is(err, ErrWriterFenceSchema)
}

// requireOneRow 要求 compare-and-swap 精确更新一行，否则返回 resolution conflict。
func requireOneRow(result sql.Result, action string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return safeAuthorityError("inspect "+action, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: %s compare-and-swap", recoverycustody.ErrResolutionConflict, action)
	}
	return nil
}
