package dbquery

import "context"

// Store 暴露受限只读 SQL 查询能力。
// Query 的安全检查在实现层完成，调用方不能绕过该接口直接执行任意写入。
type Store interface {
	Placeholder(ctx context.Context) ([]PlaceholderRow, error)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
}

// PlaceholderRow 是 dbquery 占位查询的返回 DTO。
// 它保留 store 模块统一形状，实际运行时查询结果由 Query 返回 map 行。
type PlaceholderRow struct {
	Placeholder *string
}
