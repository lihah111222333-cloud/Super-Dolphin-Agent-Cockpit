package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
)

const boundedWriteRetryDelay = 50 * time.Millisecond

// Millis 将 time.Time 转为 UTC epoch milliseconds。
// store 层时间字段统一走毫秒值，避免 SQLite 文本时间格式在跨模块传输时漂移。
func Millis(t time.Time) int64 {
	return t.UnixMilli()
}

// TimeFromMillis 将 UTC epoch milliseconds 还原为 UTC time.Time。
func TimeFromMillis(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

// ValidateJSONRaw 校验 json.RawMessage 必须是有效 JSON。
// nil 或空值不是合法 JSON；调用方需要显式传入 `{}` 或 `[]` 这类空容器。
func ValidateJSONRaw(raw json.RawMessage) error {
	if !json.Valid(raw) {
		return errors.New("invalid JSON")
	}
	return nil
}

// LikeContainsFold 构造 SQLite lower(column) LIKE lower(?) 使用的包含匹配模式。
// 调用方负责在 SQL 中使用 lower(column) LIKE lower(?)，这里不拼接 SQL 片段。
func LikeContainsFold(s string) string {
	return "%" + s + "%"
}

// IsSQLiteBusyLocked 判断错误是否属于 SQLite busy/locked 写入竞争。
func IsSQLiteBusyLocked(err error) bool {
	code, ok := sqliteResultCode(err)
	if !ok {
		return false
	}
	primaryCode := sqlitePrimaryResultCode(code)
	return primaryCode == sqlite3.SQLITE_BUSY || primaryCode == sqlite3.SQLITE_LOCKED
}

// sqlitePrimaryResultCode 提取 SQLite 扩展结果码的基础结果码。
// SQLITE_BUSY_* 与 SQLITE_LOCKED_* 共享基础码，重试决策必须同时覆盖其扩展形式。
func sqlitePrimaryResultCode(code int) int {
	return code & 0xff
}

// BoundedWriteRetry 只在 SQLite busy/locked 时做有界重试。
// 其他错误立即返回；maxAttempts 小于 1 时按 1 次执行，避免静默无限重试。
func BoundedWriteRetry(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for i := range maxAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsSQLiteBusyLocked(lastErr) {
			return lastErr
		}
		if i == maxAttempts-1 {
			break
		}
		if err := waitBoundedWriteRetry(ctx); err != nil {
			return err
		}
	}
	return lastErr
}

func waitBoundedWriteRetry(ctx context.Context) error {
	timer := time.NewTimer(boundedWriteRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
