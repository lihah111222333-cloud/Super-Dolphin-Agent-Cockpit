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
	t.Setenv(envMemoryExtractOnStop, "true")
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
	if !cfg.ExtractOnStop {
		t.Fatal("ExtractOnStop = false, want true")
	}
	wantGuidelines := []string{"Keep explanations short.", "Prefer absolute dates in summaries."}
	if !reflect.DeepEqual(cfg.ExtraGuidelines, wantGuidelines) {
		t.Fatalf("ExtraGuidelines = %#v, want %#v", cfg.ExtraGuidelines, wantGuidelines)
	}
	wantFeatures := MemoryFeatureFlags{Kairos: true, TeamMemory: true, SearchPastContext: true}
	if cfg.Features != wantFeatures {
		t.Fatalf("Features = %#v, want %#v", cfg.Features, wantFeatures)
	}
	if !cfg.Kairos.Enabled {
		t.Fatal("Kairos.Enabled = false, want true")
	}
	if cfg.NestedMemory.Enabled {
		t.Fatal("NestedMemory.Enabled = true, want false")
	}
}

func TestSkeletonConfigsDefaultDisabledAndPlaceholderHelpers(t *testing.T) {
	t.Setenv(envFeatureKairos, "0")

	cfg := NewConfig(&platformconfig.Config{ProjectRoot: t.TempDir()})
	if cfg == nil {
		t.Fatal("NewConfig() returned nil")
	}
	if cfg.Kairos.Enabled {
		t.Fatal("Kairos.Enabled = true, want false")
	}
	if cfg.NestedMemory.Enabled {
		t.Fatal("NestedMemory.Enabled = true, want false")
	}
	if got := BuildDailyLogPrompt(); got != "" {
		t.Fatalf("BuildDailyLogPrompt() = %q, want empty string", got)
	}
	HandleDateChange()
	if got := LoadNestedMemoryPaths(); len(got) != 0 {
		t.Fatalf("LoadNestedMemoryPaths() len = %d, want 0", len(got))
	}
	if MatchTargetPath() {
		t.Fatal("MatchTargetPath() = true, want false")
	}
}

func TestConfigIsMemoryEnabledHonorsGateConditions(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		cfg  Config
		want bool
	}{
		{name: "config override fallback", cfg: Config{Enabled: true}, want: true},
		{
			name: "enable env does not bypass config override off",
			env:  map[string]string{envEnableMemorySystem: "1"},
			cfg:  Config{Enabled: false},
			want: false,
		},
		{
			name: "disable env wins",
			env: map[string]string{
				envEnableMemorySystem:      "1",
				envClaudeDisableAutoMemory: "1",
			},
			cfg:  Config{Enabled: true},
			want: false,
		},
		{
			name: "simple mode disables memory",
			env: map[string]string{
				envEnableMemorySystem: "1",
				envClaudeSimple:       "1",
			},
			cfg:  Config{Enabled: true},
			want: false,
		},
		{
			name: "remote without persistent storage disables memory",
			env: map[string]string{
				envEnableMemorySystem: "1",
				envClaudeRemote:       "1",
			},
			cfg:  Config{Enabled: true},
			want: false,
		},
		{
			name: "remote with explicit persistent storage stays enabled",
			env: map[string]string{
				envEnableMemorySystem:    "1",
				envClaudeRemote:          "1",
				envClaudeRemoteMemoryDir: filepath.Join("/tmp", "memory-root"),
			},
			cfg:  Config{Enabled: true},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			if got := tc.cfg.IsMemoryEnabled(); got != tc.want {
				t.Fatalf("IsMemoryEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryConstructorsUseIsMemoryEnabled(t *testing.T) {
	t.Setenv(envEnableMemorySystem, "1")
	t.Setenv(envClaudeSimple, "1")

	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: t.TempDir()}
	if got := NewRulesProvider(cfg, nil).autoEnabled; got {
		t.Fatal("NewRulesProvider() kept memory enabled in simple mode")
	}
	if got := NewContextProvider(cfg).enabled; got {
		t.Fatal("NewContextProvider() kept memory enabled in simple mode")
	}
	if got := NewMemoryLifecycleHooks(cfg, nil, nil).enabled; got {
		t.Fatal("NewMemoryLifecycleHooks() kept memory enabled in simple mode")
	}
	if got := NewAgentMemoryPromptProvider(cfg, nil, nil).enabled; got {
		t.Fatal("NewAgentMemoryPromptProvider() kept memory enabled in simple mode")
	}
}
