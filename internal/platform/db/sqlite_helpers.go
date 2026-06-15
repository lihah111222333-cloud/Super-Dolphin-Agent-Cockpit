package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const boundedWriteRetryDelay = 50 * time.Millisecond

// Millis は time.Time を UTC epoch milliseconds に変換する。
// store 層の全時間カラム書き込みはこれを使う。
func Millis(t time.Time) int64 {
	return t.UnixMilli()
}

// TimeFromMillis は UTC epoch milliseconds を time.Time (UTC) に変換する。
func TimeFromMillis(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

// ValidateJSONRaw は json.RawMessage が valid JSON であることを確認する。
// nil / 空のケースは '{}' or '[]' などの空コンテナとして渡すこと。
func ValidateJSONRaw(raw json.RawMessage) error {
	if !json.Valid(raw) {
		return errors.New("invalid JSON")
	}
	return nil
}

// LikeContainsFold は SQLite の lower(col) LIKE lower(?) 用にパターンを組み立てる。
// 呼び出し元は "lower(column) LIKE lower(?)" を SQL に使い、この値をバインドする。
func LikeContainsFold(s string) string {
	return "%" + s + "%"
}

// IsSQLiteBusyLocked は SQLite の "database is locked" / "database is busy" を検出する。
func IsSQLiteBusyLocked(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database is busy") ||
		strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "SQLITE_LOCKED")
}

// BoundedWriteRetry は SQLite busy/locked 時のみ有界リトライする。
// その他エラーは即時返却。maxAttempts は 1 以上。
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
