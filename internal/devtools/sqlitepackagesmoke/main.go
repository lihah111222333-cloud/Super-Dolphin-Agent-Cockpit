package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
)

// smokeEnv 保存包烟测必须由外部脚本注入的环境路径。
type smokeEnv struct {
	packageRoot string // 待验证的解包后项目根目录。
	home        string // 包运行时使用的隔离 HOME。
	oldPGData   string // 旧 PostgreSQL 数据目录，用于验证不会被当作运行时状态。
}

// smokeRunConfig 保存一次包烟测运行的显式依赖。
type smokeRunConfig struct {
	now func() time.Time // 生成最小写入证据使用的时钟。
}

// sqliteCheckConfig 保存 SQLite 校验链路的显式运行配置。
type sqliteCheckConfig struct {
	packageRoot string
	now         func() time.Time
}

// main 在固定超时内运行 SQLite 包烟测，失败时用非零退出码阻断发布脚本。
func main() {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx); err != nil {
		slog.Error("sqlite package smoke failed", "error", err)
		os.Exit(1)
	}
}

// run 准备包运行时、执行 SQLite 迁移校验，并把证据写到 stdout。
func run(ctx context.Context) error {
	return runWithConfig(ctx, smokeRunConfig{now: time.Now})
}

// runWithConfig 使用显式运行配置执行包烟测。
func runWithConfig(ctx context.Context, runCfg smokeRunConfig) error {
	if runCfg.now == nil {
		return fmt.Errorf("smoke clock is required")
	}
	env, cfg, db, err := prepareSmokeRuntime()
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrateAndVerifySQLite(ctx, db, sqliteCheckConfig{packageRoot: env.packageRoot, now: runCfg.now}); err != nil {
		return err
	}
	return writeEvidence(cfg)
}

// prepareSmokeRuntime 校验烟测环境并打开包内 SQLite 数据库。
// 成功返回的 db 由调用方关闭，失败路径不会隐藏配置或数据库打开错误。
func prepareSmokeRuntime() (smokeEnv, *platformconfig.Config, *sql.DB, error) {
	env, err := loadSmokeEnv()
	if err != nil {
		return smokeEnv{}, nil, nil, err
	}
	if err := verifySmokeEnv(env); err != nil {
		return smokeEnv{}, nil, nil, err
	}
	cfg, err := resolveSmokeConfig(env)
	if err != nil {
		return smokeEnv{}, nil, nil, err
	}
	db, err := platformdb.NewDB(cfg)
	if err != nil {
		return smokeEnv{}, nil, nil, fmt.Errorf("open packaged SQLite DB: %w", err)
	}
	return env, cfg, db, nil
}

// loadSmokeEnv 读取烟测所需环境变量；缺少任一变量都会立即失败。
func loadSmokeEnv() (smokeEnv, error) {
	packageRoot, err := requiredEnv("PROJECT_ROOT")
	if err != nil {
		return smokeEnv{}, err
	}
	home, err := requiredEnv("SUPER_DOLPHIN_HOME")
	if err != nil {
		return smokeEnv{}, err
	}
	oldPGData, err := requiredEnv("SUPER_DOLPHIN_PACKAGE_SMOKE_OLD_PG_DATA")
	if err != nil {
		return smokeEnv{}, err
	}
	return smokeEnv{packageRoot: packageRoot, home: home, oldPGData: oldPGData}, nil
}

// verifySmokeEnv 验证包烟测运行在 packaged 模式且仍携带旧 PostgreSQL 输入。
// PostgreSQL 环境和旧数据目录必须存在，后续校验才有能力证明 SQLite runtime 会忽略它们。
func verifySmokeEnv(env smokeEnv) error {
	if got := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_RUNTIME_MODE")); !strings.EqualFold(got, "packaged") {
		return fmt.Errorf("SUPER_DOLPHIN_RUNTIME_MODE = %q, want packaged", got)
	}
	if got := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_PACKAGED_LAUNCHER")); got != "1" {
		return fmt.Errorf("SUPER_DOLPHIN_PACKAGED_LAUNCHER = %q, want 1", got)
	}
	for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING"} {
		if value := strings.TrimSpace(os.Getenv(key)); !strings.HasPrefix(value, "postgres://") {
			return fmt.Errorf("%s = %q, want PostgreSQL env preserved for ignore check", key, value)
		}
	}
	if _, err := os.Stat(filepath.Join(env.oldPGData, "PG_VERSION")); err != nil {
		return fmt.Errorf("old PostgreSQL data dir marker missing: %w", err)
	}
	if err := assertNoPostgresRuntimeArtifacts(env.packageRoot); err != nil {
		return err
	}
	return nil
}

