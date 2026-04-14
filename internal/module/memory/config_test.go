package memory

import (
	"path/filepath"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

func TestNewConfigUsesEnvOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	t.Setenv(envMemoryRoot, root)

	cfg := NewConfig(&platformconfig.Config{ProjectRoot: t.TempDir()})
	if cfg == nil {
		t.Fatal("NewConfig() returned nil")
	}
	if cfg.RootDir != root {
		t.Fatalf("RootDir = %q, want %q", cfg.RootDir, root)
	}
}
