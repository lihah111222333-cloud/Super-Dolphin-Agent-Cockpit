package memory

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	nestedpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/nested"
)

func TestNewConfigUsesEnvOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "memory-root")
	t.Setenv(envMemoryRoot, root)

	cfg := NewConfig(&contract.Config{ProjectRoot: newTestGitProjectRoot(t)})
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

	cfg := NewConfig(&contract.Config{ProjectRoot: newTestGitProjectRoot(t)})
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

	cfg := NewConfig(&contract.Config{ProjectRoot: newTestGitProjectRoot(t)})
	if cfg == nil {
		t.Fatal("NewConfig() returned nil")
	}
	if cfg.Kairos.Enabled {
		t.Fatal("Kairos.Enabled = true, want false")
	}
	if cfg.NestedMemory.Enabled {
		t.Fatal("NestedMemory.Enabled = true, want false")
	}
	if got := BuildDailyLogPrompt(false, false, nil); !strings.Contains(got, "KAIROS") {
		t.Fatalf("BuildDailyLogPrompt() missing KAIROS section: %q", got)
	}
	HandleDateChange()
	if got := LoadNestedMemoryPaths(); len(got) != 0 {
		t.Fatalf("LoadNestedMemoryPaths() len = %d, want 0", len(got))
	}
	if nestedpkg.MatchTargetPath("/repo/docs/readme.md", []string{"src/**/*.go"}, "/repo") {
		t.Fatal("MatchTargetPath(non-match) = true, want false")
	}
}

func TestNewConfigAutoDreamIntentOverridesEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envMemoryRoot, root)
	t.Setenv(envClaudeRemoteMemoryDir, "")
	t.Setenv(envMemoryExtractOnStop, "")

	// Without an intent file, env default (false) wins.
	cfg := NewConfig(&contract.Config{ProjectRoot: newTestGitProjectRoot(t)})
	if cfg.ExtractOnStop {
		t.Fatalf("ExtractOnStop = true, want false (no intent, no env)")
	}

	// Persisted intent=true overrides env-default false.
	if err := WriteAutoDreamIntent(root, true); err != nil {
		t.Fatalf("WriteAutoDreamIntent(true) error = %v", err)
	}
	cfg = NewConfig(&contract.Config{ProjectRoot: newTestGitProjectRoot(t)})
	if !cfg.ExtractOnStop {
		t.Fatalf("ExtractOnStop = false, want true (intent=true should override env)")
	}

	// Persisted intent=false overrides env=true.
	if err := WriteAutoDreamIntent(root, false); err != nil {
		t.Fatalf("WriteAutoDreamIntent(false) error = %v", err)
	}
	t.Setenv(envMemoryExtractOnStop, "true")
	cfg = NewConfig(&contract.Config{ProjectRoot: newTestGitProjectRoot(t)})
	if cfg.ExtractOnStop {
		t.Fatalf("ExtractOnStop = true, want false (intent=false should override env=true)")
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
		{
			name: "defined falsy auto memory env does not bypass product kill switch",
			env:  map[string]string{envClaudeDisableAutoMemory: "0"},
			cfg:  Config{Enabled: false},
			want: false,
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

	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)}
	rulesText, err := NewRulesProvider(cfg, nil, nil).Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{},
	})
	if err != nil {
		t.Fatalf("MemoryRulesProvider.Resolve() error = %v", err)
	}
	if rulesText != nil {
		t.Fatalf("MemoryRulesProvider.Resolve() = %#v, want nil in simple mode", rulesText)
	}
	provider := mustNewContextProvider(t, cfg)
	contextText, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn: &contract.TurnInput{ThreadID: "thread-1"},
	})
	if err != nil {
		t.Fatalf("MemoryContextProvider.Resolve() error = %v", err)
	}
	if contextText != nil {
		t.Fatalf("MemoryContextProvider.Resolve() = %#v, want nil in simple mode", contextText)
	}
	if got := NewMemoryLifecycleHooks(memoryLifecycleHookParams{Config: cfg}).enabled; got {
		t.Fatal("NewMemoryLifecycleHooks() kept memory enabled in simple mode")
	}
}

