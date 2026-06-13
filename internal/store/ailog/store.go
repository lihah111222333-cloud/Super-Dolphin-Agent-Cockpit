package ailog

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	CountAILogsByStatus(ctx context.Context) ([]sqlc.CountAILogsByStatusRow, error)
	ListAILogSystemLogs(ctx context.Context, arg sqlc.ListAILogSystemLogsParams) ([]sqlc.SystemLog, error)
	ListAILogsByCategory(ctx context.Context, arg sqlc.ListAILogsByCategoryParams) ([]sqlc.ListAILogsByCategoryRow, error)
	ListRecentAILogs(ctx context.Context, arg sqlc.ListRecentAILogsParams) ([]sqlc.ListRecentAILogsRow, error)
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// List 列出 AI 日志记录。
func (s *store) List(ctx context.Context, filter ListFilter) ([]AILog, error) {
	rows, err := s.q.ListAILogSystemLogs(ctx, sqlc.ListAILogSystemLogsParams{
		Keyword:    filter.Keyword,
		LimitCount: filter.Limit,
	})
	if err != nil {
		return nil, wrapAILogError(err, "list")
	}
	result := make([]AILog, len(rows))
	for i, row := range rows {
		result[i] = mapAILog(row)
	}
	return result, nil
}

// ListByCategory 按分类列出 AI 日志记录。
func (s *store) ListByCategory(ctx context.Context, category string, keyword string, limit int32) ([]AILog, error) {
	rows, err := s.q.ListAILogsByCategory(ctx, sqlc.ListAILogsByCategoryParams{
		Category:   category,
		Keyword:    keyword,
		LimitCount: limit,
	})
	if err != nil {
		return nil, wrapAILogError(err, "list_by_category")
	}
	result := make([]AILog, len(rows))
	for i, row := range rows {
		result[i] = mapCategoryAILog(row)
	}
	return result, nil
}

// CountByStatus 按状态统计 AI 日志。
func (s *store) CountByStatus(ctx context.Context) ([]StatusCount, error) {
	rows, err := s.q.CountAILogsByStatus(ctx)
	if err != nil {
		return nil, wrapAILogError(err, "count_by_status")
	}
	result := make([]StatusCount, len(rows))
	for i, row := range rows {
		result[i] = StatusCount{Status: row.Status, Count: row.Count}
	}
	return result, nil
}

// ListRecent 列出recent。
func (s *store) ListRecent(ctx context.Context, limit int32) ([]AILog, error) {
	rows, err := s.q.ListRecentAILogs(ctx, sqlc.ListRecentAILogsParams{Limit: limit})
	if err != nil {
		return nil, wrapAILogError(err, "list_recent")
	}
	result := make([]AILog, len(rows))
	for i, row := range rows {
		result[i] = mapRecentAILog(row)
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
		Extra:      json.RawMessage(row.Extra),
	}
}

func mapCategoryAILog(row sqlc.ListAILogsByCategoryRow) AILog {
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
		Extra:      json.RawMessage(row.Extra),
		Category:   row.Category,
		Method:     row.Method,
		URL:        row.Url,
		Endpoint:   row.Endpoint,
		Status:     row.Status,
		StatusText: row.StatusText,
		Model:      row.Model,
	}
}

func mapRecentAILog(row sqlc.ListRecentAILogsRow) AILog {
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
		Extra:      json.RawMessage(row.Extra),
		Category:   row.Category,
		Method:     row.Method,
		URL:        row.Url,
		Endpoint:   row.Endpoint,
		Status:     row.Status,
		StatusText: row.StatusText,
		Model:      row.Model,
	}
}

func wrapAILogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "ai_log")
}
