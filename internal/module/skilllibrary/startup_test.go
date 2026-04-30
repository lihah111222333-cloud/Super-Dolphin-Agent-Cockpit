package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

func TestStartup_SeedsAndReconciles(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()

	app := fxtest.New(t,
		skillforge.Module,
		Module,
		fx.Provide(func() Config {
			return Config{LibraryDir: libDir, CacheDir: cacheDir, HarnessVersion: "test-1"}
		}),
	)
	defer app.RequireStart().RequireStop()

	names, _ := skillforge.ListEmbeddedSkillNames()
	if len(names) == 0 {
		t.Fatal("no embedded skills available; cannot validate seed")
	}
	for _, n := range names[:1] {
		if _, err := os.Stat(filepath.Join(libDir, n, ".skill-meta.json")); err != nil {
			t.Errorf("library missing %s after startup: %v", n, err)
		}
		if _, err := os.Stat(filepath.Join(cacheDir, n, "SKILL.md")); err != nil {
			t.Errorf("cache missing %s after startup: %v", n, err)
		}
	}
}

func TestStartup_IsIdempotent(t *testing.T) {
	libDir := t.TempDir()
	cacheDir := t.TempDir()
	cfg := Config{LibraryDir: libDir, CacheDir: cacheDir, HarnessVersion: "test-1"}

	for i := 0; i < 2; i++ {
		func() {
			app := fxtest.New(t,
				skillforge.Module,
				Module,
				fx.Provide(func() Config { return cfg }),
			)
			defer app.RequireStop()
			app.RequireStart()
		}()
	}
	names, _ := skillforge.ListEmbeddedSkillNames()
	if _, err := os.Stat(filepath.Join(cacheDir, names[0])); err != nil {
		t.Errorf("cache lost between starts: %v", err)
	}
}
