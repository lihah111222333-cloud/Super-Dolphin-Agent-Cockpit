package ailog

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]AILog, error) {
	rows, err := s.q.ListAILogSystemLogs(ctx, sqlc.ListAILogSystemLogsParams{
		Keyword: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]AILog, len(rows))
	for i, row := range rows {
		result[i] = mapAILog(row)
	}
	return result, nil
}

func mapAILog(row sqlc.SystemLog) AILog {
	return AILog{
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
