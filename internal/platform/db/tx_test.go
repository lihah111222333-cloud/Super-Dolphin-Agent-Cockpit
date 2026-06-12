package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRunWithTxCommitsOnSuccess(t *testing.T) {
	t.Parallel()

	tx := &captureReadOnlyTx{}
	err := runWithTx(context.Background(), tx, func(pgx.Tx) error { return nil })
	if err != nil {
		t.Fatalf("runWithTx() error = %v, want nil", err)
	}
	if !tx.committed {
		t.Fatal("runWithTx() did not commit on success")
	}
	if tx.rolledBack {
		t.Fatal("runWithTx() rolled back on success path")
	}
}

func TestRunWithTxRollsBackOnError(t *testing.T) {
	t.Parallel()

	fnErr := errors.New("write failed")
	tx := &captureReadOnlyTx{}
	err := runWithTx(context.Background(), tx, func(pgx.Tx) error { return fnErr })
	if !errors.Is(err, fnErr) {
		t.Fatalf("runWithTx() error = %v, want fnErr", err)
	}
	if !tx.rolledBack {
		t.Fatal("runWithTx() did not roll back on error")
	}
	if tx.committed {
		t.Fatal("runWithTx() committed after fn error")
	}
}

func TestRunWithTxRollsBackOnPanicAndRepanics(t *testing.T) {
	t.Parallel()

	tx := &captureReadOnlyTx{}
	panicVal := "boom"

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("runWithTx() did not re-panic")
		}
		if r != panicVal {
			t.Fatalf("runWithTx() re-panicked with %v, want %v", r, panicVal)
		}
		if !tx.rolledBack {
			t.Fatal("runWithTx() did not roll back on panic")
		}
		if tx.committed {
			t.Fatal("runWithTx() committed despite panic")
		}
	}()

	_ = runWithTx(context.Background(), tx, func(pgx.Tx) error {
		// archguard:ignore panic_count -- this test verifies transaction rollback preserves caller panic.
		panic(panicVal)
	})
	t.Fatal("unreachable: panic should have propagated")
}

func TestRollbackTxJoinsFunctionAndRollbackErrors(t *testing.T) {
	t.Parallel()

	fnErr := errors.New("write failed")
	rollbackErr := errors.New("rollback failed")
	tx := &captureReadOnlyTx{rollbackErr: rollbackErr}
	err := rollbackTx(context.Background(), tx, fnErr)
	if !errors.Is(err, fnErr) {
		t.Fatalf("rollbackTx() error = %v, want function error", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rollbackTx() error = %v, want rollback error", err)
	}
	if !tx.rolledBack {
		t.Fatal("rollbackTx() did not roll back")
	}
	if tx.committed {
		t.Fatal("rollbackTx() committed after function error")
	}
}

func TestOpenReadOnlyRowsExecutesSetTransactionReadOnly(t *testing.T) {
	t.Parallel()

	tx := &captureReadOnlyTx{rows: emptyPlatformRows()}

	queryer := &captureReadOnlyBeginner{tx: tx}
	rows, finish, err := OpenReadOnlyRows(context.Background(), queryer, "SELECT * FROM agent_threads")
	if err != nil {
		t.Fatalf("OpenReadOnlyRows() error = %v", err)
	}
	if rows == nil {
		t.Fatal("OpenReadOnlyRows() rows is nil")
	}
	if queryer.beginOptions.AccessMode != pgx.ReadOnly {
		t.Fatalf("BeginTx() access mode = %q, want %q", queryer.beginOptions.AccessMode, pgx.ReadOnly)
	}
	if len(tx.execSQLs) != 1 || tx.execSQLs[0] != "SET TRANSACTION READ ONLY" {
		t.Fatalf("tx.Exec() sqls = %#v, want SET TRANSACTION READ ONLY", tx.execSQLs)
	}
	if tx.querySQL != "SELECT * FROM agent_threads" {
		t.Fatalf("tx.Query() sql = %q", tx.querySQL)
	}
	if err := finish(true); err != nil {
		t.Fatalf("finish(true) error = %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction state commit=%v rollback=%v", tx.committed, tx.rolledBack)
	}
}

type captureReadOnlyBeginner struct {
	tx           *captureReadOnlyTx
	beginOptions pgx.TxOptions
}

func (*captureReadOnlyBeginner) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("captureReadOnlyBeginner: exec not implemented")
}

func (*captureReadOnlyBeginner) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("captureReadOnlyBeginner: direct query should not be used")
}

func (*captureReadOnlyBeginner) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func (q *captureReadOnlyBeginner) BeginTx(_ context.Context, txOptions pgx.TxOptions) (pgx.Tx, error) {
	q.beginOptions = txOptions
	if q.tx == nil {
		q.tx = &captureReadOnlyTx{rows: emptyPlatformRows()}
	}
	return q.tx, nil
}

type captureReadOnlyTx struct {
	rows        pgx.Rows
	execSQLs    []string
	querySQL    string
	commitErr   error
	rollbackErr error
	committed   bool
	rolledBack  bool
}

func (*captureReadOnlyTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("captureReadOnlyTx: begin not implemented")
}

func (tx *captureReadOnlyTx) Commit(context.Context) error {
	tx.committed = true
	return tx.commitErr
}

func (tx *captureReadOnlyTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return tx.rollbackErr
}

func (*captureReadOnlyTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("captureReadOnlyTx: copyfrom not implemented")
}

func (*captureReadOnlyTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }

func (*captureReadOnlyTx) LargeObjects() pgx.LargeObjects { return pgx.LargeObjects{} }

func (*captureReadOnlyTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("captureReadOnlyTx: prepare not implemented")
}

func (tx *captureReadOnlyTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.execSQLs = append(tx.execSQLs, sql)
	return pgconn.CommandTag{}, nil
}

func (tx *captureReadOnlyTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	tx.querySQL = sql
	if tx.rows != nil {
		return tx.rows, nil
	}
	return emptyPlatformRows(), nil
}

func (*captureReadOnlyTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

func (*captureReadOnlyTx) Conn() *pgx.Conn { return nil }

type platformStubRows struct{}

func (r *platformStubRows) Close() { _ = r }

func (*platformStubRows) Err() error { return nil }

func (*platformStubRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (*platformStubRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (*platformStubRows) Next() bool { return false }

func (*platformStubRows) Scan(...any) error {
	return errors.New("platformStubRows: scan not implemented")
}

func (*platformStubRows) Values() ([]any, error) {
	return nil, errors.New("platformStubRows: values not implemented")
}

func (*platformStubRows) RawValues() [][]byte { return nil }

func (*platformStubRows) Conn() *pgx.Conn { return nil }

func emptyPlatformRows() pgx.Rows { return &platformStubRows{} }
