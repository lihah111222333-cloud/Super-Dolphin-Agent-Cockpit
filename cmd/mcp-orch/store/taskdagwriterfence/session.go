package taskdagwriterfence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// WriterSession is an isolated, tx-aware B10 writer candidate.
//
// It deliberately owns no taskdag store and is not registered with Fx. The
// session only supplies the fence gate around an IMMEDIATE transaction; a
// future taskdag adapter can construct its tx-bound store from the callback's
// DBTX without opening a second transaction.
type WriterSession struct {
	owner *Owner
	q     *sqlc.Queries
}

// OpenWriterSession 在显式 admission 与 DB-resident fence 校验后打开候选 session。
// It opens a candidate session after validating the explicit
// admission against the DB-resident fence. It does not bootstrap or migrate a
// database and must not be called with the production taskdag DB yet.
func OpenWriterSession(ctx context.Context, db *sql.DB, admission Admission) (*WriterSession, error) {
	if isNilValue(ctx) || db == nil {
		return nil, errors.Join(ErrInvalidAdmission, errors.New("writer session database or context is nil"))
	}
	owner, err := Open(ctx, db, admission)
	if err != nil {
		return nil, err
	}
	return &WriterSession{owner: owner, q: sqlc.New(db)}, nil
}

// WriterFence 返回 session 捕获的不可变 admission token。
// It returns the immutable admission token captured by this session.
func (s *WriterSession) WriterFence() WriterFence {
	if s == nil || s.owner == nil {
		return WriterFence{}
	}
	return s.owner.WriterFence()
}

// WithMutation 在共享 taskdag 事务形状中执行一次带 fence 的 mutation。
// It runs one fenced mutation in the shared taskdag transaction
// shape: BEGIN IMMEDIATE, fence CAS, callback, COMMIT. The callback receives
// the transaction-bound query set and DBTX; it must perform DB work only.
//
// The whole transaction is retried only by sqlctx's bounded busy/locked
// policy. A stale CAS never invokes callback. If callback execution succeeded
// but the transaction then failed (commit/connection outcome unknown), the
// session is fenced so callers cannot guess and repeat the business write.
// A callback that itself returns a busy/locked DB error may be retried, so it
// must remain DB-only and safe to replay before it reports success.
func (s *WriterSession) WithMutation(ctx context.Context, callback func(*sqlc.Queries, sqlc.DBTX) error) error {
	if err := validateWriterSessionMutation(ctx, s, callback); err != nil {
		return err
	}
	if s.owner.fenced.Load() {
		return ErrAlreadyFenced
	}

	callbackRan := false
	callbackSucceeded := false
	var callbackErr error
	err := sqlctx.WithImmediateTx(ctx, s.owner.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		return s.mutationAttempt(ctx, txq, txdb, callback, &callbackRan, &callbackSucceeded, &callbackErr)
	})
	return s.finishMutation(err, callbackRan, callbackSucceeded, callbackErr)
}

// mutationAttempt 执行一次事务尝试，并在 sqlctx 重试时阻止已成功 callback 重放。
func (s *WriterSession) mutationAttempt(ctx context.Context, txq *sqlc.Queries, txdb sqlc.DBTX, callback func(*sqlc.Queries, sqlc.DBTX) error, callbackRan, callbackSucceeded *bool, callbackErr *error) error {
	if *callbackSucceeded {
		// sqlctx may retry after a busy/locked COMMIT. The prior callback
		// already completed, so never execute business SQL a second time.
		return ErrCommitUnknown
	}
	if err := casWriterFenceDBTX(ctx, txdb, s.owner.fence); err != nil {
		if errors.Is(err, ErrStaleWriter) {
			s.owner.fenced.Store(true)
		}
		return err
	}
	*callbackRan = true
	*callbackErr = callback(txq, txdb)
	*callbackSucceeded = *callbackErr == nil
	return *callbackErr
}

// finishMutation 将 callback、commit/连接失败分别映射为可判定的 session 结果。
func (s *WriterSession) finishMutation(err error, callbackRan, callbackSucceeded bool, callbackErr error) error {
	if err == nil {
		return nil
	}
	if callbackSucceeded {
		s.owner.fenced.Store(true)
		if errors.Is(err, ErrCommitUnknown) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrCommitUnknown, err)
	}
	if callbackRan && callbackErr == nil {
		// The callback panicked or otherwise completed without a result; the
		// transaction outcome is not safe to replay.
		s.owner.fenced.Store(true)
	}
	return err
}

// validateWriterSessionMutation 在开启事务前拒绝空 session、查询集、上下文或 callback。
func validateWriterSessionMutation(ctx context.Context, session *WriterSession, callback func(*sqlc.Queries, sqlc.DBTX) error) error {
	if isNilValue(ctx) || isNilValue(session) || session.owner == nil || session.owner.db == nil || session.q == nil || callback == nil {
		return errors.Join(ErrInvalidAdmission, errors.New("writer session or callback is nil"))
	}
	return nil
}

// isNilValue 检测接口中携带的 typed-nil，避免把无效依赖传入 database/sql。
func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func casWriterFenceDBTX(ctx context.Context, tx sqlc.DBTX, fence WriterFence) error {
	if isNilValue(tx) {
		return errors.Join(ErrInvalidAdmission, errors.New("writer transaction is nil"))
	}
	result, err := tx.ExecContext(ctx, casFenceSQL, fence.SchemaVersion, fence.WriterEpoch, fence.DeploymentFence)
	if err != nil {
		return fmt.Errorf("writer fence CAS: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect writer fence CAS: %w", err)
	}
	if rows != 1 {
		return ErrStaleWriter
	}
	return nil
}
