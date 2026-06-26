// Package sqlite 提供 SQLite 数据库的打开、PRAGMA 配置和迁移执行能力。
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/securefs"
)

// RunMigrations 扫描 dir 目录下的 .sql 文件，按版本号顺序将尚未应用的迁移写入数据库。
// 必须以 001_baseline.sql 作为首次迁移，否则直接返回错误。
func RunMigrations(ctx context.Context, db *sql.DB, dir string) error {
	if db == nil {
		return fmt.Errorf("SQLite migration runner received nil DB")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("SQLite migration directory is empty")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read SQLite migration directory %s: %s", redactPath(dir), securefs.SafeErrorForPath(err, dir))
	}
	applied, err := loadAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	pending := pendingMigrationNames(files, applied)
	if err := requireBaselineFirst(applied, pending); err != nil {
		return err
	}
	for _, name := range pending {
		if err := applyMigration(ctx, db, dir, name); err != nil {
			return err
		}
	}
	return nil
}

// loadAppliedMigrations 读取 schema_migrations 表中已记录的迁移文件名集合。
// 若表不存在则返回空集合，首次全量初始化时适用。
func loadAppliedMigrations(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	hasSchemaMigrations, err := schemaMigrationsTableExists(ctx, db)
	if err != nil {
		return nil, err
	}
	if !hasSchemaMigrations {
		return map[string]bool{}, nil
	}
	return appliedMigrations(ctx, db)
}

func requireBaselineFirst(applied map[string]bool, pending []string) error {
	if applied["001_baseline.sql"] {
		return nil
	}
	if len(pending) > 0 && pending[0] == "001_baseline.sql" {
		return nil
	}
	return fmt.Errorf("SQLite baseline migration 001_baseline.sql must create schema_migrations before incremental migrations")
}

func pendingMigrationNames(files []os.DirEntry, applied map[string]bool) []string {
	var pending []string
	for _, file := range files {
		name := file.Name()
		if file.IsDir() || !strings.HasSuffix(name, ".sql") || applied[name] {
			continue
		}
		pending = append(pending, name)
	}
	sort.Strings(pending)
	return pending
}

func schemaMigrationsTableExists(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'",
	).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect SQLite schema_migrations table: %w", err)
	}
	return count > 0, nil
}

func appliedMigrations(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("list SQLite schema migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var filename string
		if err := rows.Scan(&filename); err != nil {
			return nil, fmt.Errorf("scan SQLite schema migration filename: %w", err)
		}
		applied[filename] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite schema migrations: %w", err)
	}
	return applied, nil
}

// applyMigration 在单个事务内执行迁移文件并记录 schema_migrations。
// 迁移脚本已自行写入 marker 时只提交事务，不重复插入记录；任何执行失败都会回滚。
func applyMigration(ctx context.Context, db *sql.DB, dir, name string) error {
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("read SQLite migration %s from %s: %s", name, redactPath(dir), securefs.SafeErrorForPath(err, filepath.Join(dir, name)))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration %s: %w", name, err)
	}
	if err := execMigrationSegments(ctx, tx, string(body)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("execute SQLite migration %s: %w", name, err)
	}
	recorded, err := migrationMarkerExists(ctx, tx, name)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("detect SQLite migration marker %s: %w", name, err)
	}
	if recorded {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit SQLite migration %s: %w", name, err)
		}
		return nil
	}
	version := parseMigrationVersion(name)
	if version <= 0 {
		_ = tx.Rollback()
		return fmt.Errorf("SQLite migration %s has invalid numeric version", name)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, filename, applied_at) VALUES (?, ?, ?, CAST(unixepoch('subsec') * 1000 AS INTEGER))",
		version, strings.TrimSuffix(name, ".sql"), name); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record SQLite migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration %s: %w", name, err)
	}
	return nil
}

func migrationMarkerExists(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE filename = ?", name).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func execMigrationSegments(ctx context.Context, tx *sql.Tx, body string) error {
	for _, segment := range splitMigrationBody(body) {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, segment); err != nil {
			return err
		}
	}
	return nil
}

// splitMigrationBody 按 SQL 脚本里的分段标记拆分迁移内容。
// 没有标记的旧脚本保持整体执行，空分段会被忽略以避免执行无意义语句。
func splitMigrationBody(body string) []string {
	const sentinel = "-- SPLIT --"
	var (
		out   []string
		part  strings.Builder
		found bool
	)
	for _, line := range strings.SplitAfter(body, "\n") {
		if strings.TrimSpace(line) == sentinel {
			found = true
			if segment := part.String(); strings.TrimSpace(segment) != "" {
				out = append(out, segment)
			}
			part.Reset()
			continue
		}
		part.WriteString(line)
	}
	if !found {
		return []string{body}
	}
	if segment := part.String(); strings.TrimSpace(segment) != "" {
		out = append(out, segment)
	}
	return out
}

func parseMigrationVersion(name string) int {
	prefix, _, _ := strings.Cut(name, "_")
	var version int
	_, _ = fmt.Sscanf(prefix, "%d", &version)
	return version
}
