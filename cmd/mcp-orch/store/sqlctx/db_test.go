package sqlctx

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

type nonTransactionalDBTX struct{}

func (nonTransactionalDBTX) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, nil
}

func (nonTransactionalDBTX) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, nil
}

func (nonTransactionalDBTX) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (nonTransactionalDBTX) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}

func TestWithTxOrReuseRejectsNonTransactionalDBTX(t *testing.T) {
	t.Parallel()

	db := nonTransactionalDBTX{}
	called := false
	err := WithTxOrReuse(context.Background(), db, sqlc.New(db), func(*sqlc.Queries, sqlc.DBTX) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "transaction-capable DBTX") {
		t.Fatalf("WithTxOrReuse() error = %v, want transaction-capable DBTX", err)
	}
	if called {
		t.Fatal("WithTxOrReuse() called callback without a transaction-capable DBTX")
	}
}
