package ailog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

type ailogQuerierStub struct {
	countAILogsByStatusFn  func(context.Context) ([]string, error)
	listAILogSystemLogsFn  func(context.Context, sqlc.ListAILogSystemLogsParams) ([]sqlc.ListAILogSystemLogsRow, error)
	listAILogsByCategoryFn func(context.Context, sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error)
	listRecentAILogsFn     func(context.Context, sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error)
}

func (s *ailogQuerierStub) CountAILogsByStatus(ctx context.Context) ([]string, error) {
	if s.countAILogsByStatusFn != nil {
		return s.countAILogsByStatusFn(ctx)
	}
	return nil, nil
}

func (s *ailogQuerierStub) ListAILogSystemLogs(ctx context.Context, arg sqlc.ListAILogSystemLogsParams) ([]sqlc.ListAILogSystemLogsRow, error) {
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

	ts := time.Unix(100, 0).UTC()
	duration := int64(25)
	s := &store{q: &ailogQuerierStub{
		listAILogSystemLogsFn: func(_ context.Context, arg sqlc.ListAILogSystemLogsParams) ([]sqlc.ListAILogSystemLogsRow, error) {
			if arg.Keyword != "req" || arg.KeywordPattern != "%req%" || arg.LimitCount != 5 {
				t.Fatalf("List() forwarded wrong params: %+v", arg)
			}
			return []sqlc.ListAILogSystemLogsRow{{
				ID:           1,
				Ts:           ts.UnixMilli(),
				Level:        "info",
				Logger:       "ai",
				Message:      "request",
				Raw:          "",
				Source:       "ai",
				Component:    "model",
				AgentID:      "agent-1",
				ThreadID:     "thread-1",
				TraceID:      "trace-1",
				SpanID:       "span-1",
				ParentSpanID: "parent-1",
				EventType:    "evt",
				ToolName:     "tool",
				DurationMs:   &duration,
				Extra:        `{}`,
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
	if string(rows[0].Extra) != `{}` {
		t.Fatalf("List() Extra = %s", rows[0].Extra)
	}
	if rows[0].TraceID != "trace-1" || rows[0].SpanID != "span-1" || rows[0].ParentSpanID != "parent-1" {
		t.Fatalf("List() trace fields = trace:%q span:%q parent:%q", rows[0].TraceID, rows[0].SpanID, rows[0].ParentSpanID)
	}
}

func TestListByCategoryDerivesFieldsInGo(t *testing.T) {
	t.Parallel()

	ts := time.Unix(200, 0).UTC()
	s := &store{q: &ailogQuerierStub{
		listAILogsByCategoryFn: func(_ context.Context, arg sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error) {
			if arg.Column1 != "models" || arg.LOWER != "%models%" || arg.Column3 != "api_request" || arg.Message != "api_request" || arg.Limit != 7 {
				t.Fatalf("ListByCategory() forwarded wrong params: %+v", arg)
			}
			return []sqlc.ListAILogsByCategoryRow{
				{
					ID:      2,
					Ts:      ts.UnixMilli(),
					Message: "api request GET https://example.test/v1/models HTTP/1.1 200 OK model=gpt-5",
					Extra:   `{}`,
				},
				{
					ID:      3,
					Ts:      ts.UnixMilli(),
					Message: "runtime config loaded",
					Extra:   `{}`,
				},
			}, nil
		},
	}}

	rows, err := s.ListByCategory(context.Background(), "api_request", "models", 7)
	if err != nil {
		t.Fatalf("ListByCategory() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListByCategory() len = %d, want 1: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Category != "api_request" || got.Method != "GET" || got.Endpoint != "/v1/models" || got.Status != "200" || got.Model != "gpt-5" {
		t.Fatalf("ListByCategory() derived fields = %+v", got)
	}
}

func TestCountByStatusDerivesHTTPStatusCountsInGo(t *testing.T) {
	t.Parallel()

	s := &store{q: &ailogQuerierStub{
		countAILogsByStatusFn: func(context.Context) ([]string, error) {
			return []string{
				"api request GET https://example.test/v1/models HTTP/1.1 200 OK model=gpt-5",
				"api error POST https://example.test/v1/models HTTP/1.1 500 Internal model=gpt-5",
				"api error POST https://example.test/v1/models HTTP/1.1 500 Internal model=gpt-5",
				"runtime config loaded",
			}, nil
		},
	}}

	rows, err := s.CountByStatus(context.Background())
	if err != nil {
		t.Fatalf("CountByStatus() error = %v", err)
	}
	if len(rows) != 2 || rows[0].Status != "200" || rows[0].Count != 1 || rows[1].Status != "500" || rows[1].Count != 2 {
		t.Fatalf("CountByStatus() = %+v", rows)
	}
}

func TestListRecentDerivesFieldsInGo(t *testing.T) {
	t.Parallel()

	ts := time.Unix(300, 0).UTC()
	rawExtra := json.RawMessage(`{}`)
	s := &store{q: &ailogQuerierStub{
		listRecentAILogsFn: func(_ context.Context, arg sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error) {
			if arg.LimitCount != 9 {
				t.Fatalf("ListRecent() limit = %d, want 9", arg.LimitCount)
			}
			return []sqlc.ListRecentAILogsRow{{
				ID:      3,
				Ts:      ts.UnixMilli(),
				Message: "api error POST https://example.test/v1/chat HTTP/1.1 500 Internal model=gpt-5-mini",
				Extra:   string(rawExtra),
			}}, nil
		},
	}}

	rows, err := s.ListRecent(context.Background(), 9)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Model != "gpt-5-mini" || rows[0].Category != "api_error" || string(rows[0].Extra) != string(rawExtra) {
		t.Fatalf("ListRecent() = %+v", rows)
	}
}

func TestDeriveCategoryPreservesSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "api request", message: "API REQUEST GET https://api.test/v1/models", want: "api_request"},
		{name: "api error before fallback", message: "api error fallback HTTP/1.1 500 Internal", want: "api_error"},
		{name: "compat before runtime and error", message: "runtime config fallback exception", want: "compat_fallback"},
		{name: "unicode compat", message: "\u517c\u5bb9 mode enabled", want: "compat_fallback"},
		{name: "runtime config", message: "runtime config loaded", want: "runtime_config"},
		{name: "generic error", message: "worker exception without api markers", want: "error"},
		{name: "default", message: "model stream completed", want: "ai_event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := deriveCategory(tt.message); got != tt.want {
				t.Fatalf("deriveCategory(%q) = %q, want %q", tt.message, got, tt.want)
			}
		})
	}
}

func TestSQLiteAILogQueriesUseMetadataProjectionAndGoDerivedFields(t *testing.T) {
	db := openAILogSQLiteDB(t)
	insertAILogSystemRow(t, db, 1_700_000_002_000, "info", "api request GET https://api.test/v1/responses HTTP/1.1 200 OK model=gpt-5", "RESPONSES raw payload")
	insertAILogSystemRow(t, db, 1_700_000_001_000, "error", "runtime config fallback exception", "runtime raw payload")

	s := NewStore(sqlc.New(db))
	recent, err := s.ListRecent(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("ListRecent() len = %d, want 1", len(recent))
	}
	assertRecentAILogProjection(t, recent[0])

	filtered, err := s.List(context.Background(), ListFilter{Keyword: "responses", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	assertFilteredAILogProjection(t, filtered)

	categoryRows, err := s.ListByCategory(context.Background(), "compat_fallback", "", 10)
	if err != nil {
		t.Fatalf("ListByCategory() error = %v", err)
	}
	assertCategoryAILogProjection(t, categoryRows)

	counts, err := s.CountByStatus(context.Background())
	if err != nil {
		t.Fatalf("CountByStatus() error = %v", err)
	}
	assertAILogStatusCounts(t, counts)
}

func assertRecentAILogProjection(t *testing.T, row AILog) {
	t.Helper()
	if row.Raw != "" || string(row.Extra) != `{}` {
		t.Fatalf("ListRecent() projected heavy fields: raw=%q extra=%s", row.Raw, row.Extra)
	}
	if row.Endpoint != "/v1/responses" || row.Status != "200" || row.Category != "api_request" {
		t.Fatalf("ListRecent() derived fields = %+v", row)
	}
	assertAILogTraceFields(t, "ListRecent()", row)
}

func assertFilteredAILogProjection(t *testing.T, rows []AILog) {
	t.Helper()
	if len(rows) != 1 || rows[0].Raw != "" || string(rows[0].Extra) != `{}` {
		t.Fatalf("List() = %+v", rows)
	}
	assertAILogTraceFields(t, "List()", rows[0])
}

func assertCategoryAILogProjection(t *testing.T, rows []AILog) {
	t.Helper()
	if len(rows) != 1 || rows[0].Category != "compat_fallback" {
		t.Fatalf("ListByCategory() = %+v", rows)
	}
	assertAILogTraceFields(t, "ListByCategory()", rows[0])
}

func assertAILogStatusCounts(t *testing.T, counts []StatusCount) {
	t.Helper()
	if len(counts) != 1 || counts[0].Status != "200" || counts[0].Count != 1 {
		t.Fatalf("CountByStatus() = %+v", counts)
	}
}

func assertAILogTraceFields(t *testing.T, label string, row AILog) {
	t.Helper()
	if row.TraceID != "trace-1" || row.SpanID != "span-1" || row.ParentSpanID != "parent-1" {
		t.Fatalf("%s trace fields = trace:%q span:%q parent:%q", label, row.TraceID, row.SpanID, row.ParentSpanID)
	}
}

func TestSQLiteListByCategoryReturnsLimitMatchingRowsAfterNewerNonMatches(t *testing.T) {
	db := openAILogSQLiteDB(t)
	insertAILogSystemRow(t, db, 1_700_000_003_000, "info", "runtime config loaded", "runtime raw payload")
	insertAILogSystemRow(t, db, 1_700_000_002_000, "error", "api error POST https://api.test/v1/messages HTTP/1.1 500 Internal model=gpt-5", "error raw payload")
	insertAILogSystemRow(t, db, 1_700_000_001_000, "info", "api request GET https://api.test/v1/responses HTTP/1.1 200 OK model=gpt-5", "request raw payload")

	s := NewStore(sqlc.New(db))
	rows, err := s.ListByCategory(context.Background(), "api_request", "", 1)
	if err != nil {
		t.Fatalf("ListByCategory() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListByCategory() len = %d, want 1: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Category != "api_request" || got.Status != "200" || got.Endpoint != "/v1/responses" {
		t.Fatalf("ListByCategory() = %+v", got)
	}
	if got.Raw != "" || string(got.Extra) != `{}` {
		t.Fatalf("ListByCategory() projected heavy fields: raw=%q extra=%s", got.Raw, got.Extra)
	}
}

func TestSQLiteCountByStatusDerivesHTTPStatusCounts(t *testing.T) {
	db := openAILogSQLiteDB(t)
	insertAILogSystemRow(t, db, 1_700_000_004_000, "info", "api request GET https://api.test/v1/responses HTTP/1.1 200 OK model=gpt-5", "request raw payload")
	insertAILogSystemRow(t, db, 1_700_000_003_000, "error", "api error POST https://api.test/v1/messages HTTP/1.1 500 Internal model=gpt-5", "error raw payload")
	insertAILogSystemRow(t, db, 1_700_000_002_000, "error", "api error POST https://api.test/v1/messages HTTP/1.1 500 Internal model=gpt-5", "error raw payload")
	insertAILogSystemRow(t, db, 1_700_000_001_000, "info", "runtime config loaded", "runtime raw payload")

	s := NewStore(sqlc.New(db))
	counts, err := s.CountByStatus(context.Background())
	if err != nil {
		t.Fatalf("CountByStatus() error = %v", err)
	}
	if len(counts) != 2 {
		t.Fatalf("CountByStatus() len = %d, want 2: %+v", len(counts), counts)
	}
	if counts[0].Status != "200" || counts[0].Count != 1 || counts[1].Status != "500" || counts[1].Count != 2 {
		t.Fatalf("CountByStatus() = %+v", counts)
	}
}

func openAILogSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "platform", "db", "sqlite", "migrations", "001_baseline.sql"))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("exec baseline: %v", err)
	}
	return db
}

func insertAILogSystemRow(t *testing.T, db *sql.DB, ts int64, level string, message string, raw string) {
	t.Helper()

	_, err := db.Exec(`
	INSERT INTO system_logs (
	    ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, span_id, parent_span_id, event_type, tool_name, duration_ms, extra
	) VALUES (?, ?, 'ai', ?, ?, 'provider', 'model', 'agent-1', 'thread-1', 'trace-1', 'span-1', 'parent-1', 'event', 'tool', 25, ?);
`, ts, level, message, raw, `{"big":"payload"}`)
	if err != nil {
		t.Fatalf("insert system log: %v", err)
	}
}

func TestAILogWrapsErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("db closed")
	s := &store{q: &ailogQuerierStub{
		listRecentAILogsFn: func(context.Context, sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error) {
			return nil, sentinel
		},
	}}
	_, err := s.ListRecent(context.Background(), 1)
	if !errors.Is(err, sentinel) {
		t.Fatalf("ListRecent() error = %v, want sentinel", err)
	}
}
