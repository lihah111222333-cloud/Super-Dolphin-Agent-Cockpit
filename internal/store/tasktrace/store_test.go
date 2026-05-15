package tasktrace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type taskTraceQuerierStub struct {
	insertFn func(context.Context, sqlc.InsertTaskTraceParams) (sqlc.TaskTrace, error)
	listFn   func(context.Context, sqlc.ListTaskTracesParams) ([]sqlc.TaskTrace, error)
}

func (s *taskTraceQuerierStub) InsertTaskTrace(ctx context.Context, arg sqlc.InsertTaskTraceParams) (sqlc.TaskTrace, error) {
	if s.insertFn != nil {
		return s.insertFn(ctx, arg)
	}
	return sqlc.TaskTrace{}, nil
}

func (s *taskTraceQuerierStub) ListTaskTraces(ctx context.Context, arg sqlc.ListTaskTracesParams) ([]sqlc.TaskTrace, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func TestInsertForwardsAllColumnsAndMapsResult(t *testing.T) {
	t.Parallel()
	started := time.Unix(1000, 0).UTC()
	finished := started.Add(500 * time.Millisecond)
	var captured sqlc.InsertTaskTraceParams

	s := &store{q: &taskTraceQuerierStub{
		insertFn: func(_ context.Context, arg sqlc.InsertTaskTraceParams) (sqlc.TaskTrace, error) {
			captured = arg
			return sqlc.TaskTrace{
				ID:            11,
				TraceID:       arg.TraceID,
				SpanID:        arg.SpanID,
				ParentSpanID:  arg.ParentSpanID,
				SpanName:      arg.SpanName,
				Component:     arg.Component,
				Status:        arg.Status,
				InputPayload:  []byte(arg.Column6),
				OutputPayload: []byte(arg.Column7),
				ErrorText:     arg.ErrorText,
				DurationMs:    arg.DurationMs,
				Metadata:      []byte(arg.Column11),
				StartedAt:     started,
				FinishedAt:    &finished,
			}, nil
		},
	}}

	in := TaskTrace{
		TraceID:       "trace-1",
		SpanID:        "span-1",
		ParentSpanID:  "parent-1",
		SpanName:      "Do",
		Component:     "orch",
		Status:        "ok",
		InputPayload:  []byte(`{"a":1}`),
		OutputPayload: []byte(`{"b":2}`),
		ErrorText:     "",
		DurationMs:    500,
		Metadata:      []byte(`{"tag":"x"}`),
	}
	got, err := s.Insert(context.Background(), in)
	if err != nil {
		t.Fatalf("Insert() unexpected error: %v", err)
	}
	assertInsertTaskTraceParams(t, captured)
	assertInsertedTaskTraceRow(t, got, finished)
}

func assertInsertTaskTraceParams(t *testing.T, captured sqlc.InsertTaskTraceParams) {
	t.Helper()
	if captured.TraceID != "trace-1" || captured.SpanID != "span-1" || captured.Component != "orch" ||
		captured.Status != "ok" || captured.DurationMs != 500 ||
		string(captured.Column6) != `{"a":1}` || string(captured.Column7) != `{"b":2}` ||
		string(captured.Column11) != `{"tag":"x"}` {
		t.Fatalf("Insert() forwarded wrong params: %+v", captured)
	}
}

func assertInsertedTaskTraceRow(t *testing.T, got *TaskTrace, finished time.Time) {
	t.Helper()
	if got == nil || got.ID != 11 || got.TraceID != "trace-1" || got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("Insert() row mapped incorrectly: %+v", got)
	}
	if string(got.InputPayload) != `{"a":1}` || string(got.Metadata) != `{"tag":"x"}` {
		t.Fatalf("Insert() JSON fields mapped incorrectly: in=%s meta=%s", got.InputPayload, got.Metadata)
	}
}

func TestInsertWrapsError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("insert failed")
	s := &store{q: &taskTraceQuerierStub{
		insertFn: func(context.Context, sqlc.InsertTaskTraceParams) (sqlc.TaskTrace, error) {
			return sqlc.TaskTrace{}, sentinel
		},
	}}
	_, err := s.Insert(context.Background(), TaskTrace{})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Insert() err = %v, want wrap of sentinel", err)
	}
}

func TestListForwardsFilterIncludingSinceAndKeyword(t *testing.T) {
	t.Parallel()
	since := time.Unix(200, 0).UTC()
	started := time.Unix(300, 0).UTC()
	var captured sqlc.ListTaskTracesParams

	s := &store{q: &taskTraceQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListTaskTracesParams) ([]sqlc.TaskTrace, error) {
			captured = arg
			return []sqlc.TaskTrace{{
				ID:        1,
				TraceID:   "t",
				SpanID:    "s",
				Component: "orch",
				Status:    "ok",
				StartedAt: started,
			}}, nil
		},
	}}

	got, err := s.List(context.Background(), ListFilter{
		Component: "orch",
		Since:     &since,
		Keyword:   "kw",
		Limit:     7,
	})
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if captured.Column1 != "orch" || captured.Column3 != "kw" || captured.Limit != 7 {
		t.Fatalf("List() forwarded wrong simple params: %+v", captured)
	}
	if !captured.Column2.Valid || !captured.Column2.Time.Equal(since) {
		t.Fatalf("List() Since forwarded incorrectly: %+v", captured.Column2)
	}
	if len(got) != 1 || got[0].Component != "orch" || !got[0].StartedAt.Equal(started) {
		t.Fatalf("List() row mapped incorrectly: %+v", got)
	}
}

func TestListHandlesNilSince(t *testing.T) {
	t.Parallel()
	var captured sqlc.ListTaskTracesParams
	s := &store{q: &taskTraceQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListTaskTracesParams) ([]sqlc.TaskTrace, error) {
			captured = arg
			return nil, nil
		},
	}}
	if _, err := s.List(context.Background(), ListFilter{Limit: 1}); err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if captured.Column2.Valid {
		t.Fatalf("List() nil Since should map to invalid pgtype, got %+v", captured.Column2)
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("list failed")
	s := &store{q: &taskTraceQuerierStub{
		listFn: func(context.Context, sqlc.ListTaskTracesParams) ([]sqlc.TaskTrace, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), ListFilter{})
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("List() err = %v, want wrap of sentinel", err)
	}
}
