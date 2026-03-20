package tasktrace

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
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
		InputPayload:  trace.InputPayload,
		OutputPayload: trace.OutputPayload,
		Status:        trace.Status,
		ErrorText:     trace.ErrorText,
		DurationMs:    trace.DurationMs,
		Metadata:      trace.Metadata,
	})
	if err != nil {
		return nil, err
	}
	mapped := fromSQLC(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]TaskTrace, error) {
	rows, err := s.q.ListTaskTraces(ctx, sqlc.ListTaskTracesParams{
		Component: filter.Component,
		Since:     filter.Since,
		Keyword:   filter.Keyword,
		Limit:     filter.Limit,
	})
	if err != nil {
		return nil, err
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
		InputPayload:  row.InputPayload,
		OutputPayload: row.OutputPayload,
		ErrorText:     row.ErrorText,
		Metadata:      row.Metadata,
		StartedAt:     row.StartedAt,
		FinishedAt:    row.FinishedAt,
		DurationMs:    row.DurationMs,
	}
}
