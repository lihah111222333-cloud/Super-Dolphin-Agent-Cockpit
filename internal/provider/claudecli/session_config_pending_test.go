package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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
	assertLiveConfigState(t, s, "opus", "high")
	assertOverrideState(t, s, model, effort)
	assertPendingConfig(t, s, model, effort)
	assertConfigDirty(t, s, true)
	cfg := readSessionConfig(t, s)
	assertThreadOverrideSupported(t, cfg)
	assertThreadConfigValues(t, "Override", cfg.Override, model, effort)
	assertThreadConfigValues(t, "Effective", cfg.Effective, "opus", "high")
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
	assertPendingConfig(t, s, "", "")
	cfg := readSessionConfig(t, s)
	assertThreadConfigValues(t, "Override", cfg.Override, "", "")
	assertThreadConfigValues(t, "Effective", cfg.Effective, "opus", "high")
}

func assertLiveConfigState(t *testing.T, s *session, wantModel, wantEffort string) {
	t.Helper()
	if s.model != wantModel {
		t.Fatalf("live model = %q, want %q", s.model, wantModel)
	}
	if s.config.Effort != wantEffort {
		t.Fatalf("live effort = %q, want %q", s.config.Effort, wantEffort)
	}
}

func assertOverrideState(t *testing.T, s *session, wantModel, wantEffort string) {
	t.Helper()
	if s.overrideModel != wantModel {
		t.Fatalf("overrideModel = %q, want %q", s.overrideModel, wantModel)
	}
	if s.overrideEffort != wantEffort {
		t.Fatalf("overrideEffort = %q, want %q", s.overrideEffort, wantEffort)
	}
}

func assertPendingConfig(t *testing.T, s *session, wantModel, wantEffort string) {
	t.Helper()
	assertPendingString(t, "pendingModel", s.pendingModel, wantModel)
	assertPendingString(t, "pendingEffort", s.pendingEffort, wantEffort)
}

func assertConfigDirty(t *testing.T, s *session, want bool) {
	t.Helper()
	if s.configDirty != want {
		t.Fatalf("configDirty = %v, want %v", s.configDirty, want)
	}
}

func assertPendingString(t *testing.T, field string, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %q", field, want)
	}
	if *got != want {
		t.Fatalf("%s = %q, want %q", field, *got, want)
	}
}

func readSessionConfig(t *testing.T, s *session) dto.ThreadConfig {
	t.Helper()
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	return cfg
}

func assertThreadOverrideSupported(t *testing.T, cfg dto.ThreadConfig) {
	t.Helper()
	if !cfg.SupportsThreadOverride {
		t.Fatal("SupportsThreadOverride = false, want true")
	}
}

func assertThreadConfigValues(t *testing.T, field string, got dto.ThreadConfigValues, wantModel, wantEffort string) {
	t.Helper()
	if got.Model != wantModel {
		t.Fatalf("%s.Model = %q, want %q", field, got.Model, wantModel)
	}
	if got.Effort != wantEffort {
		t.Fatalf("%s.Effort = %q, want %q", field, got.Effort, wantEffort)
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

func TestClaudeAllowedModelsAreFresh(t *testing.T) {
	first := claudeAllowedModels()
	second := claudeAllowedModels()
	first[0] = "changed"
	if got := second[0]; got != "best" {
		t.Fatalf("independent allowed model list changed to %q, want best", got)
	}
}

func TestAllowedModelsDoesNotAdvertiseLatestLongSlug(t *testing.T) {
	s := &session{model: "claude-opus-4-7[1m]"}
	models, err := s.AllowedModels(context.Background())
	if err != nil {
		t.Fatalf("AllowedModels() error = %v", err)
	}
	if containsString(models, "claude-opus-4-7") || containsString(models, "claude-opus-4-7[1m]") {
		t.Fatalf("AllowedModels() = %#v, want latest model aliases only", models)
	}
	if !containsString(models, "opus[1m]") {
		t.Fatalf("AllowedModels() = %#v, want opus[1m]", models)
	}
}

func TestReadConfigDoesNotTreatLiveStateAsOverride(t *testing.T) {
	s := &session{
		threadID:       "thread-1",
		model:          "sonnet",
		transportModel: "sonnet",
		config:         cliLaunchConfig{Effort: "high"},
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Model != "" || cfg.Override.Effort != "" {
		t.Fatalf("Override = %#v, want empty explicit override", cfg.Override)
	}
	if cfg.Effective.Model != "sonnet" || cfg.Effective.Effort != "high" {
		t.Fatalf("Effective = %#v, want sonnet/high", cfg.Effective)
	}
}

func TestReadConfigCanonicalizesEffectiveEffortForClaude(t *testing.T) {
	s := &session{
		threadID:          "thread-1",
		model:             "sonnet",
		transportModel:    "sonnet",
		config:            cliLaunchConfig{Effort: "max"},
		overrideEffort:    "max",
		overrideEffortSet: true,
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Override.Effort != "max" {
		t.Fatalf("Override.Effort = %q, want max", cfg.Override.Effort)
	}
	if cfg.Effective.Effort != "high" {
		t.Fatalf("Effective.Effort = %q, want canonical high", cfg.Effective.Effort)
	}
}

func TestReadConfigFallsBackToSettingsModelWithoutInventingOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"sonnet[1m]"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(settings.json) error = %v", err)
	}
	s := &session{
		threadID: "thread-1",
		history:  &historyBackend{sessionDir: dir, skillMetrics: testSkillMetrics(t)},
		config:   cliLaunchConfig{Effort: "high"},
	}
	cfg, err := s.ReadConfig(context.Background(), "")
	if err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}
	if cfg.Effective.Model != "sonnet[1m]" {
		t.Fatalf("Effective = %#v, want settings model fallback", cfg.Effective)
	}
	if cfg.Override.Model != "" || cfg.Override.Effort != "" {
		t.Fatalf("Override = %#v, want no synthetic override", cfg.Override)
	}
}

func TestRuntimeConfigSnapshotReportsTransportModelAlias(t *testing.T) {
	s := &session{
		threadID:       "thread-1",
		model:          "claude-opus-4-7[1m]",
		transportModel: "opus[1m]",
	}

	got := s.RuntimeConfigSnapshot()
	if got["model"] != "opus[1m]" {
		t.Fatalf("RuntimeConfigSnapshot()[model] = %#v, want opus[1m]", got["model"])
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
