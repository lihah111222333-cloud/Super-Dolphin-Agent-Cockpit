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

// MinRequiredSchemaVersion 是当前二进制能正确工作所要求的最低 schema_migrations.version。
//
// 升级理由 / 历史：
//   - 0084: AI 设计师中文 prompt seed（F7.1）；dispatcher wiring batch 默认依赖；
//   - dispatcher wiring batch（dispatch-wire）：UpdateRunningTaskDagNodeStatus
//     fence 放宽 + dispatcher 路由 NodeExecutor 抽象，要求 0083 spawning_thread_id
//     字段已在位；
//   - 0088: 修复 0012 增量路径遗漏的 agent_threads baseline 兼容列，
//     当前 thread sqlc 查询会无条件读取这些列。
//
// MinRequiredSchemaVersion is the lower bound this binary needs in
// schema_migrations.version to operate correctly. Bumping it here forces a
// hard fail-fast at startup if an operator points the binary at a database
// that has not had `make migrate` (or the autoMigrate path) reach the
// required level. Update the constant together with the corresponding
// migration file when adding a hard dependency.
const MinRequiredSchemaVersion = 88

// schemaVersionQueryRow 是 verifyMinSchemaVersion 的最小依赖面。
// *pgxpool.Pool 天然满足；测试可注入纯内存桩。
//
// schemaVersionQueryRow is the narrow surface verifyMinSchemaVersion needs.
// *pgxpool.Pool implements it; tests inject an in-memory fake.
type schemaVersionQueryRow interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// verifyMinSchemaVersion 是 mcp-orch 启动 hook 末尾的 sanity gate：
// autoMigrate 跑完后再读一次 schema_migrations 拿最高 version，
// 若 < MinRequiredSchemaVersion 就 fail-fast 拒绝继续启动。
//
// 这里**故意**只读不写：autoMigrate 已经保证应用方向；本步骤只挡
// 「migration 没跑全或 operator 手工指了一个落后的库」这类 wiring
// 误配，避免 dispatcher 跑起来后撞到 F6.3 / F1.5 SQL 才报错。
//
// verifyMinSchemaVersion is the post-autoMigrate sanity gate. It surfaces a
// readable bilingual error rather than letting a runtime SQL call blow up
// later when an operator points the binary at a database whose migrations
// have not caught up.
func verifyMinSchemaVersion(ctx context.Context, q schemaVersionQueryRow) error {
	var maxVersion int
	if err := q.QueryRow(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&maxVersion); err != nil {
		return fmt.Errorf("verify schema_migrations version: %w", err)
	}
	if maxVersion < MinRequiredSchemaVersion {
		return fmt.Errorf(
			"数据库 migration 版本 < %d (当前=%d)，请先 apply 后再启动；database migration version below %d (current=%d), apply pending migrations before starting",
			MinRequiredSchemaVersion, maxVersion, MinRequiredSchemaVersion, maxVersion)
	}
	return nil
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
		return fmt.Errorf("read migration %s: %w", f, err)
	}
	if err := execMigrationBody(ctx, pool, string(c)); err != nil {
		return fmt.Errorf("execute migration %s: %w", f, err)
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
			if err := verifyMinSchemaVersion(ctx, pool); err != nil {
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
