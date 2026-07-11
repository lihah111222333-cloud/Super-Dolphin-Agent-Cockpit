package thread

import (
	"context"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestValidateModelNameTrimsAndAllowsExtendedSyntax(t *testing.T) {
	t.Parallel()

	got, err := validateModelName("  claude-sonnet-4-20250514[1m]  ")
	if err != nil {
		t.Fatalf("validateModelName() error = %v", err)
	}
	if got != "claude-sonnet-4-20250514[1m]" {
		t.Fatalf("validateModelName() = %q, want trimmed model", got)
	}
}

func TestValidateModelNameRejectsEmptyAndInvalid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "   ", want: "model is required"},
		{name: "invalid rune", input: "gpt-5.5!", want: `invalid model name "gpt-5.5!"`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateModelName(tt.input)
			if err == nil {
				t.Fatalf("validateModelName(%q) = %q, want error", tt.input, got)
			}
			if err.Error() != tt.want {
				t.Fatalf("validateModelName(%q) error = %q, want %q", tt.input, err.Error(), tt.want)
			}
		})
	}
}

func TestNormalizeThreadConfigPatchTrimsValues(t *testing.T) {
	t.Parallel()

	model := "  gpt-5.5  "
	effort := "  high  "
	personality := "  concise  "
	approvals := "  on-request  "
	patch, err := normalizeThreadConfigPatch(
		context.Background(),
		&stubSession{threadID: "thread-1", allowedModels: []string{"gpt-5.5"}},
		"codex",
		dto.ThreadConfigPatch{Model: &model, Effort: &effort, Personality: &personality, Approvals: &approvals},
	)
	if err != nil {
		t.Fatalf("normalizeThreadConfigPatch() error = %v", err)
	}
	if patch.Model == nil || *patch.Model != "gpt-5.5" {
		t.Fatalf("patch.Model = %#v, want trimmed model", patch.Model)
	}
	if patch.Effort == nil || *patch.Effort != "high" {
		t.Fatalf("patch.Effort = %#v, want trimmed effort", patch.Effort)
	}
	if patch.Personality == nil || *patch.Personality != "concise" {
		t.Fatalf("patch.Personality = %#v, want trimmed personality", patch.Personality)
	}
	if patch.Approvals == nil || *patch.Approvals != "on-request" {
		t.Fatalf("patch.Approvals = %#v, want trimmed approvals", patch.Approvals)
	}
}

func TestNormalizeThreadConfigPatchOfflineTrimsValues(t *testing.T) {
	t.Parallel()

	model := "  claude-sonnet-4-20250514[1m]  "
	effort := "  max  "
	personality := "  concise  "
	patch, err := normalizeThreadConfigPatchOffline(
		"claude",
		dto.ThreadConfigPatch{Model: &model, Effort: &effort, Personality: &personality},
	)
	if err != nil {
		t.Fatalf("normalizeThreadConfigPatchOffline() error = %v", err)
	}
	if patch.Model == nil || *patch.Model != "claude-sonnet-4-20250514[1m]" {
		t.Fatalf("patch.Model = %#v, want trimmed model", patch.Model)
	}
	if patch.Effort == nil || *patch.Effort != "max" {
		t.Fatalf("patch.Effort = %#v, want trimmed effort", patch.Effort)
	}
	if patch.Personality == nil || *patch.Personality != "concise" {
		t.Fatalf("patch.Personality = %#v, want trimmed personality", patch.Personality)
	}
}
