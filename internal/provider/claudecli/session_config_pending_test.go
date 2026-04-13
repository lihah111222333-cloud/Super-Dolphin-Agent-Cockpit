package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestConfigureStoresPendingOverrideWithoutChangingLiveState(t *testing.T) {
	model := "sonnet[1m]"
	effort := "max"
	s := &session{
		threadID: "thread-1",
		model:    "opus",
		config:   cliLaunchConfig{Effort: "high"},
	}
	if err := s.Configure(context.Background(), dto.ThreadConfigPatch{Model: &model, Effort: &effort}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if s.model != "opus" || s.config.Effort != "high" {
		t.Fatalf("live state mutated early: model=%q effort=%q", s.model, s.config.Effort)
	}
	if s.overrideModel != model || s.overrideEffort != effort {
		t.Fatalf("override state = %#v, want model=%q effort=%q", s, model, effort)
	}
	if s.pendingModel == nil || *s.pendingModel != model || s.pendingEffort == nil || *s.pendingEffort != effort || !s.configDirty {
		t.Fatalf("pending state not captured: %#v", s)
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if !cfg.SupportsThreadOverride {
		t.Fatal("SupportsThreadOverride = false, want true")
	}
	if cfg.Override.Model != model || cfg.Override.Effort != effort {
		t.Fatalf("Override = %#v, want model=%q effort=%q", cfg.Override, model, effort)
	}
	if cfg.Effective.Model != "opus" || cfg.Effective.Effort != "high" {
		t.Fatalf("Effective = %#v, want model=opus effort=high", cfg.Effective)
	}
}

func TestConfigureAllowsExplicitClear(t *testing.T) {
	empty := ""
	s := &session{
		threadID:          "thread-1",
		model:             "opus",
		config:            cliLaunchConfig{Effort: "high"},
		overrideModel:     "opus",
		overrideEffort:    "high",
		overrideModelSet:  true,
		overrideEffortSet: true,
	}
	if err := s.Configure(context.Background(), dto.ThreadConfigPatch{Model: &empty, Effort: &empty}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if s.pendingModel == nil || *s.pendingModel != "" || s.pendingEffort == nil || *s.pendingEffort != "" {
		t.Fatalf("pending clear not preserved: %#v", s)
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Model != "" || cfg.Override.Effort != "" {
		t.Fatalf("Override = %#v, want explicit clear", cfg.Override)
	}
	if cfg.Effective.Model != "opus" || cfg.Effective.Effort != "high" {
		t.Fatalf("Effective = %#v, want unchanged live state", cfg.Effective)
	}
}

func TestAllowedModelsIncludesShortlistAndCurrentValue(t *testing.T) {
	s := &session{model: "claude-opus-4-6-20260219"}
	models, err := s.AllowedModels(context.Background())
	if err != nil {
		t.Fatalf("AllowedModels() error = %v", err)
	}
	for _, want := range []string{"best", "sonnet[1m]", "opus[1m]", "claude-opus-4-6-20260219"} {
		if !containsString(models, want) {
			t.Fatalf("AllowedModels() = %#v, want %q", models, want)
		}
	}
}

func TestReadConfigFallsBackToSettingsModel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"sonnet[1m]"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(settings.json) error = %v", err)
	}
	s := &session{
		threadID: "thread-1",
		history:  &historyBackend{sessionDir: dir},
		config:   cliLaunchConfig{Effort: "high"},
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Effective.Model != "sonnet[1m]" || cfg.Override.Model != "sonnet[1m]" {
		t.Fatalf("ReadConfig() = %#v, want settings model fallback", cfg)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
