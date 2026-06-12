package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var Module = fx.Module(
	"db",
	fx.Provide(NewDB),
	fx.Provide(NewPool),
	fx.Invoke(registerLifecycle),
)

func NewDB(cfg *config.Config) (*sql.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("SQLite DB config is nil")
	}
	return sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: cfg.SQLitePath})
}

// NewPool is a temporary compatibility symbol for store and mcp-orch callers
// that Task 01 is not allowed to migrate. PostgreSQL runtime support has been
// removed from the product path; this function must not parse PG config,
// connect to PG, or derive configuration from environment.
func NewPool(_ *config.Config) (*pgxpool.Pool, error) {
	return nil, errors.New("PostgreSQL pool removed; migrate caller to SQLite DB boundary")
}

// MinRequiredSchemaVersion is the lower bound this binary needs in
// schema_migrations.version to operate correctly.
const MinRequiredSchemaVersion = 103

var requiredBaselineTables = []string{
	"agent_codex_binding",
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
	"topology_approval_archives",
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
	return nil
}

type sqlContextQueryRow interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type pgxContextQueryRow interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func querySchemaVersion(ctx context.Context, q any, dest *int) error {
	const query = "SELECT COALESCE(MAX(version), 0) FROM schema_migrations"
	switch v := q.(type) {
	case sqlContextQueryRow:
		return v.QueryRowContext(ctx, query).Scan(dest)
	case pgxContextQueryRow:
		return v.QueryRow(ctx, query).Scan(dest)
	default:
		return fmt.Errorf("unsupported schema version queryer %T", q)
	}
}

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
