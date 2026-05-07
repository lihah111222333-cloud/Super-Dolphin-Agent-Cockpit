package hookstore

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

var Module = fx.Module("store.hookstore",
	fx.Provide(func(q *sqlc.Queries) contract.HookReviewStore { return NewStore(q) }),
)
