package buslog

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// querier 是 buslog store 依赖的 sqlc 查询子集，测试可用窄接口替身覆盖。
type querier interface {
	ListBusExceptionLogs(ctx context.Context, arg sqlc.ListBusExceptionLogsParams) ([]sqlc.ListBusExceptionLogsRow, error)
}

// store 实现业务异常日志只读查询，并把数据库行映射为 UI DTO。
type store struct {
	q querier
}

// NewStore 使用生产 sqlc 查询对象创建 buslog Store。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

// newStoreForTest 使用窄 querier 构造测试 Store，避免测试依赖真实数据库池。
func newStoreForTest(q querier) Store { return &store{q: q} }

// List 按过滤条件读取业务异常日志，关键字匹配由 store 转成 SQL LIKE 模式。
func (s *store) List(ctx context.Context, filter ListFilter) ([]BusExceptionLog, error) {
	rows, err := s.q.ListBusExceptionLogs(ctx, sqlc.ListBusExceptionLogsParams{
		CategoryFilter: filter.Category,
		SeverityFilter: filter.Severity,
		Keyword:        filter.Keyword,
		KeywordPattern: platformdb.LikeContainsFold(filter.Keyword),
		LimitCount:     int64(filter.Limit),
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

// mapBusExceptionLog 将 sqlc 查询行转换为前端 JSON wire DTO。
func mapBusExceptionLog(row sqlc.ListBusExceptionLogsRow) BusExceptionLog {
	return BusExceptionLog{
		ID:        row.ID,
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

// wrapBusLogError 统一包装业务异常日志 store 错误，保留 operation 便于排查。
func wrapBusLogError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "bus_exception_log")
}
