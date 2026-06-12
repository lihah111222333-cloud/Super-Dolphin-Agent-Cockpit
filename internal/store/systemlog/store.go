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

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]SystemLog, error) {
	rows, err := s.q.ListSystemLogs(ctx, sqlc.ListSystemLogsParams{
		Column1:   filter.Level,
		Level:     filter.Level,
		Column3:   filter.Logger,
		Logger:    filter.Logger,
		Column5:   filter.Source,
		Source:    filter.Source,
		Column7:   filter.Component,
		Component: filter.Component,
		Column9:   filter.AgentID,
		AgentID:   filter.AgentID,
		Column11:  filter.ThreadID,
		ThreadID:  filter.ThreadID,
		Column13:  filter.EventType,
		EventType: filter.EventType,
		Column15:  filter.ToolName,
		ToolName:  filter.ToolName,
		Column17:  filter.Keyword,
		Limit:     int64(filter.Limit),
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
		Ts:         platformdb.TimeFromMillis(row.Ts),
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
		DurationMs: durationMsPtr(row.DurationMs),
		Extra:      json.RawMessage(row.Extra),
	}
}

func durationMsPtr(v *int64) *int32 {
	if v == nil {
		return nil
	}
	x := int32(*v)
	return &x
}

func wrapSystemLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "system_log")
}
