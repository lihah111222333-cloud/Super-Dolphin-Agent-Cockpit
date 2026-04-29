package skilllibrary

import (
	"context"
	"fmt"

	"go.uber.org/fx"
)

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

// runStartup 在 fx OnStart 钩子中跑：seed 内置 skill 到 library，然后 reconcile 到 cache。
// 任一阶段失败则 OnStart 返回 error，阻止 app 启动（fail-closed）。
func runStartup(lc fx.Lifecycle, store *Store, reconciler *Reconciler, cfg Config) {
	lc.Append(fx.Hook{
		OnStart: func(_ context.Context) error {
			if _, err := SeedBuiltins(store, cfg.HarnessVersion); err != nil {
				return fmt.Errorf("skilllibrary startup: seed builtins: %w", err)
			}
			if _, err := reconciler.ReconcileAll(); err != nil {
				return fmt.Errorf("skilllibrary startup: reconcile: %w", err)
			}
			return nil
		},
	})
}
