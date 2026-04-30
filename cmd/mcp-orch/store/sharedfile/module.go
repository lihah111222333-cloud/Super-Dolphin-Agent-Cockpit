package sharedfile

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
)

// Module wires Store with disk-source / DB-index behaviour. CWD comes from
// platformconfig.Config.ProjectRoot (the same value memory store uses); when
// it is empty (e.g. unit-test fx graph) the store transparently degrades to
// DB-only mode.
var Module = fx.Module("store.sharedfile",
	fx.Provide(provideStore),
)

func provideStore(q *sqlc.Queries, cfg *platformconfig.Config) Store {
	return NewStoreWithConfig(q, sharedfileFSConfigFrom(cfg))
}

func sharedfileFSConfigFrom(cfg *platformconfig.Config) sharedfilefs.Config {
	if cfg == nil {
		return sharedfilefs.Config{}
	}
	return sharedfilefs.Config{
		CWD:                  cfg.ProjectRoot,
		InlineThresholdBytes: sharedfilefs.DefaultInlineThresholdBytes,
	}
}
