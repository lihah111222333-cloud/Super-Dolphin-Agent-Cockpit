package systemlog

import (
	"context"
	"encoding/json"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
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
		LevelFilter:        filter.Level,
		LoggerFilter:       filter.Logger,
		SourceFilter:       filter.Source,
		ComponentFilter:    filter.Component,
		AgentIDFilter:      filter.AgentID,
		ThreadIDFilter:     filter.ThreadID,
		TraceIDFilter:      filter.TraceID,
		SpanIDFilter:       filter.SpanID,
		ParentSpanIDFilter: filter.ParentSpanID,
		EventTypeFilter:    filter.EventType,
		ToolNameFilter:     filter.ToolName,
		Keyword:            filter.Keyword,
		KeywordPattern:     platformdb.LikeContainsFold(filter.Keyword),
		LimitCount:         int64(filter.Limit),
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
// 时间戳由 store 统一使用当前 UTC 毫秒，trace 和维度字段必须显式透传到 system_logs。
func (s *store) Insert(ctx context.Context, params InsertParams) error {
	extra, err := normalizeSystemLogExtra(params.Extra)
	if err != nil {
		return wrapSystemLogError(err, "insert")
	}
	return wrapSystemLogError(s.q.InsertSystemLog(ctx, sqlc.InsertSystemLogParams{
		Ts:           platformdb.Millis(time.Now().UTC()),
		Level:        params.Level,
		Logger:       params.Logger,
		Message:      params.Message,
		Raw:          params.Raw,
		Source:       params.Source,
		Component:    params.Component,
		AgentID:      params.AgentID,
		ThreadID:     params.ThreadID,
		TraceID:      params.TraceID,
		SpanID:       params.SpanID,
		ParentSpanID: params.ParentSpanID,
		EventType:    params.EventType,
		ToolName:     params.ToolName,
		DurationMs:   int32PtrToInt64Ptr(params.DurationMs),
		Extra:        extra,
	}), "insert")
}

// normalizeSystemLogExtra 校验 system_logs.extra；空值写入空 object，非法 JSON 直接阻断。
func normalizeSystemLogExtra(extra json.RawMessage) (json.RawMessage, error) {
	if len(extra) == 0 {
		extra = json.RawMessage(`{}`)
	}
	if err := platformdb.ValidateJSONRaw(extra); err != nil {
		return nil, err
	}
	return extra, nil
}

func mapSystemLog(row sqlc.ListSystemLogsRow) SystemLog {
	return SystemLog{
		ID:           row.ID,
		Ts:           platformdb.TimeFromMillis(row.Ts),
		Level:        row.Level,
		Logger:       row.Logger,
		Message:      row.Message,
		Raw:          row.Raw,
		Source:       row.Source,
		Component:    row.Component,
		AgentID:      row.AgentID,
		ThreadID:     row.ThreadID,
		TraceID:      row.TraceID,
		SpanID:       row.SpanID,
		ParentSpanID: row.ParentSpanID,
		EventType:    row.EventType,
		ToolName:     row.ToolName,
		DurationMs:   durationMsPtr(row.DurationMs),
		Extra:        json.RawMessage(row.Extra),
	}
}

func durationMsPtr(v *int64) *int32 {
	if v == nil {
		return nil
	}
	x := int32(*v)
	return &x
}

func int32PtrToInt64Ptr(v *int32) *int64 {
	if v == nil {
		return nil
	}
	x := int64(*v)
	return &x
}

func wrapSystemLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "system_log")
}
