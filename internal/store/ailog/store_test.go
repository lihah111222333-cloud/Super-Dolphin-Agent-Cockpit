package ailog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type ailogQuerierStub struct {
	countAILogsByStatusFn  func(context.Context) ([]sqlc.CountAILogsByStatusRow, error)
	listAILogSystemLogsFn  func(context.Context, sqlc.ListAILogSystemLogsParams) ([]sqlc.SystemLog, error)
	listAILogsByCategoryFn func(context.Context, sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error)
	listRecentAILogsFn     func(context.Context, int32) ([]sqlc.ListRecentAILogsRow, error)
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

func (s *ailogQuerierStub) ListRecentAILogs(ctx context.Context, limit int32) ([]sqlc.ListRecentAILogsRow, error) {
	if s.listRecentAILogsFn != nil {
		return s.listRecentAILogsFn(ctx, limit)
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
			if arg.Category != "api_request" || arg.LimitCount != 7 {
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

	rows, err := s.ListByCategory(context.Background(), "api_request", 7)
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
		listRecentAILogsFn: func(_ context.Context, limit int32) ([]sqlc.ListRecentAILogsRow, error) {
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
