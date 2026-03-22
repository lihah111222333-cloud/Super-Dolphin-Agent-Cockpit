package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestServiceQueryPassesThroughAndNormalizesArgs(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{rows: &queryRowsStub{
		fields: []pgconn.FieldDescription{{Name: "thread_id"}},
		values: [][]any{{"thread-1"}},
	}}
	svc := NewService(nil, nil, nil, dbquerystore.NewStore(sqlc.New(db), time.Second), nil, nil, nil, nil, nil)

	rows, err := svc.Query(context.Background(), "SELECT * FROM agent_threads WHERE thread_id = $1 AND score > $2", float64(7), float64(1.5))
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(rows) != 1 || rows[0]["thread_id"] != "thread-1" {
		t.Fatalf("Query() rows = %#v", rows)
	}
	if got, ok := db.args[0].(int64); !ok || got != 7 {
		t.Fatalf("arg[0] = %#v", db.args[0])
	}
	if got, ok := db.args[1].(float64); !ok || got != 1.5 {
		t.Fatalf("arg[1] = %#v", db.args[1])
	}
}

func TestServiceQueryRejectsDangerousSQL(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{}
	svc := NewService(nil, nil, nil, dbquerystore.NewStore(sqlc.New(db), time.Second), nil, nil, nil, nil, nil)

	_, err := svc.Query(context.Background(), "SELECT version() FROM agent_threads")
	if err == nil || !strings.Contains(err.Error(), "disallowed function") {
		t.Fatalf("Query() error = %v", err)
	}
	if db.called {
		t.Fatal("Query() reached DBTX.Query for dangerous SQL")
	}
}

func TestServiceQueryRequiresStore(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.Query(context.Background(), "SELECT * FROM agent_threads")
	if err == nil || !strings.Contains(err.Error(), "dbquery store is not configured") {
		t.Fatalf("Query() error = %v", err)
	}
}

func TestDashboardQueryHandlerRegistered(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handlers := NewDashboardHandlers(svc).Handlers
	if _, ok := handlers["dashboard/query"]; !ok {
		t.Fatalf("dashboard/query handler missing from %#v", handlers)
	}
}

func TestDashboardQueryHandlerRejectsDangerousSQL(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{}
	server := newDashboardQueryTestServer(t, db)

	_, err := dispatchDashboardQuery(server, `{"query":"SELECT pg_sleep(1) FROM agent_threads"}`)
	if err == nil || !strings.Contains(err.Error(), "disallowed function") {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if db.called {
		t.Fatal("Dispatch() reached DBTX.Query for dangerous SQL")
	}
}

func TestDashboardQueryHandlerNormalArgs(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{rows: &queryRowsStub{
		fields: []pgconn.FieldDescription{{Name: "thread_id"}},
		values: [][]any{{"thread-1"}},
	}}
	server := newDashboardQueryTestServer(t, db)

	rows, err := dispatchDashboardQuery(server, `{"query":"SELECT * FROM agent_threads WHERE thread_id = $1","args":["thread-1"]}`)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(rows) != 1 || rows[0]["thread_id"] != "thread-1" {
		t.Fatalf("Dispatch() rows = %#v", rows)
	}
	if len(db.args) != 1 || db.args[0] != "thread-1" {
		t.Fatalf("captured args = %#v", db.args)
	}
}

func TestDashboardQueryHandlerFloat64Normalization(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{rows: &queryRowsStub{
		fields: []pgconn.FieldDescription{{Name: "thread_id"}},
		values: [][]any{{"thread-1"}},
	}}
	server := newDashboardQueryTestServer(t, db)

	_, err := dispatchDashboardQuery(server, `{"query":"SELECT * FROM agent_threads WHERE thread_id = $1","args":[7]}`)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(db.args) != 1 {
		t.Fatalf("len(args) = %d, want 1", len(db.args))
	}
	if got, ok := db.args[0].(int64); !ok || got != 7 {
		t.Fatalf("args[0] = %#v", db.args[0])
	}
}

func newDashboardQueryTestServer(t *testing.T, db *queryDBTXStub) *platformrpc.Server {
	t.Helper()

	svc := NewService(nil, nil, nil, dbquerystore.NewStore(sqlc.New(db), time.Second), nil, nil, nil, nil, nil)
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)
	return server
}

func dispatchDashboardQuery(server *platformrpc.Server, raw string) ([]map[string]any, error) {
	result, err := server.Dispatch(context.Background(), "dashboard/query", json.RawMessage(raw))
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(result, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

type queryDBTXStub struct {
	args   []any
	called bool
	rows   pgx.Rows
	err    error
}

func (s *queryDBTXStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("queryDBTXStub: exec not implemented")
}

func (s *queryDBTXStub) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	s.called = true
	s.args = append([]any(nil), args...)
	if s.err != nil {
		return nil, s.err
	}
	if s.rows != nil {
		return s.rows, nil
	}
	return &queryRowsStub{}, nil
}

func (s *queryDBTXStub) QueryRow(context.Context, string, ...any) pgx.Row {
	return queryRowStub{}
}

type queryRowStub struct{}

func (queryRowStub) Scan(...any) error { return errors.New("queryRowStub: scan not implemented") }

type queryRowsStub struct {
	fields []pgconn.FieldDescription
	values [][]any
	index  int
	err    error
}

func (r *queryRowsStub) Close() {}

func (r *queryRowsStub) Err() error { return r.err }

func (r *queryRowsStub) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *queryRowsStub) FieldDescriptions() []pgconn.FieldDescription { return r.fields }

func (r *queryRowsStub) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *queryRowsStub) Scan(...any) error { return errors.New("queryRowsStub: scan not implemented") }

func (r *queryRowsStub) Values() ([]any, error) {
	if r.index == 0 || r.index > len(r.values) {
		return nil, errors.New("queryRowsStub: invalid cursor")
	}
	return append([]any(nil), r.values[r.index-1]...), nil
}

func (r *queryRowsStub) RawValues() [][]byte { return nil }

func (r *queryRowsStub) Conn() *pgx.Conn { return nil }
