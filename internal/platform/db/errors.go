package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	return errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows)
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
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "57014"
}

// IsUniqueViolation 判断uniqueviolation是否可用。
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
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
