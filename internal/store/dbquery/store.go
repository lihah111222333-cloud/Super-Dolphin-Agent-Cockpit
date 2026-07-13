package dbquery

import (
	"context"
	"errors"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

const defaultQueryTimeout = 10 * time.Second

type store struct {
	q       sqlc.Querier
	db      platformdb.Queryable
	timeout time.Duration
}

// NewStore 创建同时支持通用 Query 和旧 Placeholder 接口的 dbquery 存储。
// timeout 非正时使用受控默认值，避免只读 SQL 长时间占用独占连接。
func NewStore(q sqlc.Querier, db platformdb.Queryable, timeout time.Duration) Store {
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	return &store{q: q, db: db, timeout: timeout}
}

// NewQueryStore 创建只暴露通用 Query 的 dbquery 存储。
// 该入口用于不需要 sqlc PlaceholderDBQuery 的调用方，db 必须能提供独占 SQLite 连接。
func NewQueryStore(db platformdb.Queryable, timeout time.Duration) Store {
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	return &store{db: db, timeout: timeout}
}

// Query 执行受白名单约束的只读 SQL。
// 所有 SQL 在执行前都会经过文本、占位符和表引用校验，并通过独占连接开启 SQLite query_only。
func (s *store) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if s == nil || s.db == nil {
		return nil, wrapDBQueryError(errors.New("dbquery store is not initialized"), "query")
	}

	rows, err := executeQuery(ctx, s.db, s.timeout, query, args...)
	if err != nil {
		return nil, wrapDBQueryError(err, "query")
	}
	return rows, nil
}

// Placeholder 保留 PlaceholderDBQuery 的兼容读取入口。
// 新调用方应使用 Query；这里仍按 store 错误包装返回，避免旧接口直接泄露 sqlc 错误。
func (s *store) Placeholder(ctx context.Context) ([]PlaceholderRow, error) {
	if s == nil || s.q == nil {
		return nil, wrapDBQueryError(errors.New("dbquery store is not initialized"), "placeholder")
	}
	rows, err := s.q.PlaceholderDBQuery(ctx)
	if err != nil {
		return nil, wrapDBQueryError(err, "placeholder")
	}
	out := make([]PlaceholderRow, 0, len(rows))
	for _, row := range rows {
		placeholder := row
		out = append(out, PlaceholderRow{Placeholder: &placeholder})
	}
	return out, nil
}

func wrapDBQueryError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "db_query")
}