func TestResolveMemoryGateSupportsSettingsAndModeSelection(t *testing.T) {
	cfg := &Config{
		Enabled:             true,
		SkipIndex:           true,
		AutoMemPathOverride: filepath.Join(t.TempDir(), "memory"),
		Features: MemoryFeatureFlags{
			Kairos:            true,
			TeamMemory:        true,
			SearchPastContext: false,
		},
	}
	buildCtx := contract.BuildCtx{
		SessionFlags: map[string]bool{"auto_memory_enabled": false},
	}
	gate := ResolveMemoryGate(buildCtx, cfg)
	if gate.AutoEnabled {
		t.Fatalf("ResolveMemoryGate(settings=false).AutoEnabled = true, want false")
	}

	t.Setenv(envClaudeDisableAutoMemory, "0")
	gate = ResolveMemoryGate(buildCtx, cfg)
	assertKairosMemoryGateSnapshot(t, gate)

	gate = ResolveMemoryGate(contract.BuildCtx{}, &Config{Enabled: true})
	if gate.EnableRelevantPrefetch {
		t.Fatalf("ResolveMemoryGate(default).EnableRelevantPrefetch = true, want false")
	}

	gate = ResolveMemoryGate(contract.BuildCtx{}, &Config{Enabled: true, Features: MemoryFeatureFlags{SearchPastContext: true}})
	if gate.EnableRelevantPrefetch {
		t.Fatalf("ResolveMemoryGate(searchPastContext).EnableRelevantPrefetch = true, want false without skipIndex")
	}
}

func assertKairosMemoryGateSnapshot(t *testing.T, gate MemoryGateSnapshot) {
	t.Helper()
	if !gate.AutoEnabled || !gate.ForceEnabledByEnvFalsy {
		t.Fatalf("ResolveMemoryGate(env falsy) = %+v, want forced enabled snapshot", gate)
	}
	if gate.InjectMemoryIndex {
		t.Fatalf("ResolveMemoryGate(skipIndex).InjectMemoryIndex = true, want false")
	}
	if !gate.EnableRelevantPrefetch {
		t.Fatalf("ResolveMemoryGate(skipIndex).EnableRelevantPrefetch = false, want true")
	}
	if gate.InjectTeamMemIndex {
		t.Fatalf("ResolveMemoryGate(kairos).InjectTeamMemIndex = true, want false")
	}
	if gate.RequestedMemoryMode != MemoryModeKairos {
		t.Fatalf("RequestedMemoryMode = %q, want %q", gate.RequestedMemoryMode, MemoryModeKairos)
	}
	if !gate.KairosActive {
		t.Fatalf("KairosActive = false, want true")
	}
	if gate.EffectiveMemoryMode != MemoryModeKairos {
		t.Fatalf("EffectiveMemoryMode = %q, want %q", gate.EffectiveMemoryMode, MemoryModeKairos)
	}
	if gate.AutoMemPathSource != AutoMemPathSourceSettings {
		t.Fatalf("AutoMemPathSource = %q, want %q", gate.AutoMemPathSource, AutoMemPathSourceSettings)
	}
}

func TestResolveMemoryGateDisableClaudeMdsHonorsCompatEnvAndBareAddDirs(t *testing.T) {
	cfg := &Config{Enabled: true}
	t.Setenv(envClaudeDisableClaudeMds, "1")
	gate := ResolveMemoryGate(contract.BuildCtx{}, cfg)
	if !gate.DisableClaudeMds {
		t.Fatalf("ResolveMemoryGate(env disable).DisableClaudeMds = false, want true")
	}

	t.Setenv(envClaudeDisableClaudeMds, "")
	gate = ResolveMemoryGate(contract.BuildCtx{SessionFlags: map[string]bool{"bare_mode": true}}, cfg)
	if !gate.DisableClaudeMds {
		t.Fatalf("ResolveMemoryGate(bare).DisableClaudeMds = false, want true")
	}

	gate = ResolveMemoryGate(contract.BuildCtx{
		SessionFlags:                 map[string]bool{"bare_mode": true},
		AdditionalWorkingDirectories: []string{t.TempDir()},
	}, cfg)
	if gate.DisableClaudeMds {
		t.Fatalf("ResolveMemoryGate(bare+add-dir).DisableClaudeMds = true, want false")
	}
	if !gate.HasAdditionalDirsForBare {
		t.Fatalf("ResolveMemoryGate(bare+add-dir).HasAdditionalDirsForBare = false, want true")
	}
}

