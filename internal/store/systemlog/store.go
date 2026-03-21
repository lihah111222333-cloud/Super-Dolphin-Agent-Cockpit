package systemlog

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]SystemLog, error) {
	rows, err := s.q.ListSystemLogs(ctx, sqlc.ListSystemLogsParams{
		Level:     filter.Level,
		Logger:    filter.Logger,
		Source:    filter.Source,
		Component: filter.Component,
		AgentID:   filter.AgentID,
		ThreadID:  filter.ThreadID,
		EventType: filter.EventType,
		ToolName:  filter.ToolName,
		Keyword:   filter.Keyword,
		Limit:     filter.Limit,
	})
	if err != nil {
		return nil, wrapSystemLogError(err, "list")
	}
	result := make([]SystemLog, len(rows))
	for i, row := range rows {
		result[i] = mapSystemLog(row)
	}
	return result, nil
}

func (s *store) Insert(ctx context.Context, params InsertParams) error {
	return wrapSystemLogError(s.q.InsertSystemLog(ctx, sqlc.InsertSystemLogParams{
		Level:   params.Level,
		Logger:  params.Logger,
		Message: params.Message,
		Raw:     params.Raw,
	}), "insert")
}

func mapSystemLog(row sqlc.SystemLog) SystemLog {
	return SystemLog{
		ID:         row.ID,
		Ts:         row.Ts,
		Level:      row.Level,
		Logger:     row.Logger,
		Message:    row.Message,
		Raw:        row.Raw,
		Source:     row.Source,
		Component:  row.Component,
		AgentID:    row.AgentID,
		ThreadID:   row.ThreadID,
		TraceID:    row.TraceID,
		EventType:  row.EventType,
		ToolName:   row.ToolName,
		DurationMs: row.DurationMs,
		Extra:      row.Extra,
	}
}

func wrapSystemLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "system_log")
}
