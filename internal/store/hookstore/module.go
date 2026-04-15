package hookstore

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/jackc/pgx/v5/pgxpool"
)

var Module = fx.Module("store.hookstore",
	fx.Provide(func(pool *pgxpool.Pool) contract.HookReviewStore { return NewStore(pool) }),
)
