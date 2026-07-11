package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestSQLiteMCPOrchRuntimeReadinessSmoke(t *testing.T) {
	db := newRuntimeSQLiteDB(t)
	if err := sqliteruntime.RunMigrations(context.Background(), db, runtimeSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	if err := verifyMCPOrchDatabaseReady(context.Background(), db); err != nil {
		t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want nil", err)
	}
}

func TestSQLiteMCPOrchConfigCreatesCleanDatabaseAndIgnoresPostgresEnv(t *testing.T) {
	unsetEnvForSQLiteSmoke(t, "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH")

	projectRoot := t.TempDir()
	sqlitePath := filepath.Join(t.TempDir(), "state", "orch.db")
	t.Setenv("PROJECT_ROOT", projectRoot)
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", sqlitePath)
	t.Setenv("DATABASE_URL", "postgres://127.0.0.1:1/should-not-connect")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://127.0.0.1:1/should-not-connect")
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "127.0.0.1:0")
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "")
	t.Setenv("LOG_LEVEL", "")

	cfg, err := platformconfig.New()
	if err != nil {
		t.Fatalf("platformconfig.New() error = %v", err)
	}
	if cfg.SQLitePath != sqlitePath {
		t.Fatalf("SQLitePath = %q, want %q", cfg.SQLitePath, sqlitePath)
	}

	db, err := platformdb.NewDB(cfg)
	if err != nil {
		t.Fatalf("platformdb.NewDB() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := os.Stat(sqlitePath); err != nil {
		t.Fatalf("sqlite DB was not created at configured path: %v", err)
	}
	if err := sqliteruntime.RunMigrations(context.Background(), db, runtimeSQLiteMigrationsDir(t)); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	if err := verifyMCPOrchDatabaseReady(context.Background(), db); err != nil {
		t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want nil", err)
	}
}

func unsetEnvForSQLiteSmoke(t *testing.T, key string) {
	t.Helper()
	old, hadOld := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if hadOld {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}
