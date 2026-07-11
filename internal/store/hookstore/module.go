package hookstore

import (
	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

var Module = fx.Module("store.hookstore",
	fx.Provide(func(q *sqlc.Queries) contract.HookReviewStore { return NewStore(q) }),
)
