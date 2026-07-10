package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrNotFound = contract.ErrNotFound
	ErrConflict = contract.ErrConflict
	ErrTimeout  = errors.New("store: timeout")
)

// StoreError 为 store 层错误补充操作、实体和分类信息。
// Err 保留原始原因，Kind 用于 errors.Is 归类，调用方可以同时拿到人类可读上下文和稳定错误类型。
type StoreError struct {
	Operation string
	Entity    string
	Kind      error
	Err       error
}

// Error 按已有操作和实体字段拼出 store 错误文本。
func (e *StoreError) Error() string {
	switch {
	case e.Operation == "" && e.Entity == "":
		return e.Err.Error()
	case e.Entity == "":
		return fmt.Sprintf("%s: %v", e.Operation, e.Err)
	case e.Operation == "":
		return fmt.Sprintf("%s: %v", e.Entity, e.Err)
	default:
		return fmt.Sprintf("%s %s: %v", e.Operation, e.Entity, e.Err)
	}
}

// Unwrap 返回底层错误，保留 errors.Is/As 穿透能力。
func (e *StoreError) Unwrap() error { return e.Err }

// Is 先按 store 分类匹配，再回落到底层错误链。
func (e *StoreError) Is(target error) bool {
	if e.Kind != nil && target == e.Kind {
		return true
	}
	return errors.Is(e.Err, target)
}

// WrapStoreError 为 store 错误附加操作和实体上下文。
// 已经是 StoreError 时保持原样，避免重复包装改变外层错误分类。
func WrapStoreError(err error, operation, entity string) error {
	if err == nil {
		return nil
	}
	var storeErr *StoreError
	if errors.As(err, &storeErr) {
		return err
	}
	return &StoreError{
		Operation: operation,
		Entity:    entity,
		Kind:      classifyStoreError(err),
		Err:       err,
	}
}

// IsNotFound 判断错误是否表示记录不存在。
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

// IsConflict 判断错误是否表示持久化冲突或唯一约束冲突。
func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict) || IsUniqueViolation(err)
}

// IsTimeout 判断错误是否属于超时或 SQLite 写入锁竞争。
func IsTimeout(err error) bool {
	if errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return IsSQLiteBusyLocked(err)
}

// IsUniqueViolation 判断 SQLite UNIQUE 约束错误。
func IsUniqueViolation(err error) bool {
	code, ok := sqliteResultCode(err)
	return ok && code == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// sqliteResultCode 从错误链中提取 modernc SQLite 的稳定结果码。
// 非 SQLite 驱动错误不参与 SQLite 分类，避免把可变的人类文本误判为数据库状态。
func sqliteResultCode(err error) (int, bool) {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return 0, false
	}
	return sqliteErr.Code(), true
}

func classifyStoreError(err error) error {
	switch {
	case IsNotFound(err):
		return ErrNotFound
	case IsConflict(err):
		return ErrConflict
	case IsTimeout(err):
		return ErrTimeout
	default:
		return nil
	}
}
