package claudecli

import (
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestNormalizeEffortRespectsClaudeModelFamily(t *testing.T) {
	for _, tc := range []struct {
		name   string
		model  string
		effort string
		want   string
	}{
		{name: "best keeps max", model: "best", effort: "max", want: "max"},
		{name: "opus keeps max", model: "claude-opus-4-6", effort: "max", want: "max"},
		{name: "sonnet demotes max", model: "sonnet", effort: "max", want: "high"},
		{name: "haiku demotes max", model: "haiku", effort: "max", want: "high"},
		{name: "xhigh maps high", model: "sonnet", effort: "xhigh", want: "high"},
		{name: "minimal maps low", model: "sonnet", effort: "minimal", want: "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeEffort(tc.model, tc.effort); got != tc.want {
				t.Fatalf("normalizeEffort(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
			}
		})
	}
}

func TestBuildCLIArgsIncludesModelAndNormalizedEffort(t *testing.T) {
	args := mustBuildCLIArgs(t, "best", "system", "", cliLaunchConfig{Effort: "max"})
	if !hasFlagValue(args, "--model", "best") {
		t.Fatalf("buildCLIArgs() = %#v, want --model best", args)
	}
	if !hasFlagValue(args, "--effort", "max") {
		t.Fatalf("buildCLIArgs() = %#v, want --effort max", args)
	}

	args = mustBuildCLIArgs(t, "sonnet", "system", "", cliLaunchConfig{Effort: "max"})
	if !hasFlagValue(args, "--effort", "high") {
		t.Fatalf("buildCLIArgs() = %#v, want sonnet max -> --effort high", args)
	}
}

func TestBuildCLIArgsDropsAccidentalObjectModelString(t *testing.T) {
	args := mustBuildCLIArgs(t, "[object Object]", "system", "", cliLaunchConfig{Effort: "high"})
	if hasFlag(args, "--model") {
		t.Fatalf("buildCLIArgs() = %#v, want no --model for object artifact", args)
	}
}

func TestResolveRequestedStartConfigSanitizesObjectModelArtifact(t *testing.T) {
	model := "sonnet"
	got, _ := resolveRequestedStartConfig(startSpec{
		model: "[object Object]",
		configOverride: dto.ThreadConfigPatch{
			Model: &model,
		},
	})
	if got != "sonnet" {
		t.Fatalf("resolveRequestedStartConfig model = %q, want sonnet", got)
	}
}

func hasFlagValue(args []string, flag, want string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == want {
			return true
		}
	}
	return false
}
