package db

import (
	"context"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunWithTxCommitsOnSuccess(t *testing.T) {
	t.Parallel()
	tx := &captureTx{}
	err := runWithCommitter(context.Background(), tx, func(sqlTxCommitter) error { return nil })
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
	tx := &captureTx{}
	err := runWithCommitter(context.Background(), tx, func(sqlTxCommitter) error { return fnErr })
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
	tx := &captureTx{}
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
	_ = runWithCommitter(context.Background(), tx, func(sqlTxCommitter) error {
		// archguard:ignore panic_count -- 本测试验证事务回滚后必须保留调用方 panic。
		panic(panicVal)
	})
	t.Fatal("unreachable: panic should have propagated")
}

func TestRollbackTxJoinsFunctionAndRollbackErrors(t *testing.T) {
	t.Parallel()
	fnErr := errors.New("write failed")
	rollbackErr := errors.New("rollback failed")
	tx := &captureTx{rollbackErr: rollbackErr}
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

type captureTx struct {
	commitErr   error
	rollbackErr error
	committed   bool
	rolledBack  bool
}

func (tx *captureTx) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *captureTx) Rollback() error {
	tx.rolledBack = true
	return tx.rollbackErr
}
