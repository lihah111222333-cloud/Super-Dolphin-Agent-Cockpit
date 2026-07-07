package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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

func TestRunWithTxRollsBackOnPanicReturnsError(t *testing.T) {
	t.Parallel()
	tx := &captureTx{}
	panicVal := "boom"
	err := runWithCommitter(context.Background(), tx, func(sqlTxCommitter) error {
		// archguard:ignore panic_count -- 本测试验证事务回滚后必须返回显式错误。
		panic(panicVal)
	})
	if err == nil {
		t.Fatal("runWithTx() error = nil, want explicit panic error")
	}
	if !strings.Contains(err.Error(), "transaction callback panicked: "+panicVal) {
		t.Fatalf("runWithTx() error = %v, want explicit panic context", err)
	}
	if !tx.rolledBack {
		t.Fatal("runWithTx() did not roll back on panic")
	}
	if tx.committed {
		t.Fatal("runWithTx() committed despite panic")
	}
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

func TestWithImmediateTxAcquiresWriteLockBeforeCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "immediate.db")
	db1 := openImmediateTxTestDB(t, path)
	db2 := openImmediateTxTestDB(t, path)
	if _, err := db1.ExecContext(context.Background(), `CREATE TABLE locks (id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create locks table: %v", err)
	}

	err := WithImmediateTx(context.Background(), db1, func(tx *sql.Tx) error {
		if tx == nil {
			return errors.New("callback tx is nil")
		}
		_, secondErr := db2.ExecContext(context.Background(), `INSERT INTO locks(value) VALUES ('second writer')`)
		if secondErr == nil {
			return errors.New("second writer acquired lock while immediate transaction callback was running")
		}
		if !strings.Contains(secondErr.Error(), "locked") && !strings.Contains(secondErr.Error(), "busy") {
			return secondErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithImmediateTx() error = %v, want second writer locked before callback body", err)
	}
}

func openImmediateTxTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
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
