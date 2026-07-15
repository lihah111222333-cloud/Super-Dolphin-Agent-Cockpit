package sharedfile

import (
	"database/sql"

	"go.uber.org/fx"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	sharedfilefs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilefs"
)

// Module 注册 sharedfile store 的 fx provider。
// ProjectRoot 为空时使用 DB-only 模式，供轻量测试图复用；生产图会启用磁盘正文和 DB 索引。
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

// provideConcreteStore 从平台配置解析 sharedfile 根目录后创建具体 store。
func provideConcreteStore(db *sql.DB, cfg *platformconfig.Config) (*store, error) {
	fsCfg, err := sharedfileFSConfigFrom(cfg)
	if err != nil {
		return nil, err
	}
	return newStoreWithConfig(db, fsCfg), nil
}

// ProvideStore 将具体 sharedfile store 暴露为 Store 接口。
func ProvideStore(store *store) Store { return store }

// ProvideReader 从聚合 Store 拆出 Reader。Store interface 显式嵌入 Reader
// （见 contract.go:26-29），所以该转换编译期安全。
func ProvideReader(store Store) Reader { return store }

// ProvideImporter 将具体 sharedfile store 暴露为 Importer 接口。
func ProvideImporter(store *store) Importer { return store }

// sharedfileFSConfigFrom 从平台配置生成 sharedfile 磁盘存储配置。
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
