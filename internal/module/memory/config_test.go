package memory

import (
	"path/filepath"
	"reflect"
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

func TestNewConfigSupportsClaudeCompatOverridesAndFlags(t *testing.T) {
	root := filepath.Join(t.TempDir(), "remote-memory-root")
	override := filepath.Join(t.TempDir(), "projects", "repo", "memory")
	t.Setenv(envMemoryRoot, "")
	t.Setenv(envClaudeRemoteMemoryDir, root)
	t.Setenv(envMemoryPathOverride, "")
	t.Setenv(envClaudeMemoryPathOverride, override)
	t.Setenv(envMemorySkipIndex, "1")
	t.Setenv(envMemoryExtraGuidelines, "")
	t.Setenv(envClaudeMemoryExtraGuidelines, "Keep explanations short.\nPrefer absolute dates in summaries.")
	t.Setenv(envFeatureKairos, "1")
	t.Setenv(envFeatureTeamMemory, "true")
	t.Setenv(envFeatureSearchPastContext, "yes")

	cfg := NewConfig(&platformconfig.Config{ProjectRoot: t.TempDir()})
	if cfg == nil {
		t.Fatal("NewConfig() returned nil")
	}
	if cfg.RootDir != root {
		t.Fatalf("RootDir = %q, want %q", cfg.RootDir, root)
	}
	if cfg.AutoMemPathOverride != override {
		t.Fatalf("AutoMemPathOverride = %q, want %q", cfg.AutoMemPathOverride, override)
	}
	if !cfg.SkipIndex {
		t.Fatal("SkipIndex = false, want true")
	}
	wantGuidelines := []string{"Keep explanations short.", "Prefer absolute dates in summaries."}
	if !reflect.DeepEqual(cfg.ExtraGuidelines, wantGuidelines) {
		t.Fatalf("ExtraGuidelines = %#v, want %#v", cfg.ExtraGuidelines, wantGuidelines)
	}
	wantFeatures := MemoryFeatureFlags{Kairos: true, TeamMemory: true, SearchPastContext: true}
	if cfg.Features != wantFeatures {
		t.Fatalf("Features = %#v, want %#v", cfg.Features, wantFeatures)
	}
}
