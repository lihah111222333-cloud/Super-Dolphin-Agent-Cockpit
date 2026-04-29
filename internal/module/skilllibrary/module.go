package skilllibrary

import "go.uber.org/fx"

// Config 是 skilllibrary 的启动配置。注入方负责填充。
type Config struct {
	LibraryDir     string // ~/.multi-agent/skills-library/
	CacheDir       string // ~/.multi-agent/skills-cache/
	HarnessVersion string // SeedBuiltins 使用
}

var Module = fx.Module("skilllibrary",
	fx.Provide(func(c Config) *Store { return NewStore(c.LibraryDir) }),
	fx.Provide(func(s *Store, c Config) *Reconciler { return NewReconciler(s, c.CacheDir) }),
	fx.Invoke(runStartup),
)
