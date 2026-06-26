package systemlog

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	ListSystemLogs(ctx context.Context, arg sqlc.ListSystemLogsParams) ([]sqlc.ListSystemLogsRow, error)
	InsertSystemLog(ctx context.Context, arg sqlc.InsertSystemLogParams) error
}

type store struct {
	q querier
}

// NewStore 创建基于 sqlc 的 system log 存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// List 按多维过滤条件列出系统日志。
// Keyword 会转换为大小写无关的 LIKE 条件，其余字段保持精确过滤以便排查单个 agent 或 thread。
func (s *store) List(ctx context.Context, filter ListFilter) ([]SystemLog, error) {
	rows, err := s.q.ListSystemLogs(ctx, sqlc.ListSystemLogsParams{
		LevelFilter:     filter.Level,
		LoggerFilter:    filter.Logger,
		SourceFilter:    filter.Source,
		ComponentFilter: filter.Component,
		AgentIDFilter:   filter.AgentID,
		ThreadIDFilter:  filter.ThreadID,
		EventTypeFilter: filter.EventType,
		ToolNameFilter:  filter.ToolName,
		Keyword:         filter.Keyword,
		KeywordPattern:  platformdb.LikeContainsFold(filter.Keyword),
		LimitCount:      int64(filter.Limit),
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

// Insert 写入一条系统日志。
// 时间戳由 store 统一使用当前 UTC 毫秒，调用方只传日志级别、logger、message 和 raw JSON。
func (s *store) Insert(ctx context.Context, params InsertParams) error {
	return wrapSystemLogError(s.q.InsertSystemLog(ctx, sqlc.InsertSystemLogParams{
		Ts:      platformdb.Millis(time.Now().UTC()),
		Level:   params.Level,
		Logger:  params.Logger,
		Message: params.Message,
		Raw:     params.Raw,
	}), "insert")
}

func mapSystemLog(row sqlc.ListSystemLogsRow) SystemLog {
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
