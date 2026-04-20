package claudecli

import (
	"reflect"
	"testing"
)

func TestBuildCLIArgsUsesLegacyDisallowedListByDefault(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{})
	got := flagValues(args, "--disallowedTools")
	want := []string{"Read,Write,Edit,MultiEdit,Bash,Grep,Glob,LS"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--disallowedTools = %#v, want %#v", got, want)
	}
}

func TestBuildCLIArgsHonorsConfiguredDisallowedOverride(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{
		DisallowedTools: []string{"Read", "Bash", "WebFetch"},
	})
	got := flagValues(args, "--disallowedTools")
	want := []string{"Read,Bash,WebFetch"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--disallowedTools override = %#v, want %#v", got, want)
	}
}

func TestBuildCLIArgsSkipsDisallowedFlagWhenOverrideExplicitlyEmpty(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{
		DisallowedTools: []string{},
	})
	if got := flagValues(args, "--disallowedTools"); len(got) != 0 {
		t.Fatalf("--disallowedTools with empty override = %#v, want none", got)
	}
}

func TestBuildCLIArgsOmitsDisallowedFlagWhenNoMCPConfig(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "", cliLaunchConfig{
		DisallowedTools: []string{"Read"},
	})
	if got := flagValues(args, "--disallowedTools"); len(got) != 0 {
		t.Fatalf("--disallowedTools without --mcp-config = %#v, want none", got)
	}
}

func TestConfigFromMapParsesDisallowedToolsKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input map[string]any
		want  []string
	}{
		{
			name:  "nil when key absent",
			input: map[string]any{"effort": "high"},
			want:  nil,
		},
		{
			name:  "snake_case array",
			input: map[string]any{"disallowed_tools": []any{"Read", "Bash"}},
			want:  []string{"Read", "Bash"},
		},
		{
			name:  "camelCase string list",
			input: map[string]any{"disallowedTools": "Read, Edit , Write"},
			want:  []string{"Read", "Edit", "Write"},
		},
		{
			name:  "explicit empty array returns non-nil empty slice",
			input: map[string]any{"disallowed_tools": []any{}},
			want:  []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := configFromMap(tc.input).DisallowedTools
			if tc.want == nil {
				if got != nil {
					t.Fatalf("DisallowedTools = %#v, want nil", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DisallowedTools = %#v, want %#v", got, tc.want)
			}
		})
	}
}
