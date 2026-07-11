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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
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
	if err := executeMigrationBody(ctx, tx, name, string(body)); err != nil {
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

// executeMigrationBody 执行普通 SQL 迁移；少数需要按现有 schema 分支的迁移在这里收敛为 Go 逻辑。
func executeMigrationBody(ctx context.Context, tx *sql.Tx, name, body string) error {
	if name == "112_system_logs_trace_span.sql" {
		return migrateSystemLogsTraceSpan(ctx, tx)
	}
	if name == "113_bus_exception_log_flags.sql" {
		return migrateBusExceptionLogFlags(ctx, tx)
	}
	return execMigrationSegments(ctx, tx, body)
}

// migrateSystemLogsTraceSpan 只支持 agent-v3 的 system_logs 形状，补齐 span 字段和查询索引。
// SQLite 不支持 ADD COLUMN IF NOT EXISTS；这里先探测列，遇到更老表形状直接 fail-fast。
func migrateSystemLogsTraceSpan(ctx context.Context, tx *sql.Tx) error {
	columns, err := sqliteTableColumns(ctx, tx, "system_logs")
	if err != nil {
		return err
	}
	if err := requireSystemLogColumns(columns, systemLogsTraceSpanRequiredColumns...); err != nil {
		return err
	}
	if columns["span_id"] != columns["parent_span_id"] {
		return fmt.Errorf("system_logs span columns are partially migrated")
	}
	if columns["span_id"] {
		return execMigrationStatements(ctx, tx, systemLogsIndexSQL)
	}
	statements := []string{
		createSystemLogsTraceSpanMigrationSQL,
		systemLogsTraceSpanInsertSQL,
		"DROP TABLE system_logs",
		"ALTER TABLE system_logs_trace_span_migration RENAME TO system_logs",
	}
	statements = append(statements, systemLogsIndexSQL...)
	return execMigrationStatements(ctx, tx, statements)
}

const createSystemLogsTraceSpanMigrationSQL = `
CREATE TABLE system_logs_trace_span_migration (
	id INTEGER PRIMARY KEY,
	ts INTEGER NOT NULL,
	level TEXT NOT NULL,
	logger TEXT NOT NULL,
	message TEXT NOT NULL,
	raw TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	component TEXT NOT NULL DEFAULT '',
	agent_id TEXT NOT NULL DEFAULT '',
	thread_id TEXT NOT NULL DEFAULT '',
	trace_id TEXT NOT NULL DEFAULT '',
	span_id TEXT NOT NULL DEFAULT '',
	parent_span_id TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL DEFAULT '',
	tool_name TEXT NOT NULL DEFAULT '',
	duration_ms INTEGER,
	extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra))
)`

var systemLogsTraceSpanRequiredColumns = []string{
	"id",
	"ts",
	"level",
	"logger",
	"message",
	"raw",
	"source",
	"component",
	"agent_id",
	"thread_id",
	"trace_id",
	"event_type",
	"tool_name",
	"duration_ms",
	"extra",
}

var systemLogsIndexSQL = []string{
	"CREATE INDEX IF NOT EXISTS idx_system_logs_ts_id ON system_logs(ts DESC, id DESC)",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_level_ts_id ON system_logs(level, ts DESC, id DESC)",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_source_ts_id ON system_logs(source, ts DESC, id DESC) WHERE source <> ''",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_agent_ts_id ON system_logs(agent_id, ts DESC, id DESC) WHERE agent_id <> ''",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_thread_ts_id ON system_logs(thread_id, ts DESC, id DESC) WHERE thread_id <> ''",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_trace_ts_id ON system_logs(trace_id, ts DESC, id DESC) WHERE trace_id <> ''",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_span_ts_id ON system_logs(span_id, ts DESC, id DESC) WHERE span_id <> ''",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_logger ON system_logs(logger)",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_event ON system_logs(event_type) WHERE event_type <> ''",
	"CREATE INDEX IF NOT EXISTS idx_system_logs_tool ON system_logs(tool_name) WHERE tool_name <> ''",
}

const systemLogsTraceSpanInsertSQL = `
INSERT INTO system_logs_trace_span_migration (
	id, ts, level, logger, message, raw,
	source, component, agent_id, thread_id, trace_id,
	span_id, parent_span_id,
	event_type, tool_name, duration_ms, extra
)
SELECT
	id, ts, level, logger, message, raw,
	source, component, agent_id, thread_id, trace_id,
	'', '',
	event_type, tool_name, duration_ms, COALESCE(extra, '{}')
FROM system_logs`

func execMigrationStatements(ctx context.Context, tx *sql.Tx, statements []string) error {
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// sqliteTableColumns 读取 SQLite 表列集合；迁移代码只用固定表名调用，避免把外部输入拼进 SQL。
func sqliteTableColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var (
			cid      int
			name     string
			colType  string
			notNull  int
			defaultV sql.NullString
			primaryK int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultV, &primaryK); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func requireSystemLogColumns(columns map[string]bool, required ...string) error {
	for _, column := range required {
		if !columns[column] {
			return fmt.Errorf("system_logs missing required column %s", column)
		}
	}
	return nil
}

// migrateBusExceptionLogFlags 补齐 bus 日志列表所需的轻量标志列。
// SQLite 不支持 ADD COLUMN IF NOT EXISTS，fresh baseline 已含列时只确保触发器和回填存在。
func migrateBusExceptionLogFlags(ctx context.Context, tx *sql.Tx) error {
	columns, err := sqliteTableColumns(ctx, tx, "bus_exception_logs")
	if err != nil {
		return err
	}
	if err := requireTableColumns("bus_exception_logs", columns, "id", "traceback", "extra"); err != nil {
		return err
	}
	hasTraceback := columns["has_traceback"]
	hasExtra := columns["has_extra"]
	if hasTraceback != hasExtra {
		return fmt.Errorf("bus_exception_logs flag columns are partially migrated")
	}
	statements := make([]string, 0, 5)
	if !hasTraceback {
		statements = append(statements,
			"ALTER TABLE bus_exception_logs ADD COLUMN has_traceback INTEGER NOT NULL DEFAULT 0 CHECK(has_traceback IN (0, 1))",
			"ALTER TABLE bus_exception_logs ADD COLUMN has_extra INTEGER NOT NULL DEFAULT 0 CHECK(has_extra IN (0, 1))",
		)
	}
	statements = append(statements, busExceptionLogFlagBackfillSQL)
	statements = append(statements, busExceptionLogFlagTriggerSQL...)
	return execMigrationStatements(ctx, tx, statements)
}

const busExceptionLogFlagBackfillSQL = `
UPDATE bus_exception_logs
SET has_traceback = CASE WHEN traceback <> '' THEN 1 ELSE 0 END,
    has_extra = CASE WHEN extra <> '{}' THEN 1 ELSE 0 END`

var busExceptionLogFlagTriggerSQL = []string{
	`CREATE TRIGGER IF NOT EXISTS trg_bus_exception_logs_flags_insert
AFTER INSERT ON bus_exception_logs
BEGIN
    UPDATE bus_exception_logs
    SET has_traceback = CASE WHEN NEW.traceback <> '' THEN 1 ELSE 0 END,
        has_extra = CASE WHEN NEW.extra <> '{}' THEN 1 ELSE 0 END
    WHERE id = NEW.id;
END`,
	`CREATE TRIGGER IF NOT EXISTS trg_bus_exception_logs_flags_update
AFTER UPDATE OF traceback, extra ON bus_exception_logs
BEGIN
    UPDATE bus_exception_logs
    SET has_traceback = CASE WHEN NEW.traceback <> '' THEN 1 ELSE 0 END,
        has_extra = CASE WHEN NEW.extra <> '{}' THEN 1 ELSE 0 END
    WHERE id = NEW.id;
END`,
}

func requireTableColumns(table string, columns map[string]bool, required ...string) error {
	for _, column := range required {
		if !columns[column] {
			return fmt.Errorf("%s missing required column %s", table, column)
		}
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
