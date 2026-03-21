package tasktrace

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Insert(ctx context.Context, trace TaskTrace) (*TaskTrace, error) {
	row, err := s.q.InsertTaskTrace(ctx, sqlc.InsertTaskTraceParams{
		TraceID:       trace.TraceID,
		SpanID:        trace.SpanID,
		ParentSpanID:  trace.ParentSpanID,
		SpanName:      trace.SpanName,
		Component:     trace.Component,
		Column6:       trace.InputPayload,
		Column7:       trace.OutputPayload,
		Status:        trace.Status,
		ErrorText:     trace.ErrorText,
		DurationMs:    trace.DurationMs,
		Column11:      trace.Metadata,
	})
	if err != nil {
		return nil, wrapTaskTraceError(err, "insert")
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]TaskTrace, error) {
	rows, err := s.q.ListTaskTraces(ctx, sqlc.ListTaskTracesParams{
		Column1: filter.Component,
		Column2: toTraceSince(filter.Since),
		Column3: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, wrapTaskTraceError(err, "list")
	}
	traces := make([]TaskTrace, 0, len(rows))
	for _, row := range rows {
		traces = append(traces, fromSQLC(row))
	}
	return traces, nil
}

func fromSQLC(row sqlc.TaskTrace) TaskTrace {
	return TaskTrace{
		ID:            row.ID,
		TraceID:       row.TraceID,
		SpanID:        row.SpanID,
		ParentSpanID:  row.ParentSpanID,
		SpanName:      row.SpanName,
		Component:     row.Component,
		Status:        row.Status,
		InputPayload:  json.RawMessage(row.InputPayload),
		OutputPayload: json.RawMessage(row.OutputPayload),
		ErrorText:     row.ErrorText,
		Metadata:      json.RawMessage(row.Metadata),
		StartedAt:     row.StartedAt,
		FinishedAt:    row.FinishedAt,
		DurationMs:    row.DurationMs,
	}
}

func toTraceSince(ts *time.Time) pgtype.Timestamptz {
	if ts == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *ts, Valid: true}
}

func wrapTaskTraceError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "task_trace")
}
