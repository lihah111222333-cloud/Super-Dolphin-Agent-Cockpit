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

func (e *StoreError) Unwrap() error { return e.Err }

func (e *StoreError) Is(target error) bool {
	if e.Kind != nil && target == e.Kind {
		return true
	}
	return errors.Is(e.Err, target)
}

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

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows)
}

func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict) || IsUniqueViolation(err)
}

func IsTimeout(err error) bool {
	if errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "57014"
}

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
