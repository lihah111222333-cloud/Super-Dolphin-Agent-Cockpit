package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var (
	ErrNotFound = contract.ErrNotFound
	ErrConflict = contract.ErrConflict
	ErrTimeout  = errors.New("store: timeout")
)

type StoreError struct {
	Operation string
	Entity    string
	Kind      error
	Err       error
}

// Error 返回错误文本。
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

// Unwrap 返回底层错误。
func (e *StoreError) Unwrap() error { return e.Err }

// Is 判断平台数据库是否可用。
func (e *StoreError) Is(target error) bool {
	if e.Kind != nil && target == e.Kind {
		return true
	}
	return errors.Is(e.Err, target)
}

// WrapStoreError 包装存储错误。
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

// IsNotFound 判断notfound是否可用。
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

// IsConflict 判断conflict是否可用。
func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict) || IsUniqueViolation(err)
}

// IsTimeout 判断超时是否可用。
func IsTimeout(err error) bool {
	if errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return IsSQLiteBusyLocked(err)
}

// IsUniqueViolation 判断 SQLite UNIQUE 约束错误。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "unique constraint failed")
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
