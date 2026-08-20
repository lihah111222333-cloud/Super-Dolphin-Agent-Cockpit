package heterociowner

import (
	"context"
	"database/sql"
	"fmt"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// WithImmediateTx 在单一 SQLite 连接上执行不可降级的 BEGIN IMMEDIATE 事务。
// 回调返回错误或 panic 时由底层事务 helper 回滚，绝不退回 deferred 事务。
func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	if db == nil {
		return fmt.Errorf("heteroci owner database is nil")
	}
	if fn == nil {
		return fmt.Errorf("heteroci owner immediate transaction callback is nil")
	}
	return platformdb.WithImmediateTx(ctx, db, fn)
}
