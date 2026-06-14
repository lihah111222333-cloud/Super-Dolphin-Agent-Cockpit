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
	fx.Provide(
		provideConcreteStore,
		ProvideStore,
		// ProvideReader 从聚合 Store 拆出窄端口 Reader。Store interface 嵌入
		// Reader（见 contract.go），任何 Store 实现必然也实现 Reader。
		// dispatcher RunContext wiring 闭合（orchestration.NewStoreSharedFileReader）
		// 需要这个 narrow port adapter。
		ProvideReader,
		ProvideImporter,
	),
)

func provideConcreteStore(q *sqlc.Queries, cfg *platformconfig.Config) (*store, error) {
	fsCfg, err := sharedfileFSConfigFrom(cfg)
	if err != nil {
		return nil, err
	}
	return newStoreWithConfig(q, fsCfg), nil
}

// ProvideStore 提供存储。
func ProvideStore(store *store) Store { return store }

// ProvideReader 从聚合 Store 拆出 Reader。Store interface 显式嵌入 Reader
// （见 contract.go:26-29），所以该转换编译期安全。
func ProvideReader(store Store) Reader { return store }

// ProvideImporter 提供importer。
func ProvideImporter(store *store) Importer { return store }

func sharedfileFSConfigFrom(cfg *platformconfig.Config) (sharedfilefs.Config, error) {
	root, err := platformconfig.SharedFileRoot(cfg)
	if err != nil {
		return sharedfilefs.Config{}, err
	}
	return sharedfilefs.Config{
		CWD:                  root,
		InlineThresholdBytes: sharedfilefs.DefaultInlineThresholdBytes,
	}, nil
}
