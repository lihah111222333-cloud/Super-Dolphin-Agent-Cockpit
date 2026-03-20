package buslog

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

func (s *store) List(ctx context.Context, filter ListFilter) ([]BusExceptionLog, error) {
	rows, err := s.q.ListBusExceptionLogs(ctx, sqlc.ListBusExceptionLogsParams{
		Category: filter.Category,
		Severity: filter.Severity,
		Keyword:  filter.Keyword,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]BusExceptionLog, len(rows))
	for i, row := range rows {
		result[i] = mapBusExceptionLog(row)
	}
	return result, nil
}

func mapBusExceptionLog(row sqlc.BusExceptionLog) BusExceptionLog {
	return BusExceptionLog{
		ID:        row.ID,
		Ts:        row.Ts,
		Category:  row.Category,
		Severity:  row.Severity,
		Source:    row.Source,
		ToolName:  row.ToolName,
		Message:   row.Message,
		Traceback: row.Traceback,
		Extra:     row.Extra,
	}
}
