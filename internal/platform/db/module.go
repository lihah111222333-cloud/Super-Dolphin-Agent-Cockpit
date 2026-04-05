package db

import (
	"context"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

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
	return pgxpool.NewWithConfig(context.Background(), poolCfg)
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
