package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/embeddedpg"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var Module = fx.Module(
	"db",
	fx.Provide(NewPool),
	fx.Invoke(registerLifecycle),
)

// NewPool 创建pool。
func NewPool(cfg *config.Config) (*pgxpool.Pool, error) {
	databaseURL, err := requireDatabaseURL(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
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

func requireDatabaseURL(databaseURL string) (string, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return "", errors.New("DATABASE_URL is empty; set DATABASE_URL or use embedded postgres owner so a DSN is generated")
	}
	return databaseURL, nil
}

func createDatabaseSQL(targetDB string) (string, error) {
	if strings.TrimSpace(targetDB) == "" {
		return "", errors.New("database name is empty in DATABASE_URL")
	}
	return "CREATE DATABASE " + pgx.Identifier{targetDB}.Sanitize(), nil
}

// ensureDatabaseExists 确保databaseexists。
func ensureDatabaseExists(targetDB, databaseURL string) error {
	createSQL, err := createDatabaseSQL(targetDB)
	if err != nil {
		return err
	}
	databaseURL, err = requireDatabaseURL(databaseURL)
	if err != nil {
		return err
	}
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
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
		if _, err := conn.Exec(context.Background(), createSQL); err != nil {
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
//   - 0094: prompt_templates.when_to_use，prompt 编辑、路由回显与 expert
//     渲染链路会无条件读写该列。
//   - 0096: prompt_template_sections.trigger_type / recall_topic，prompt
//     section 编辑与回显链路会无条件读写这两列。
//   - 0101: prompt_intent_drafts + scoped recall index，意图式创建 RPC 会
//     无条件读写草稿表，recall 需要 project scope。
//   - 0103: prompt_intent_drafts.scope，待确认草稿需要保留 project/global
//     作用范围，避免全局草稿恢复后静默保存为项目草稿。
//
// MinRequiredSchemaVersion is the lower bound this binary needs in
// schema_migrations.version to operate correctly. Bumping it here forces a
// hard fail-fast at startup if an operator points the binary at a database
// that has not had `make migrate` (or the autoMigrate path) reach the
// required level. Update the constant together with the corresponding
// migration file when adding a hard dependency.
const MinRequiredSchemaVersion = 103

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
func VerifyMinSchemaVersion(ctx context.Context, q schemaVersionQueryRow) error {
	return verifyMinSchemaVersion(ctx, q)
}

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

type baselineTx interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type baselineBeginFunc func(context.Context) (baselineTx, error)
type baselineReadFileFunc func(string) ([]byte, error)

var requiredBaselineTables = []string{
	"agent_codex_binding",
	"agent_interactions",
	"agent_provider_binding",
	"agent_status",
	"agent_threads",
	"audit_events",
	"bus_exception_logs",
	"command_card_runs",
	"command_card_versions",
	"command_cards",
	"cwd_instance_locks",
	"prompt_template_versions",
	"prompt_versions",
	"prompt_templates",
	"prompts",
	"shared_files",
	"system_logs",
	"task_acks",
	"task_dag_nodes",
	"task_dags",
	"task_traces",
	"topology_approval_archives",
	"topology_approvals",
	"ui_preferences",
	"workspace_run_files",
	"workspace_runs",
}

func applyBaselineIfMissing(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	return applyBaselineIfMissingWithBegin(ctx, func(ctx context.Context) (baselineTx, error) {
		return pool.Begin(ctx)
	}, dir, os.ReadFile)
}

func applyBaselineIfMissingWithBegin(ctx context.Context, begin baselineBeginFunc, dir string, readFile baselineReadFileFunc) error {
	tx, err := begin(ctx)
	if err != nil {
		return fmt.Errorf("begin baseline transaction: %w", err)
	}
	if err := applyBaselineInTx(ctx, tx, dir, readFile); err != nil {
		if rollbackErr := tx.Rollback(context.Background()); rollbackErr != nil {
			return fmt.Errorf("%w; rollback baseline transaction: %v", err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit baseline transaction: %w", err)
	}
	return nil
}

// applyBaselineInTx 在tx应用baseline。
func applyBaselineInTx(ctx context.Context, tx baselineTx, dir string, readFile baselineReadFileFunc) error {
	var hasBaseline bool
	err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = '001_baseline.sql')").Scan(&hasBaseline)
	if err != nil {
		return fmt.Errorf("detect baseline marker: %w", err)
	}
	if hasBaseline {
		return nil
	}

	existingTables, err := countExistingBaselineTables(ctx, tx)
	if err != nil {
		return err
	}
	if existingTables > 0 && existingTables != len(requiredBaselineTables) {
		return fmt.Errorf("partial existing baseline schema: found %d of %d required tables; refusing to mark 001_baseline.sql applied", existingTables, len(requiredBaselineTables))
	}
	if existingTables == 0 {
		c, err := readFile(filepath.Join(dir, "001_baseline.sql"))
		if err != nil {
			return fmt.Errorf("read baseline migration 001_baseline.sql: %w", err)
		}
		if _, err := tx.Exec(ctx, string(c)); err != nil {
			return fmt.Errorf("execute baseline migration 001_baseline.sql: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name, filename) VALUES (1, 'baseline', '001_baseline.sql')"); err != nil {
		return fmt.Errorf("insert baseline marker: %w", err)
	}
	return nil
}

func countExistingBaselineTables(ctx context.Context, tx baselineTx) (int, error) {
	var existingTables int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_name = ANY($1)
	`, requiredBaselineTables).Scan(&existingTables)
	if err != nil {
		return 0, fmt.Errorf("probe existing baseline schema: %w", err)
	}
	return existingTables, nil
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
		if err := rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("scan migration filename: %w", err)
		}
		applied[f] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migrations: %w", err)
	}
	return applied, nil
}

// applyPendingMigrations 应用待处理migrations。
func applyPendingMigrations(ctx context.Context, pool *pgxpool.Pool, dir string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("migration directory not found %q: %w", dir, err)
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

// shouldApplyMigration 判断应用migration是否可用。
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

// registerLifecycle 注册生命周期。
func registerLifecycle(lc fx.Lifecycle, logger *pkglogger.Logger, pool *pgxpool.Pool, cfg *config.Config) {
	embeddedPostgres := newEmbeddedPostgresResource(cfg)
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := embeddedPostgres.Open(ctx); err != nil {
				return err
			}
			failAfterEmbeddedOpen := func(err error) error {
				stopCtx, cancel := config.WithTimeout(context.WithoutCancel(ctx), config.ShutdownTimeout)
				defer cancel()
				if closeErr := embeddedPostgres.Close(stopCtx); closeErr != nil {
					return errors.Join(err, closeErr)
				}
				return err
			}
			poolCfg := pool.Config()
			if err := ensureDatabaseExists(poolCfg.ConnConfig.Database, cfg.DatabaseURL); err != nil {
				return failAfterEmbeddedOpen(err)
			}
			if err := autoMigrate(ctx, pool, cfg.ProjectRoot); err != nil {
				return failAfterEmbeddedOpen(err)
			}
			if err := VerifyMinSchemaVersion(ctx, pool); err != nil {
				return failAfterEmbeddedOpen(err)
			}
			logger.Info("db pool ready")
			if err := pool.Ping(ctx); err != nil {
				return failAfterEmbeddedOpen(err)
			}
			return nil
		},
		OnStop: func(ctx context.Context) error {
			pool.Close()
			logger.Info("db pool closed")
			return embeddedPostgres.Close(ctx)
		},
	})
}

type embeddedPostgresResource struct {
	cfg   *config.Config
	owned bool
}

func newEmbeddedPostgresResource(cfg *config.Config) *embeddedPostgresResource {
	return &embeddedPostgresResource{cfg: cfg}
}

// Open 打开平台数据库。
func (r *embeddedPostgresResource) Open(ctx context.Context) error {
	if err := embeddedpg.Start(ctx, r.cfg.EmbeddedPostgres); err != nil {
		return err
	}
	r.owned = r.cfg.EmbeddedPostgres.Enabled && r.cfg.EmbeddedPostgres.Owner
	return nil
}

// Close 关闭平台数据库资源。
func (r *embeddedPostgresResource) Close(ctx context.Context) error {
	if !r.owned {
		return nil
	}
	return embeddedpg.Stop(ctx, r.cfg.EmbeddedPostgres)
}
