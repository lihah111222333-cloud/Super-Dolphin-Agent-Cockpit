package buslog

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier is the narrow subset of *sqlc.Queries this store calls.
type querier interface {
	ListBusExceptionLogs(ctx context.Context, arg sqlc.ListBusExceptionLogsParams) ([]sqlc.ListBusExceptionLogsRow, error)
}

type store struct {
	q querier
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func newStoreForTest(q querier) Store { return &store{q: q} }

func (s *store) List(ctx context.Context, filter ListFilter) ([]BusExceptionLog, error) {
	rows, err := s.q.ListBusExceptionLogs(ctx, sqlc.ListBusExceptionLogsParams{
		Column1:  filter.Category,
		Category: filter.Category,
		Column3:  filter.Severity,
		Severity: filter.Severity,
		Column5:  filter.Keyword,
		Limit:    int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapBusLogError(err, "list")
	}
	result := make([]BusExceptionLog, len(rows))
	for i, row := range rows {
		result[i] = mapBusExceptionLog(row)
	}
	return result, nil
}

func mapBusExceptionLog(row sqlc.ListBusExceptionLogsRow) BusExceptionLog {
	return BusExceptionLog{
		Ts:        platformdb.TimeFromMillis(row.Ts),
		Category:  row.Category,
		Severity:  row.Severity,
		Source:    row.Source,
		ToolName:  row.ToolName,
		Message:   row.Message,
		Traceback: row.Traceback,
		Extra:     json.RawMessage(row.Extra),
	}
}

func wrapBusLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "bus_exception_log")
}
