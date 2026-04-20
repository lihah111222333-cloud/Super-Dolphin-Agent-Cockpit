package prompt

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

// p20.1 merge-in-place 写能力恢复：
//   - `NewStore(q, pool)` 返回完整 `Store`
//   - 同时提供 `Reader` adapter，保证 `internal/module/dashboard` 无感知切换
var Module = fx.Module("store.prompt",
	fx.Provide(newDefaultStore),
	fx.Provide(asReader),
)

func newDefaultStore(q *sqlc.Queries, pool *pgxpool.Pool) Store {
	return NewStore(q, pool)
}

// asReader 把 Store 退降为 Reader 询回 fx 图，dashboard 模块的 `promptstore.Reader`
// 注入依然得到满足；同时避免 "有 Store 但 Reader 未声明” 的 fx 报错。
func asReader(s Store) Reader { return s }
