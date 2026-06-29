package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var Module = fx.Module(
	"db",
	fx.Provide(NewDB),
	fx.Invoke(registerLifecycle),
)

// NewDB 打开产品运行时使用的 SQLite 数据库。
func NewDB(cfg *config.Config) (*sql.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("SQLite DB config is nil")
	}
	return sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: cfg.SQLitePath})
}

// MinRequiredSchemaVersion 是当前二进制正常运行所需的 schema_migrations.version 下限。
const MinRequiredSchemaVersion = 111

var requiredBaselineTables = []string{
	// agent_codex_binding: 历史遗留表，数据合并至 agent_provider_binding.codex_thread_id，无活跃 sqlc query
	// topology_approval_archives: 无 sql/queries/*.sql 文件，不进入 SQLite runtime
	"agent_provider_binding",
	"agent_status",
	"agent_threads",
	"audit_events",
	"bus_exception_logs",
	"prompts",
	"prompt_templates",
	"prompt_template_versions",
	"prompt_versions",
	"prompt_template_sections",
	"prompt_recall_topics",
	"prompt_routing_tests",
	"prompt_intent_drafts",
	"command_cards",
	"command_card_versions",
	"command_card_runs",
	"shared_files",
	"agent_feedback_events",
	"session_insights",
	"hook_pending_reviews",
	"agent_interactions",
	"topology_approvals",
	"ui_preferences",
	"system_logs",
	"task_traces",
	"cwd_instance_locks",
	"turn_dedupe_registry",
	"cron_jobs",
	"cron_job_runs",
	"task_acks",
	"task_dags",
	"task_dag_runs",
	"task_dag_nodes",
	"task_dag_wakeups",
	"task_dag_worker_leases",
	"workspace_run_files",
	"workspace_runs",
	"runtime_locks",
}

type requiredSQLiteColumn struct {
	table  string
	column string
}

var requiredBaselineColumns = []requiredSQLiteColumn{
	{table: "agent_threads", column: "prompt_snapshot"},
	{table: "shared_files", column: "content_location"},
}

// VerifyMinSchemaVersion 校验 SQLite schema 版本和基线表完整性。
func VerifyMinSchemaVersion(ctx context.Context, q any) error {
	return verifyMinSchemaVersion(ctx, q)
}

func verifyMinSchemaVersion(ctx context.Context, q any) error {
	var maxVersion int
	if err := querySchemaVersion(ctx, q, &maxVersion); err != nil {
		return fmt.Errorf("verify schema_migrations version: %w", err)
	}
	if maxVersion < MinRequiredSchemaVersion {
		return fmt.Errorf(
			"数据库 migration 版本 < %d (当前=%d)，请先 apply 后再启动；database migration version below %d (current=%d), apply pending migrations before starting",
			MinRequiredSchemaVersion, maxVersion, MinRequiredSchemaVersion, maxVersion)
	}
	if err := verifySQLiteBaselineTables(ctx, q); err != nil {
		return err
	}
	if err := verifySQLiteRequiredColumns(ctx, q); err != nil {
		return err
	}
	return nil
}

type sqlContextQueryRow interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlContextQuery interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func querySchemaVersion(ctx context.Context, q any, dest *int) error {
	const query = "SELECT COALESCE(MAX(version), 0) FROM schema_migrations"
	v, ok := q.(sqlContextQueryRow)
	if !ok {
		return fmt.Errorf("unsupported schema version queryer %T", q)
	}
	return v.QueryRowContext(ctx, query).Scan(dest)
}

// verifySQLiteBaselineTables 拒绝只有 migration marker 或缺表的 SQLite 基线库。
func verifySQLiteBaselineTables(ctx context.Context, q any) error {
	v, ok := q.(sqlContextQueryRow)
	if !ok {
		return nil
	}
	missing, err := missingSQLiteBaselineTables(ctx, v)
	if err != nil {
		return fmt.Errorf("verify SQLite baseline schema tables: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("SQLite baseline schema incomplete: missing required table(s): %s; refusing marker-only or partial baseline database", strings.Join(missing, ", "))
	}
	return nil
}

func missingSQLiteBaselineTables(ctx context.Context, q sqlContextQueryRow) ([]string, error) {
	var missing []string
	for _, table := range requiredBaselineTables {
		var exists int
		if err := q.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			missing = append(missing, table)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// verifySQLiteRequiredColumns 用 PRAGMA table_info 校验生产代码依赖的关键列。
// 该检查补上 marker-only / 旧 schema 只建表不建列的启动缺口，缺列时必须阻断启动。
func verifySQLiteRequiredColumns(ctx context.Context, q any) error {
	v, ok := q.(sqlContextQuery)
	if !ok {
		return nil
	}
	missing, err := missingSQLiteRequiredColumns(ctx, v)
	if err != nil {
		return fmt.Errorf("verify SQLite baseline schema columns: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("SQLite baseline schema incomplete: missing required column(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// missingSQLiteRequiredColumns 汇总 schema gate 需要的缺失列列表。
// 同一张表只读取一次 PRAGMA，避免启动检查随 required 列数量线性重复扫表。
func missingSQLiteRequiredColumns(ctx context.Context, q sqlContextQuery) ([]string, error) {
	columnsByTable := make(map[string]map[string]struct{}, len(requiredBaselineColumns))
	missing := make([]string, 0)
	for _, required := range requiredBaselineColumns {
		columns, ok := columnsByTable[required.table]
		if !ok {
			var err error
			columns, err = sqliteTableColumns(ctx, q, required.table)
			if err != nil {
				return nil, err
			}
			columnsByTable[required.table] = columns
		}
		if _, ok := columns[required.column]; !ok {
			missing = append(missing, required.table+"."+required.column)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func sqliteTableColumns(ctx context.Context, q sqlContextQuery, table string) (map[string]struct{}, error) {
	rows, err := q.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

// registerLifecycle 在应用生命周期中执行 SQLite 迁移、schema gate 和关闭逻辑。
func registerLifecycle(lc fx.Lifecycle, logger *pkglogger.Logger, database *sql.DB, cfg *config.Config) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if cfg == nil {
				return fmt.Errorf("SQLite lifecycle config is nil")
			}
			if err := sqliteruntime.RunMigrations(ctx, database, sqliteMigrationsDir(cfg.ProjectRoot)); err != nil {
				return err
			}
			if err := VerifyMinSchemaVersion(ctx, database); err != nil {
				return err
			}
			if err := sqliteruntime.RestrictSidecarFilePermissions(cfg.SQLitePath); err != nil {
				return err
			}
			logger.Info("sqlite database ready")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := database.Close(); err != nil {
				return err
			}
			logger.Info("sqlite database closed")
			return nil
		},
	})
}

func sqliteMigrationsDir(projectRoot string) string {
	return filepath.Join(strings.TrimSpace(projectRoot), "internal", "platform", "db", "sqlite", "migrations")
}

const migrationSplitSentinel = "-- SPLIT --"

// splitMigrationBody 按迁移脚本里的分段标记拆分 SQL。
// 未出现标记时保持原始脚本整体执行，避免改变旧迁移文件的事务语义。
func splitMigrationBody(body string) []string {
	var (
		out   []string
		part  strings.Builder
		found bool
	)
	for _, line := range strings.SplitAfter(body, "\n") {
		if strings.TrimSpace(line) == migrationSplitSentinel {
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
