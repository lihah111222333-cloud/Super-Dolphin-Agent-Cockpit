package systemlog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type systemLogQuerierStub struct {
	listFn   func(context.Context, sqlc.ListSystemLogsParams) ([]sqlc.SystemLog, error)
	insertFn func(context.Context, sqlc.InsertSystemLogParams) error
}

func (s *systemLogQuerierStub) ListSystemLogs(ctx context.Context, arg sqlc.ListSystemLogsParams) ([]sqlc.SystemLog, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func (s *systemLogQuerierStub) InsertSystemLog(ctx context.Context, arg sqlc.InsertSystemLogParams) error {
	if s.insertFn != nil {
		return s.insertFn(ctx, arg)
	}
	return nil
}

func TestListForwardsAll9ColumnsAndLimit(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0).UTC()
	duration := int64(42)
	var captured sqlc.ListSystemLogsParams

	s := &store{q: &systemLogQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListSystemLogsParams) ([]sqlc.SystemLog, error) {
			captured = arg
			return []sqlc.SystemLog{{
				ID:         1,
				Ts:         now.UnixMilli(),
				Level:      "info",
				Logger:     "app",
				Message:    "hello",
				Raw:        "raw",
				Source:     "src",
				Component:  "cmp",
				AgentID:    "a1",
				ThreadID:   "t1",
				TraceID:    "tr1",
				EventType:  "evt",
				ToolName:   "tool",
				DurationMs: &duration,
				Extra:      []byte(`{"ok":true}`),
			}}, nil
		},
	}}

	filter := ListFilter{
		Level:     "info",
		Logger:    "app",
		Source:    "src",
		Component: "cmp",
		AgentID:   "a1",
		ThreadID:  "t1",
		EventType: "evt",
		ToolName:  "tool",
		Keyword:   "hel",
		Limit:     10,
	}
	got, err := s.List(context.Background(), filter)
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	assertSystemLogListParams(t, captured)
	assertSystemLogListRow(t, got)
}

func assertSystemLogListParams(t *testing.T, captured sqlc.ListSystemLogsParams) {
	t.Helper()
	for _, check := range []struct {
		name string
		got  string
		want string
	}{
		{name: "level", got: captured.Level, want: "info"},
		{name: "logger", got: captured.Logger, want: "app"},
		{name: "source", got: captured.Source, want: "src"},
		{name: "component", got: captured.Component, want: "cmp"},
		{name: "agent", got: captured.AgentID, want: "a1"},
		{name: "thread", got: captured.ThreadID, want: "t1"},
		{name: "event", got: captured.EventType, want: "evt"},
		{name: "tool", got: captured.ToolName, want: "tool"},
		{name: "keyword", got: captured.Column17.(string), want: "hel"},
	} {
		if check.got != check.want {
			t.Fatalf("List() %s param = %q, want %q; all params=%+v", check.name, check.got, check.want, captured)
		}
	}
	if captured.Limit != 10 {
		t.Fatalf("List() Limit = %d, want 10; all params=%+v", captured.Limit, captured)
	}
}

func assertSystemLogListRow(t *testing.T, got []SystemLog) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("List() len = %d, want 1", len(got))
	}
	row := got[0]
	assertSystemLogCoreFields(t, row)
	if string(row.Extra) != `{"ok":true}` {
		t.Fatalf("List() Extra = %s", row.Extra)
	}
}

func assertSystemLogCoreFields(t *testing.T, row SystemLog) {
	t.Helper()
	if row.ID != 1 {
		t.Fatalf("List() row ID = %d, want 1", row.ID)
	}
	if row.Level != "info" {
		t.Fatalf("List() row Level = %q, want info", row.Level)
	}
	if row.AgentID != "a1" {
		t.Fatalf("List() row AgentID = %q, want a1", row.AgentID)
	}
	if row.DurationMs == nil {
		t.Fatalf("List() row DurationMs = nil, want 42")
	}
	if *row.DurationMs != 42 {
		t.Fatalf("List() row DurationMs = %d, want 42", *row.DurationMs)
	}
}

func TestListReturnsEmptyWhenNoRows(t *testing.T) {
	t.Parallel()
	s := &store{q: &systemLogQuerierStub{}}
	got, err := s.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	// This store returns `make([]SystemLog, len(rows))` which is non-nil even
	// for empty input.
	if got == nil {
		t.Fatal("List() returned nil, expected empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("List() len = %d, want 0", len(got))
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("db closed")
	s := &store{q: &systemLogQuerierStub{
		listFn: func(context.Context, sqlc.ListSystemLogsParams) ([]sqlc.SystemLog, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), ListFilter{})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("List() err = %v, want wrap of sentinel", err)
	}
}

func TestInsertForwardsParamsAndWrapsError(t *testing.T) {
	t.Parallel()
	var captured sqlc.InsertSystemLogParams
	s := &store{q: &systemLogQuerierStub{
		insertFn: func(_ context.Context, arg sqlc.InsertSystemLogParams) error {
			captured = arg
			return nil
		},
	}}
	params := InsertParams{Level: "warn", Logger: "audit", Message: "denied", Raw: "raw-line"}
	if err := s.Insert(context.Background(), params); err != nil {
		t.Fatalf("Insert() unexpected error: %v", err)
	}
	if captured.Level != "warn" || captured.Logger != "audit" || captured.Message != "denied" || captured.Raw != "raw-line" {
		t.Fatalf("Insert() forwarded wrong params: %+v", captured)
	}

	sentinel := errors.New("write failed")
	s = &store{q: &systemLogQuerierStub{
		insertFn: func(context.Context, sqlc.InsertSystemLogParams) error { return sentinel },
	}}
	if err := s.Insert(context.Background(), InsertParams{}); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Insert() err = %v, want wrap of sentinel", err)
	}
}
