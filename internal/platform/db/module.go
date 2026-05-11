package db

import (
	"context"
	"fmt"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

var Module = fx.Module(
	"db",
	fx.Provide(NewPool),
	fx.Invoke(registerLifecycle),
)

func NewPool(cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}

	poolCfg.MaxConns = 100
	// pgxpool.NewWithConfig opens lazily; actual connections are
	// established on first use. Blocking work (ensureDatabaseExists,
	// autoMigrate) is deferred to registerLifecycle.OnStart.
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func ensureDatabaseExists(targetDB, databaseURL string) error {
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return err
	}
	connConfig.Database = "postgres" // connect to default db

	conn, err := pgx.ConnectConfig(context.Background(), connConfig)
	if err != nil {
		return err // if postgres db doesn't exist or other error, return
	}
	defer conn.Close(context.Background())

	var exists bool
	err = conn.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", targetDB).Scan(&exists)
	if err != nil {
		return err
	}

	if !exists {
		// CREATE DATABASE cannot run inside a transaction block in postgres
		if _, err := conn.Exec(context.Background(), `CREATE DATABASE "`+targetDB+`"`); err != nil {
			return err
		}
	}
	return nil
}

func autoMigrate(ctx context.Context, pool *pgxpool.Pool, projectRoot string) error {
	migrationsDir := filepath.Join(projectRoot, "migrations")
	if err := ensureSchemaMigrationsTable(ctx, pool); err != nil {
		return err
	}
	if err := applyBaselineIfMissing(ctx, pool, migrationsDir); err != nil {
		return err
	}
	return applyPendingMigrations(ctx, pool, migrationsDir)
}

func ensureSchemaMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.schema_migrations (
			version integer NOT NULL,
			name text NOT NULL,
			filename text NOT NULL,
			applied_at timestamp with time zone DEFAULT now() NOT NULL
		);
	`)
	return err
}

func applyBaselineIfMissing(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	var hasBaseline bool
	err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = '001_baseline.sql')").Scan(&hasBaseline)
	if err != nil {
		return err
	}
	if hasBaseline {
		return nil
	}

	var threadsExist bool
	_ = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'agent_threads')").Scan(&threadsExist)

	if !threadsExist {
		c, err := os.ReadFile(filepath.Join(dir, "001_baseline.sql"))
		if err == nil {
			if _, err := pool.Exec(ctx, string(c)); err != nil {
				return err
			}
		}
	}
	_, err = pool.Exec(ctx, "INSERT INTO schema_migrations (version, name, filename) VALUES (1, 'baseline', '001_baseline.sql')")
	return err
}

func getAppliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	applied := make(map[string]bool)
	rows, err := pool.Query(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return applied, err
	}
	defer rows.Close()

	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err == nil {
			applied[f] = true
		}
	}
	return applied, nil
}

func applyPendingMigrations(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	applied, err := getAppliedMigrations(ctx, pool)
	if err != nil {
		return err
	}

	var toApply []string
	for _, f := range files {
		if !shouldApplyMigration(f, applied) {
			continue
		}
		toApply = append(toApply, f.Name())
	}
	sort.Strings(toApply)

	for _, f := range toApply {
		if err := executeMigration(ctx, pool, dir, f); err != nil {
			return err
		}
	}
	return nil
}

func shouldApplyMigration(f os.DirEntry, applied map[string]bool) bool {
	n := f.Name()
	if f.IsDir() || !strings.HasSuffix(n, ".sql") || n == "001_baseline.sql" {
		return false
	}
	if strings.HasPrefix(n, "000") || strings.HasPrefix(n, "001") || applied[n] {
		return false
	}
	return true
}

// migrationSplitSentinel 是 migration 文件内的分段标记。出现该行时
// executeMigration 把它两侧的语句拆成独立的 pool.Exec 调用。
//
// 主要用例：CREATE INDEX CONCURRENTLY 不能跑在任何 transaction 块内（PG 硬
// 规则），但迁移文件主体心里不可避免要一些 ALTER TABLE 裹在 BEGIN/COMMIT
// 中保证原子。之前 runner 单次 pool.Exec(文件整体) 无法共存两者；现在只要
// 在两段间加一行“-- SPLIT --”，后面的语句就在事务外跑。
const migrationSplitSentinel = "-- SPLIT --"

func executeMigration(ctx context.Context, pool *pgxpool.Pool, dir, f string) error {
	c, err := os.ReadFile(filepath.Join(dir, f))
	if err != nil {
		return err
	}
	if err := execMigrationBody(ctx, pool, string(c)); err != nil {
		return err
	}
	var version int
	_, _ = fmt.Sscanf(f, "%d_", &version)
	_, err = pool.Exec(ctx, "INSERT INTO schema_migrations (version, name, filename) VALUES ($1, $2, $3)", version, f, f)
	return err
}

// execMigrationBody 拆并顺序跑 migration 体。无 sentinel 时退化为原单次
// pool.Exec（完全后向兼容）。每个非空段被 trim 后单独 exec；空段跳过。
func execMigrationBody(ctx context.Context, pool *pgxpool.Pool, body string) error {
	for _, segment := range splitMigrationBody(body) {
		if _, err := pool.Exec(ctx, segment); err != nil {
			return err
		}
	}
	return nil
}

// splitMigrationBody 是拆逻辑的纯函数形式，便于单测。返回列表每项是一个
// 非空段（原本 trim 前的 segment，保留原 start/end 空白以不干扰 PG 解析）。
// 无 sentinel 时返 [body] 一项（这使 exec 路径与原语义一致）。
func splitMigrationBody(body string) []string {
	if !strings.Contains(body, migrationSplitSentinel) {
		return []string{body}
	}
	parts := strings.Split(body, migrationSplitSentinel)
	out := make([]string, 0, len(parts))
	for _, segment := range parts {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		out = append(out, segment)
	}
	return out
}

func registerLifecycle(lc fx.Lifecycle, logger *pkglogger.Logger, pool *pgxpool.Pool, cfg *config.Config) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			poolCfg := pool.Config()
			if err := ensureDatabaseExists(poolCfg.ConnConfig.Database, cfg.DatabaseURL); err != nil {
				return err
			}
			if err := autoMigrate(ctx, pool, cfg.ProjectRoot); err != nil {
				return err
			}
			logger.Info("db pool ready")
			return pool.Ping(ctx)
		},
		OnStop: func(context.Context) error {
			pool.Close()
			logger.Info("db pool closed")
			return nil
		},
	})
}
