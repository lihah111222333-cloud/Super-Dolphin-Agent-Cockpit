package systemlog

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	ListSystemLogs(ctx context.Context, arg sqlc.ListSystemLogsParams) ([]sqlc.SystemLog, error)
	InsertSystemLog(ctx context.Context, arg sqlc.InsertSystemLogParams) error
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// List 列出systemlog存储。
func (s *store) List(ctx context.Context, filter ListFilter) ([]SystemLog, error) {
	rows, err := s.q.ListSystemLogs(ctx, sqlc.ListSystemLogsParams{
		Column1: filter.Level,
		Column2: filter.Logger,
		Column3: filter.Source,
		Column4: filter.Component,
		Column5: filter.AgentID,
		Column6: filter.ThreadID,
		Column7: filter.EventType,
		Column8: filter.ToolName,
		Column9: filter.Keyword,
		Limit:   filter.Limit,
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

// Insert 插入systemlog存储。
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
		Extra:      json.RawMessage(row.Extra),
	}
}

func wrapSystemLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "system_log")
}
