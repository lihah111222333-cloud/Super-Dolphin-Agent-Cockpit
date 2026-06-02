package sharedfile

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

var Module = fx.Module("store.sharedfile",
	fx.Provide(provideStore),
	fx.Provide(func(s Store) Reader { return s }),
	fx.Provide(func(s Store) Deleter { return s }),
	fx.Provide(func(s Store) Upserter { return s }),
)

type storeParams struct {
	fx.In

	Queries                *sqlc.Queries
	Config                 *platformconfig.Config
	EmitSharedFilesChanged contract.UISharedFilesChangedEmitter `optional:"true"`
}

func provideStore(p storeParams) Store {
	return NewStoreWithConfigAndEmitter(p.Queries, sharedfileFSConfigFrom(p.Config), p.EmitSharedFilesChanged)
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
