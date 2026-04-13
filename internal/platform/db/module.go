package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

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
	
	if err := ensureDatabaseExists(poolCfg.ConnConfig.Database, cfg.DatabaseURL); err != nil {
		return nil, err
	}
	
	poolCfg.MaxConns = 100
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, err
	}
	
	if err := autoMigrate(context.Background(), pool, cfg.ProjectRoot); err != nil {
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
		if _, err := conn.Exec(context.Background(), `CREATE DATABASE "` + targetDB + `"`); err != nil {
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
		n := f.Name()
		if f.IsDir() || !strings.HasSuffix(n, ".sql") || n == "001_baseline.sql" {
			continue
		}
		if strings.HasPrefix(n, "000") || strings.HasPrefix(n, "001") || applied[n] {
			continue
		}
		toApply = append(toApply, n)
	}
	sort.Strings(toApply)

	for _, f := range toApply {
		if err := executeMigration(ctx, pool, dir, f); err != nil {
			return err
		}
	}
	return nil
}

func executeMigration(ctx context.Context, pool *pgxpool.Pool, dir, f string) error {
	c, err := os.ReadFile(filepath.Join(dir, f))
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, string(c)); err != nil {
		return err
	}
	var version int
	fmt.Sscanf(f, "%d_", &version)
	_, err = pool.Exec(ctx, "INSERT INTO schema_migrations (version, name, filename) VALUES ($1, $2, $3)", version, f, f)
	return err
}

func registerLifecycle(lc fx.Lifecycle, logger *pkglogger.Logger, pool *pgxpool.Pool) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
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
