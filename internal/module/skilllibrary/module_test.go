package skilllibrary

import (
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestModuleWiresUpReconciler(t *testing.T) {
	var r *Reconciler
	app := fxtest.New(t,
		skillforge.Module,
		Module,
		fx.Provide(func() Config {
			return Config{LibraryDir: t.TempDir(), CacheDir: t.TempDir()}
		}),
		fx.Populate(&r),
	)
	defer app.RequireStart().RequireStop()
	if r == nil {
		t.Fatal("Reconciler not provided")
	}
}

func TestModuleWiresUpStore(t *testing.T) {
	var s *Store
	app := fxtest.New(t,
		skillforge.Module,
		Module,
		fx.Provide(func() Config {
			return Config{LibraryDir: t.TempDir(), CacheDir: t.TempDir()}
		}),
		fx.Populate(&s),
	)
	defer app.RequireStart().RequireStop()
	if s == nil {
		t.Fatal("Store not provided")
	}
}