func TestConfigAutoMemPathHelpers(t *testing.T) {
	override := filepath.Join(t.TempDir(), "memory")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         newTestGitProjectRoot(t),
		AutoMemPathOverride: override,
	}
	if !cfg.HasAutoMemPathOverride() {
		t.Fatal("HasAutoMemPathOverride() = false, want true")
	}
	if got := cfg.ResolvedAutoMemPathOverride(); got != override {
		t.Fatalf("ResolvedAutoMemPathOverride() = %q, want %q", got, override)
	}
	if !cfg.IsAutoMemPath(filepath.Join(override, "project.md")) {
		t.Fatal("IsAutoMemPath(override child) = false, want true")
	}
	if cfg.IsAutoMemPath(filepath.Join(t.TempDir(), "other", "project.md")) {
		t.Fatal("IsAutoMemPath(outside path) = true, want false")
	}
}

func TestGateAutoEnabledSeparatesProductKillSwitch(t *testing.T) {
	cfg := &Config{Enabled: false}
	if cfg.IsMemoryEnabled() {
		t.Fatal("IsMemoryEnabled() = true, want false when product kill switch is off")
	}
	gate := ResolveMemoryGate(contract.BuildCtx{}, cfg)
	if !gate.AutoEnabled {
		t.Fatalf("ResolveMemoryGate().AutoEnabled = false, want true with Claude defaults: %+v", gate)
	}
}

func TestGatePathOverrideProvenanceLayering(t *testing.T) {
	trusted := filepath.Join(t.TempDir(), "trusted-memory")
	cfg := &Config{
		Enabled: true,
		TrustedAutoMemPathOverride: TrustedAutoMemPathOverride{
			Path:   trusted,
			Source: TrustedPathSettingSourcePolicy,
		},
	}
	gate := ResolveMemoryGate(contract.BuildCtx{}, cfg)
	if gate.AutoMemPathSource != AutoMemPathSourceSettings {
		t.Fatalf("AutoMemPathSource = %q, want %q", gate.AutoMemPathSource, AutoMemPathSourceSettings)
	}
	if gate.TrustedAutoMemPathSource != TrustedPathSettingSourcePolicy {
		t.Fatalf("TrustedAutoMemPathSource = %q, want %q", gate.TrustedAutoMemPathSource, TrustedPathSettingSourcePolicy)
	}
	if got := cfg.ResolvedAutoMemPathOverride(); got != trusted {
		t.Fatalf("ResolvedAutoMemPathOverride() = %q, want %q", got, trusted)
	}
	provider := mustNewContextProvider(t, cfg)
	if got := provider.memoryRoot; got != trusted {
		t.Fatalf("NewContextProvider().memoryRoot = %q, want %q", got, trusted)
	}
	envOverride := filepath.Join(t.TempDir(), "env-memory")
	t.Setenv(envClaudeMemoryPathOverride, envOverride)
	gate = ResolveMemoryGate(contract.BuildCtx{}, cfg)
	if gate.AutoMemPathSource != AutoMemPathSourceEnv {
		t.Fatalf("env AutoMemPathSource = %q, want %q", gate.AutoMemPathSource, AutoMemPathSourceEnv)
	}
	if gate.TrustedAutoMemPathSource != TrustedPathSettingSourceNone {
		t.Fatalf("env TrustedAutoMemPathSource = %q, want empty provenance", gate.TrustedAutoMemPathSource)
	}
	if got := cfg.ResolvedAutoMemPathOverride(); got != envOverride {
		t.Fatalf("env ResolvedAutoMemPathOverride() = %q, want %q", got, envOverride)
	}
}

func TestGateRulesProvidersRespectProductKillSwitch(t *testing.T) {
	cfg := &Config{Enabled: false, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)}
	rulesText, err := NewRulesProvider(cfg, nil, nil).Resolve(context.Background(), contract.SectionContext{
		Start: &contract.StartInput{},
	})
	if err != nil {
		t.Fatalf("MemoryRulesProvider.Resolve() error = %v", err)
	}
	if rulesText != nil {
		t.Fatalf("MemoryRulesProvider.Resolve() = %#v, want nil when product kill switch is off", rulesText)
	}
	provider := mustNewContextProvider(t, cfg)
	contextText, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn: &contract.TurnInput{ThreadID: "thread-1", UserText: "review notes"},
	})
	if err != nil {
		t.Fatalf("MemoryContextProvider.Resolve() error = %v", err)
	}
	if contextText != nil {
		t.Fatalf("MemoryContextProvider.Resolve() = %#v, want nil when product kill switch is off", contextText)
	}
}

