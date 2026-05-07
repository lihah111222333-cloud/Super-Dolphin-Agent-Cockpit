package ailog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ailogQuerierStub struct {
	countAILogsByStatusFn  func(context.Context) ([]sqlc.CountAILogsByStatusRow, error)
	listAILogSystemLogsFn  func(context.Context, sqlc.ListAILogSystemLogsParams) ([]sqlc.SystemLog, error)
	listAILogsByCategoryFn func(context.Context, sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error)
	listRecentAILogsFn     func(context.Context, sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error)
}

func (s *ailogQuerierStub) CountAILogsByStatus(ctx context.Context) ([]sqlc.CountAILogsByStatusRow, error) {
	if s.countAILogsByStatusFn != nil {
		return s.countAILogsByStatusFn(ctx)
	}
	return nil, nil
}

func (s *ailogQuerierStub) ListAILogSystemLogs(ctx context.Context, arg sqlc.ListAILogSystemLogsParams) ([]sqlc.SystemLog, error) {
	if s.listAILogSystemLogsFn != nil {
		return s.listAILogSystemLogsFn(ctx, arg)
	}
	return nil, nil
}

func (s *ailogQuerierStub) ListAILogsByCategory(ctx context.Context, arg sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error) {
	if s.listAILogsByCategoryFn != nil {
		return s.listAILogsByCategoryFn(ctx, arg)
	}
	return nil, nil
}

func (s *ailogQuerierStub) ListRecentAILogs(ctx context.Context, arg sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error) {
	if s.listRecentAILogsFn != nil {
		return s.listRecentAILogsFn(ctx, arg)
	}
	return nil, nil
}

func TestList(t *testing.T) {
	t.Parallel()

	ts := time.Unix(100, 0)
	duration := int32(25)
	s := &store{q: &ailogQuerierStub{
		listAILogSystemLogsFn: func(_ context.Context, arg sqlc.ListAILogSystemLogsParams) ([]sqlc.SystemLog, error) {
			if arg.Keyword != "req" || arg.LimitCount != 5 {
				t.Fatalf("List() forwarded wrong params: %+v", arg)
			}
			return []sqlc.SystemLog{{
				ID:         1,
				Ts:         ts,
				Level:      "info",
				Logger:     "ai",
				Message:    "request",
				Raw:        "raw",
				Source:     "ai",
				Component:  "model",
				AgentID:    "agent-1",
				ThreadID:   "thread-1",
				TraceID:    "trace-1",
				EventType:  "evt",
				ToolName:   "tool",
				DurationMs: &duration,
				Extra:      []byte(`{"ok":true}`),
			}}, nil
		},
	}}

	rows, err := s.List(context.Background(), ListFilter{Keyword: "req", Limit: 5})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Message != "request" || rows[0].DurationMs == nil || *rows[0].DurationMs != 25 {
		t.Fatalf("List() = %+v", rows)
	}
	if string(rows[0].Extra) != `{"ok":true}` {
		t.Fatalf("List() Extra = %s", rows[0].Extra)
	}
}

