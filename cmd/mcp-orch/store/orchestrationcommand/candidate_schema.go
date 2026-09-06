package orchestrationcommand

import (
	"context"
	"database/sql"
	"embed"
	"errors"
)

// candidateMigrationFS is deliberately package-local; it is not part of the
// platform SQLite embedded migration set and is never run during startup.
//
//go:embed candidate_migrations/137_orchestration_command_submissions.sql
var candidateMigrationFS embed.FS

// EnsureCandidateSchema 显式安装隔离的 candidate schema，仅供测试或离线实验使用；生产启动不调用。
func EnsureCandidateSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("candidate command submission database is required")
	}
	if ctx == nil {
		return ErrContextRequired
	}
	body, err := candidateMigrationFS.ReadFile("candidate_migrations/137_orchestration_command_submissions.sql")
	if err != nil {
		return err
	}
	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		return errors.New("candidate command submission requires PRAGMA foreign_keys=ON")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	return tx.Commit()
}
