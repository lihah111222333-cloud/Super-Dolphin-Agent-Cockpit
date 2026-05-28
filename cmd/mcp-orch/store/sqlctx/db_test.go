package sqlctx

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type nonTransactionalDBTX struct{}

func (nonTransactionalDBTX) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (nonTransactionalDBTX) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (nonTransactionalDBTX) QueryRow(context.Context, string, ...interface{}) pgx.Row {
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
