package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestServiceQueryPassesThroughAndNormalizesArgs(t *testing.T) {
	t.Parallel()

	db := &queryDBTXStub{rows: &queryRowsStub{
		fields: []pgconn.FieldDescription{{Name: "thread_id"}},
		values: [][]any{{"thread-1"}},
	}}
	svc := NewService(nil, nil, nil, nil, nil, nil, dbquerystore.NewQueryStore(db, time.Second), nil, nil, nil, nil, nil)

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
	svc := NewService(nil, nil, nil, nil, nil, nil, dbquerystore.NewQueryStore(db, time.Second), nil, nil, nil, nil, nil)

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

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	_, err := svc.Query(context.Background(), "SELECT * FROM agent_threads")
	if err == nil || !strings.Contains(err.Error(), "dbquery store is not configured") {
		t.Fatalf("Query() error = %v", err)
	}
}

func TestDashboardQueryHandlerRegistered(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handlers := NewDashboardHandlers(svc).Handlers
	if _, ok := handlers["dashboard/query"]; !ok {
		t.Fatalf("dashboard/query handler missing from %#v", handlers)
	}
}

func TestDashboardExtraHandlersRegistered(t *testing.T) {
	t.Parallel()

	svc := NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handlers := NewDashboardHandlers(svc).Handlers
	for _, method := range []string{
		"dashboard/auditLogs",
		"dashboard/busLogs",
		"dashboard/dags",
		"dashboard/dagDetail",
	} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("%s handler missing from %#v", method, handlers)
		}
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

func TestDashboardAuditAndBusLogHandlersReturnLogs(t *testing.T) {
	t.Parallel()

	auditStore := &stubAuditLogStore{
		listResult: []auditlogstore.AuditEvent{{ID: 7, EventType: "tool", Action: "run"}},
	}
	busStore := &stubBusLogStore{
		listResult: []buslogstore.BusExceptionLog{{ID: 9, Category: "rpc", Severity: "error"}},
	}
	server := newDashboardTestServer(t, &service{auditLogs: auditStore, busLogs: busStore})

	var auditResp struct {
		Logs []auditlogstore.AuditEvent `json:"logs"`
	}
	if err := dispatchDashboardInto(server, "dashboard/auditLogs", `{"eventType":"tool","action":"run","actor":"agent-1","limit":7}`, &auditResp); err != nil {
		t.Fatalf("dispatch audit logs error = %v", err)
	}
	if auditStore.listFilter.EventType != "tool" || auditStore.listFilter.Action != "run" || auditStore.listFilter.Actor != "agent-1" || auditStore.listFilter.Limit != 7 {
		t.Fatalf("audit filter = %#v", auditStore.listFilter)
	}
	if len(auditResp.Logs) != 1 || auditResp.Logs[0].ID != 7 {
		t.Fatalf("audit response = %#v", auditResp)
	}

	var busResp struct {
		Logs []buslogstore.BusExceptionLog `json:"logs"`
	}
	if err := dispatchDashboardInto(server, "dashboard/busLogs", `{"category":"rpc","severity":"error","keyword":"timeout","limit":9}`, &busResp); err != nil {
		t.Fatalf("dispatch bus logs error = %v", err)
	}
	if busStore.listFilter.Category != "rpc" || busStore.listFilter.Severity != "error" || busStore.listFilter.Keyword != "timeout" || busStore.listFilter.Limit != 9 {
		t.Fatalf("bus filter = %#v", busStore.listFilter)
	}
	if len(busResp.Logs) != 1 || busResp.Logs[0].ID != 9 {
		t.Fatalf("bus response = %#v", busResp)
	}
}

func TestDashboardDAGHandlersReturnData(t *testing.T) {
	t.Parallel()

	orchestration := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Dag One", Status: "running"}},
		dagDetail: contract.DAGDetail{
			DAG:   contract.DAGSummary{DagKey: "dag-1", Title: "Dag One"},
			Nodes: []contract.DAGNode{{NodeKey: "node-1", Title: "Node One"}},
		},
	}
	server := newDashboardTestServer(t, &service{orchestration: orchestration})

	var dagsResp struct {
		Dags []contract.DAGSummary `json:"dags"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dags", `{"keyword":"build","status":"running","limit":7}`, &dagsResp); err != nil {
		t.Fatalf("dispatch dags error = %v", err)
	}
	if orchestration.listDAGsFilter.Keyword != "build" || orchestration.listDAGsFilter.Status != "running" || orchestration.listDAGsFilter.Limit != 7 {
		t.Fatalf("ListDAGs() filter = %#v", orchestration.listDAGsFilter)
	}
	if len(dagsResp.Dags) != 1 || dagsResp.Dags[0].DagKey != "dag-1" {
		t.Fatalf("dags response = %#v", dagsResp)
	}

	var detailResp struct {
		DAG   contract.DAGSummary `json:"dag"`
		Nodes []contract.DAGNode  `json:"nodes"`
	}
	if err := dispatchDashboardInto(server, "dashboard/dagDetail", `{"dagKey":"dag-1"}`, &detailResp); err != nil {
		t.Fatalf("dispatch dag detail error = %v", err)
	}
	if orchestration.getDAGKey != "dag-1" {
		t.Fatalf("GetDAG() key = %q, want dag-1", orchestration.getDAGKey)
	}
	if detailResp.DAG.DagKey != "dag-1" || len(detailResp.Nodes) != 1 || detailResp.Nodes[0].NodeKey != "node-1" {
		t.Fatalf("dag detail response = %#v", detailResp)
	}
}

func TestDashboardDAGHandlersReturnServiceNotAvailableWithoutOrchestration(t *testing.T) {
	t.Parallel()

	server := newDashboardTestServer(t, &service{})

	if err := dispatchDashboardInto(server, "dashboard/dags", `{"keyword":"build"}`, &struct{}{}); err == nil || !strings.Contains(err.Error(), "service not available") {
		t.Fatalf("dispatch dags error = %v, want service not available", err)
	}

	if err := dispatchDashboardInto(server, "dashboard/dagDetail", `{"dagKey":"dag-1"}`, &struct{}{}); err == nil || !strings.Contains(err.Error(), "service not available") {
		t.Fatalf("dispatch dag detail error = %v, want service not available", err)
	}
}

func newDashboardQueryTestServer(t *testing.T, db *queryDBTXStub) *platformrpc.Server {
	t.Helper()

	svc := NewService(nil, nil, nil, nil, nil, nil, dbquerystore.NewQueryStore(db, time.Second), nil, nil, nil, nil, nil)
	return newDashboardTestServer(t, svc)
}

func newDashboardTestServer(t *testing.T, svc Service) *platformrpc.Server {
	t.Helper()

	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewDashboardHandlers(svc).Handlers)
	return server
}

func dispatchDashboardQuery(server *platformrpc.Server, raw string) ([]map[string]any, error) {
	var rows []map[string]any
	if err := dispatchDashboardInto(server, "dashboard/query", raw, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func dispatchDashboardInto(server *platformrpc.Server, method, raw string, out any) error {
	result, err := server.Dispatch(context.Background(), method, json.RawMessage(raw))
	if err != nil {
		return err
	}
	return json.Unmarshal(result, out)
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