func TestShouldStartRelevantMemoryPrefetchHonorsRuntimeGate(t *testing.T) {
	snapshot := MemoryGateSnapshot{EnableRelevantPrefetch: true}
	if !ShouldStartRelevantMemoryPrefetch(snapshot, contract.TurnInput{UserText: "review notes"}, RelevantPrefetchSurfacedState{}) {
		t.Fatal("ShouldStartRelevantMemoryPrefetch(review notes) = false, want true")
	}
	if ShouldStartRelevantMemoryPrefetch(snapshot, contract.TurnInput{UserText: "continue"}, RelevantPrefetchSurfacedState{}) {
		t.Fatal("ShouldStartRelevantMemoryPrefetch(single word) = true, want false")
	}
	if ShouldStartRelevantMemoryPrefetch(snapshot, contract.TurnInput{UserText: "review notes"}, RelevantPrefetchSurfacedState{TotalBytes: defaultRelevantMemoryBudgetBytes}) {
		t.Fatal("ShouldStartRelevantMemoryPrefetch(no headroom) = true, want false")
	}
}

func TestResolveMemoryGateHarnessFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want MemoryHarness
	}{
		{name: "unset_defaults_to_generic", env: "", want: MemoryHarnessGeneric},
		{name: "claude_code_canonical", env: "claude_code", want: MemoryHarnessClaudeCode},
		{name: "claude_code_dash_form", env: "claude-code", want: MemoryHarnessClaudeCode},
		{name: "claude_code_uppercase", env: "CLAUDE_CODE", want: MemoryHarnessClaudeCode},
		{name: "codex", env: "codex", want: MemoryHarnessCodex},
		{name: "unknown_falls_back_to_generic", env: "gemini", want: MemoryHarnessGeneric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envHarnessKind, tc.env)
			gate := ResolveMemoryGate(contract.BuildCtx{}, &Config{Enabled: true})
			if gate.Harness != tc.want {
				t.Fatalf("ResolveMemoryGate(%q).Harness = %q, want %q", tc.env, gate.Harness, tc.want)
			}
		})
	}
}

func TestResolveMemoryGateOverlaySuppressesIndexAndPrefetch(t *testing.T) {
	t.Setenv(envHarnessKind, "claude_code")
	cfg := &Config{
		Enabled:   true,
		SkipIndex: true, // would normally enable prefetch via SkipIndex branch
		Features:  MemoryFeatureFlags{TeamMemory: true},
	}
	gate := ResolveMemoryGate(contract.BuildCtx{}, cfg)
	if !gate.SuppressForOverlay() {
		t.Fatalf("SuppressForOverlay() = false, want true for claude_code harness")
	}
	if gate.InjectMemoryIndex {
		t.Fatalf("overlay InjectMemoryIndex = true, want false")
	}
	if gate.InjectTeamMemIndex {
		t.Fatalf("overlay InjectTeamMemIndex = true, want false")
	}
	if gate.EnableRelevantPrefetch {
		t.Fatalf("overlay EnableRelevantPrefetch = true, want false")
	}
}

func TestResolveMemoryGateGenericKeepsPrefetchAndIndex(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	cfg := &Config{
		Enabled:   true,
		SkipIndex: true,
		Features:  MemoryFeatureFlags{TeamMemory: true},
	}
	gate := ResolveMemoryGate(contract.BuildCtx{}, cfg)
	if gate.SuppressForOverlay() {
		t.Fatalf("SuppressForOverlay() = true, want false for default generic harness")
	}
	if !gate.EnableRelevantPrefetch {
		t.Fatalf("generic EnableRelevantPrefetch = false, want true (skipIndex branch)")
	}
}

func TestMemoryRulesProviderSuppressedInOverlay(t *testing.T) {
	t.Setenv(envHarnessKind, "claude_code")
	provider := NewRulesProvider(&Config{
		Enabled:  true,
		Features: MemoryFeatureFlags{},
	}, NewMemoryRuleEngine(), nil)
	text, err := provider.Resolve(context.Background(), contract.SectionContext{Start: &contract.StartInput{}})
	if err != nil {
		t.Fatalf("MemoryRulesProvider.Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("MemoryRulesProvider.Resolve() = %q, want nil under overlay", *text)
	}
}
