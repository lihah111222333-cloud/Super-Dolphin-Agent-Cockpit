package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Checkpoint 执行 SQLite WAL checkpoint，并在数据库忙或模式非法时返回错误。
// 只允许调用方传入受控模式，避免把 PRAGMA 参数拼接成未审计的任意 SQL。
func Checkpoint(ctx context.Context, database *sql.DB, mode string) error {
	if database == nil {
		return fmt.Errorf("SQLite checkpoint DB is nil")
	}
	normalized := strings.ToUpper(strings.TrimSpace(mode))
	switch normalized {
	case "PASSIVE", "TRUNCATE":
	default:
		return fmt.Errorf("unsupported SQLite checkpoint mode %q", mode)
	}

	var busy, logFrames, checkpointedFrames int
	if err := database.QueryRowContext(ctx, "PRAGMA wal_checkpoint("+normalized+")").Scan(
		&busy, &logFrames, &checkpointedFrames,
	); err != nil {
		return fmt.Errorf("SQLite WAL checkpoint %s: %w", normalized, err)
	}
	if busy != 0 {
		return fmt.Errorf("SQLite WAL checkpoint %s busy: log_frames=%d checkpointed_frames=%d", normalized, logFrames, checkpointedFrames)
	}
	return nil
}
