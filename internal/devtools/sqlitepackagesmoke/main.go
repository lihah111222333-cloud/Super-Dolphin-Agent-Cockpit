package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sqliteruntime "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
)

type smokeEnv struct {
	packageRoot string
	home        string
	oldPGData   string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	env, cfg, db, err := prepareSmokeRuntime()
	if err != nil {
		return err
	}
	defer db.Close()
	if err := migrateAndVerifySQLite(ctx, db, env.packageRoot); err != nil {
		return err
	}
	return writeEvidence(cfg)
}

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

func migrateAndVerifySQLite(ctx context.Context, db *sql.DB, packageRoot string) error {
	migrations := filepath.Join(packageRoot, "internal", "platform", "db", "sqlite", "migrations")
	return runSQLiteChecks(ctx, db, migrations, packageRoot)
}

func runSQLiteChecks(ctx context.Context, db *sql.DB, migrations, packageRoot string) error {
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
	return insertSmokeThread(ctx, db, packageRoot)
}

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

func requiredEnv(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

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

func insertSmokeThread(ctx context.Context, db *sql.DB, packageRoot string) error {
	now := time.Now().UTC().UnixMilli()
	_, err := db.ExecContext(ctx, `
INSERT INTO agent_threads (thread_id, name, prompt, model, cwd, status, created_at, updated_at, config_override, prompt_snapshot, agent_key)
VALUES (?, 'Package Smoke', 'sqlite package smoke', 'gpt-5', ?, 'running', ?, ?, '{}', '{}', 'package-smoke')`,
		fmt.Sprintf("package-smoke-%d", now), packageRoot, now, now)
	if err != nil {
		return fmt.Errorf("insert packaged SQLite smoke thread: %w", err)
	}
	return nil
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
