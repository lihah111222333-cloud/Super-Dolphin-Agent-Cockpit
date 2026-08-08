// Package gateprivate contains infrastructure used only by super-dolphin-gate.
package gateprivate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const sqliteWriteRetryDelay = 50 * time.Millisecond

// OpenSQLite 按单写者合同打开门禁协调器数据库。
func OpenSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// RestrictOwnerFile 将门禁状态文件限制为仅当前所有者可读写。
func RestrictOwnerFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict owner file: %w", err)
	}
	return nil
}

// RetrySQLiteWrite 只在上下文时限内重试 SQLite busy 或 locked 冲突。
func RetrySQLiteWrite(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := range maxAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil || !isSQLiteBusyLocked(lastErr) {
			return lastErr
		}
		if attempt == maxAttempts-1 {
			break
		}
		timer := time.NewTimer(sqliteWriteRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func isSQLiteBusyLocked(err error) bool {
	type sqliteCoder interface{ Code() int }
	var coded sqliteCoder
	if !errors.As(err, &coded) {
		return false
	}
	code := coded.Code() & 0xff
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}
