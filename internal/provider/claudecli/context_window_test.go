package claudecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeModelContextWindowRules(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
		want  int
	}{
		{name: "best", model: "best", want: 272000},
		{name: "opus", model: "opus", want: 272000},
		{name: "opus 1m", model: "opus[1m]", want: 872000},
		{name: "sonnet", model: "sonnet", want: 336000},
		{name: "sonnet 1m", model: "sonnet[1m]", want: 936000},
		{name: "haiku", model: "haiku", want: 336000},
		{name: "unknown", model: "claude-sonnet-4-6-20260219", want: 336000},
		{name: "unknown 1m", model: "claude-sonnet-4-6-20260219[1m]", want: 936000},
		{name: "empty", model: "", want: 336000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeModelContextWindow(tc.model); got != tc.want {
				t.Fatalf("claudeModelContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestClaudeLatestLongModelAliasesAreFresh(t *testing.T) {
	first := claudeLatestLongModelAliases()
	second := claudeLatestLongModelAliases()
	first["claude-opus-4-7"] = "changed"
	if got := second["claude-opus-4-7"]; got != "opus" {
		t.Fatalf("independent alias set changed to %q, want opus", got)
	}
}

func TestClaudeContextWindowPrefersRuntimeAndSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"model":"opus[1m]"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(settings.json) error = %v", err)
	}
	history := &historyBackend{sessionDir: dir}
	if got := readClaudeSettingsModel(history); got != "opus[1m]" {
		t.Fatalf("readClaudeSettingsModel() = %q, want opus[1m]", got)
	}
	if got := claudeContextWindow(123, "sonnet", history); got != 123 {
		t.Fatalf("claudeContextWindow(runtime=123) = %d, want 123", got)
	}
	if got := claudeContextWindow(0, "sonnet[1m]", history); got != 936000 {
		t.Fatalf("claudeContextWindow(sonnet[1m]) = %d, want 936000", got)
	}
	if got := claudeContextWindow(0, "", history); got != 872000 {
		t.Fatalf("claudeContextWindow(settings fallback) = %d, want 872000", got)
	}
}