// resolveSmokeConfig 解析 packaged 模式配置，并确认 SQLite 文件落在隔离 HOME 下。
func resolveSmokeConfig(env smokeEnv) (*platformconfig.Config, error) {
	cfg, err := platformconfig.New()
	if err != nil {
		return nil, fmt.Errorf("resolve packaged SQLite config: %w", err)
	}
	if !samePath(cfg.ProjectRoot, env.packageRoot) {
		return nil, fmt.Errorf("ProjectRoot = %q, want package root %q", cfg.ProjectRoot, env.packageRoot)
	}
	wantSQLitePath := filepath.Join(env.home, "super-dolphin.db")
	if !samePath(cfg.SQLitePath, wantSQLitePath) {
		return nil, fmt.Errorf("SQLitePath = %q, want clean packaged home path %q", cfg.SQLitePath, wantSQLitePath)
	}
	return cfg, nil
}

// migrateAndVerifySQLite 使用包内迁移目录推进 SQLite schema 并执行运行时校验。
func migrateAndVerifySQLite(ctx context.Context, db *sql.DB, checkCfg sqliteCheckConfig) error {
	if checkCfg.packageRoot == "" {
		return fmt.Errorf("package root is required")
	}
	if checkCfg.now == nil {
		return fmt.Errorf("smoke clock is required")
	}
	migrations := filepath.Join(checkCfg.packageRoot, "internal", "platform", "db", "sqlite", "migrations")
	return runSQLiteChecks(ctx, db, migrations, checkCfg)
}

// runSQLiteChecks 验证 packaged SQLite 迁移、schema floor、关键 PRAGMA 和最小写入路径。
func runSQLiteChecks(ctx context.Context, db *sql.DB, migrations string, checkCfg sqliteCheckConfig) error {
	if err := sqliteruntime.RunMigrations(ctx, db, migrations); err != nil {
		return fmt.Errorf("run packaged SQLite migrations: %w", err)
	}
	if err := platformdb.VerifyMinSchemaVersion(ctx, db); err != nil {
		return fmt.Errorf("verify packaged SQLite schema floor: %w", err)
	}
	if err := verifyPragma(ctx, db, "journal_mode", "wal"); err != nil {
		return err
	}
	if err := verifyPragma(ctx, db, "foreign_keys", "1"); err != nil {
		return err
	}
	return insertSmokeThread(ctx, db, checkCfg)
}

// writeEvidence 输出包烟测证据，供发布脚本记录实际平台和 SQLite 路径。
func writeEvidence(cfg *platformconfig.Config) error {
	if _, err := os.Stat(cfg.SQLitePath); err != nil {
		return fmt.Errorf("packaged runtime did not create SQLite DB: %w", err)
	}
	evidence := map[string]string{
		"goos":         runtime.GOOS,
		"goarch":       runtime.GOARCH,
		"package_root": cfg.ProjectRoot,
		"sqlite_path":  cfg.SQLitePath,
	}
	if err := json.NewEncoder(os.Stdout).Encode(evidence); err != nil {
		return fmt.Errorf("write smoke evidence: %w", err)
	}
	_, err := fmt.Fprintln(os.Stdout, "sqlite package runtime smoke passed")
	return err
}

// requiredEnv 读取非空环境变量，避免烟测在缺少输入时静默使用默认值。
func requiredEnv(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

// assertNoPostgresRuntimeArtifacts 扫描发布包，发现 PostgreSQL runtime 文件名立即失败。
func assertNoPostgresRuntimeArtifacts(root string) error {
	forbidden := []string{"postgres", "pg_ctl", "initdb", "postgres.bki"}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := strings.ToLower(entry.Name())
		for _, marker := range forbidden {
			if strings.Contains(name, marker) {
				return fmt.Errorf("packaged PostgreSQL runtime artifact %q found at %s", marker, path)
			}
		}
		return nil
	})
}

// verifyPragma 校验 SQLite 运行时必须打开的 PRAGMA，防止包构建漏掉启动设置。
func verifyPragma(ctx context.Context, db *sql.DB, name, want string) error {
	var got string
	if err := db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
		return fmt.Errorf("read PRAGMA %s: %w", name, err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("PRAGMA %s = %q, want %q", name, got, want)
	}
	return nil
}

// insertSmokeThread 写入一条最小 agent_threads 记录，锁定 packaged schema 的基础写路径。
func insertSmokeThread(ctx context.Context, db *sql.DB, checkCfg sqliteCheckConfig) error {
	if checkCfg.now == nil {
		return fmt.Errorf("smoke clock is required")
	}
	now := checkCfg.now().UTC().UnixMilli()
	_, err := db.ExecContext(ctx, `
INSERT INTO agent_threads (thread_id, name, prompt, model, cwd, status, created_at, updated_at, config_override, prompt_snapshot, agent_key)
VALUES (?, 'Package Smoke', 'sqlite package smoke', 'gpt-5', ?, 'running', ?, ?, '{}', '{}', 'package-smoke')`,
		fmt.Sprintf("package-smoke-%d", now), checkCfg.packageRoot, now, now)
	if err != nil {
		return fmt.Errorf("insert packaged SQLite smoke thread: %w", err)
	}
	return nil
}

// samePath 比较清理后的路径；Windows 下按大小写不敏感规则处理包路径。
func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