func TestListByCategory(t *testing.T) {
	t.Parallel()

	ts := time.Unix(200, 0)
	s := &store{q: &ailogQuerierStub{
		listAILogsByCategoryFn: func(_ context.Context, arg sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error) {
			if arg.Category != "api_request" || arg.Keyword != "models" || arg.LimitCount != 7 {
				t.Fatalf("ListByCategory() forwarded wrong params: %+v", arg)
			}
			return []sqlc.ListAILogsByCategoryRow{{
				ID:         2,
				Ts:         ts,
				Message:    "GET https://example.test/v1",
				Extra:      []byte(`{"kind":"api"}`),
				Category:   "api_request",
				Method:     "GET",
				Url:        "https://example.test/v1",
				Endpoint:   "/v1",
				Status:     "200",
				StatusText: "OK",
				Model:      "gpt-5",
			}}, nil
		},
	}}

	rows, err := s.ListByCategory(context.Background(), "api_request", "models", 7)
	if err != nil {
		t.Fatalf("ListByCategory() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Category != "api_request" || rows[0].Endpoint != "/v1" {
		t.Fatalf("ListByCategory() = %+v", rows)
	}
}

func TestCountByStatus(t *testing.T) {
	t.Parallel()

	s := &store{q: &ailogQuerierStub{
		countAILogsByStatusFn: func(context.Context) ([]sqlc.CountAILogsByStatusRow, error) {
			return []sqlc.CountAILogsByStatusRow{
				{Status: "200", Count: 3},
				{Status: "500", Count: 1},
			}, nil
		},
	}}

	rows, err := s.CountByStatus(context.Background())
	if err != nil {
		t.Fatalf("CountByStatus() error = %v", err)
	}
	if len(rows) != 2 || rows[0].Status != "200" || rows[1].Count != 1 {
		t.Fatalf("CountByStatus() = %+v", rows)
	}
}

func TestListRecent(t *testing.T) {
	t.Parallel()

	ts := time.Unix(300, 0)
	rawExtra := json.RawMessage(`{"recent":true}`)
	s := &store{q: &ailogQuerierStub{
		listRecentAILogsFn: func(_ context.Context, arg sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error) {
			limit := arg.Limit
			if limit != 9 {
				t.Fatalf("ListRecent() limit = %d, want 9", limit)
			}
			return []sqlc.ListRecentAILogsRow{{
				ID:         3,
				Ts:         ts,
				Message:    "recent",
				Extra:      []byte(rawExtra),
				Category:   "ai_event",
				Status:     "unknown",
				StatusText: "",
				Model:      "gpt-5-mini",
			}}, nil
		},
	}}

	rows, err := s.ListRecent(context.Background(), 9)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Model != "gpt-5-mini" || string(rows[0].Extra) != string(rawExtra) {
		t.Fatalf("ListRecent() = %+v", rows)
	}
}

func TestListKeywordHandling(t *testing.T) {
	t.Parallel()

	db := &ailogQueryCaptureDB{}
	s := NewStore(sqlc.New(db))

	filtered, err := s.List(context.Background(), ListFilter{Keyword: "req", Limit: 10})
	if err != nil {
		t.Fatalf("List(filtered) error = %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("List(filtered) = %+v", filtered)
	}
	requireAILogQuery(t, db, "req", int32(10))
	requireSQLContains(t, db.query, "WHERE ($1::text = '' OR message ILIKE '%' || $1::text || '%')")

	all, err := s.List(context.Background(), ListFilter{Keyword: "", Limit: 10})
	if err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("List(all) = %+v", all)
	}
	requireAILogQuery(t, db, "", int32(10))
	requireSQLContains(t, db.query, "WHERE ($1::text = '' OR message ILIKE '%' || $1::text || '%')")
}

func TestListByCategoryAllowsEmptyKeyword(t *testing.T) {
	t.Parallel()

	db := &ailogQueryCaptureDB{}
	s := NewStore(sqlc.New(db))

	rows, err := s.ListByCategory(context.Background(), "api_request", "", 3)
	if err != nil {
		t.Fatalf("ListByCategory() error = %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListByCategory() = %+v", rows)
	}
	requireAILogCategoryQuery(t, db, "api_request", "", int32(3))
	requireSQLContains(t, db.query, "WHERE ($1::text = '' OR category = $1::text)")
	requireSQLContains(t, db.query, "AND ($2::text = '' OR message ILIKE '%' || $2::text || '%')")
}

func requireSQLContains(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(compactAILogSQL(got), compactAILogSQL(want)) {
		t.Fatalf("query = %q, want substring %q", got, want)
	}
}

func requireAILogQuery(t *testing.T, db *ailogQueryCaptureDB, keyword string, limit int32) {
	t.Helper()

	if len(db.args) != 2 {
		t.Fatalf("query args = %#v, want 2 args", db.args)
	}
	if got, ok := db.args[0].(string); !ok || got != keyword {
		t.Fatalf("query keyword arg = %#v, want %q", db.args[0], keyword)
	}
	if got, ok := db.args[1].(int32); !ok || got != limit {
		t.Fatalf("query limit arg = %#v, want %d", db.args[1], limit)
	}
}

func requireAILogCategoryQuery(t *testing.T, db *ailogQueryCaptureDB, category, keyword string, limit int32) {
	t.Helper()

	if len(db.args) != 3 {
		t.Fatalf("query args = %#v, want 3 args", db.args)
	}
	if got, ok := db.args[0].(string); !ok || got != category {
		t.Fatalf("query category arg = %#v, want %q", db.args[0], category)
	}
	if got, ok := db.args[1].(string); !ok || got != keyword {
		t.Fatalf("query keyword arg = %#v, want %q", db.args[1], keyword)
	}
	if got, ok := db.args[2].(int32); !ok || got != limit {
		t.Fatalf("query limit arg = %#v, want %d", db.args[2], limit)
	}
}

type ailogQueryCaptureDB struct {
	query string
	args  []any
	err   error
	rows  pgx.Rows
}

func (*ailogQueryCaptureDB) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("ailogQueryCaptureDB: exec not implemented")
}

func (db *ailogQueryCaptureDB) Query(_ context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	db.query = query
	db.args = append([]any(nil), args...)
	if db.err != nil {
		return nil, db.err
	}
	if db.rows != nil {
		return db.rows, nil
	}
	return &ailogEmptyRows{}, nil
}

func (*ailogQueryCaptureDB) QueryRow(context.Context, string, ...interface{}) pgx.Row { return nil }

type ailogEmptyRows struct{}

func (*ailogEmptyRows) Close() {}

func (*ailogEmptyRows) Err() error { return nil }

func (*ailogEmptyRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (*ailogEmptyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (*ailogEmptyRows) Next() bool { return false }

func (*ailogEmptyRows) Scan(...any) error { return errors.New("ailogEmptyRows: scan not implemented") }

func (*ailogEmptyRows) Values() ([]any, error) {
	return nil, errors.New("ailogEmptyRows: values not implemented")
}

func (*ailogEmptyRows) RawValues() [][]byte { return nil }

func (*ailogEmptyRows) Conn() *pgx.Conn { return nil }

func compactAILogSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
