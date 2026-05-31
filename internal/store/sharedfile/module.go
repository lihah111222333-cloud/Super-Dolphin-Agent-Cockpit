package sharedfile

import (
	"go.uber.org/fx"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
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

	Queries *sqlc.Queries
	Config  *platformconfig.Config
	Emit    func(uidto.UISharedFilesChanged) `optional:"true"`
}

func provideStore(p storeParams) Store {
	return NewStoreWithConfigAndEmitter(p.Queries, sharedfileFSConfigFrom(p.Config), p.Emit)
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
