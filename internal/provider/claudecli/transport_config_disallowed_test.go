package claudecli

import (
	"reflect"
	"testing"
)

func TestBuildCLIArgsUsesDefaultDisabledNativeToolsByDefault(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{})
	got := flagValues(args, "--disallowedTools")
	want := []string{"Read,Write,Edit,MultiEdit,Bash,BashOutput,KillShell,Grep,Glob,LS,Agent,AskUserQuestion,CronCreate,CronDelete,CronList,EnterPlanMode,ExitPlanMode,EnterWorktree,ExitWorktree,TodoWrite,ListMcpResources,ReadMcpResource,PushNotification,RemoteTrigger,ScheduleWakeup,SendUserFile,SendUserMessage,SendMessage,Skill,Task,TaskCreate,TaskGet,TaskList,TaskOutput,TaskStop,TaskUpdate,TeamCreate,TeamDelete,ToolSearch,WaitForMcpServers,ShareOnboardingGuide"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--disallowedTools = %#v, want %#v", got, want)
	}
}

func TestBuildCLIArgsUsesConfiguredBuiltinToolsAllowlist(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{
		BuiltinTools: []string{"WebFetch", "Task"},
	})
	got := flagValues(args, "--tools")
	want := []string{"WebFetch,Task"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--tools = %#v, want %#v", got, want)
	}
	if got := flagValues(args, "--disallowedTools"); len(got) != 0 {
		t.Fatalf("--disallowedTools with --tools allowlist = %#v, want none", got)
	}
}

func TestBuildCLIArgsUsesEmptyBuiltinToolsAllowlist(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{
		BuiltinTools: []string{},
	})
	got := flagValues(args, "--tools")
	want := []string{""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--tools empty allowlist = %#v, want %#v", got, want)
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

func TestBuildCLIArgsKeepsHardFilterWhenNoMCPConfig(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "", cliLaunchConfig{
		DisallowedTools: []string{"Read"},
	})
	got := flagValues(args, "--disallowedTools")
	want := []string{"Read"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--disallowedTools without --mcp-config = %#v, want %#v", got, want)
	}
}

func TestConfigFromMapParsesBuiltinToolsKeys(t *testing.T) {
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
			input: map[string]any{"claude_builtin_tools": []any{"WebFetch", "Task"}},
			want:  []string{"WebFetch", "Task"},
		},
		{
			name:  "camelCase string list",
			input: map[string]any{"claudeBuiltinTools": "WebFetch, Task"},
			want:  []string{"WebFetch", "Task"},
		},
		{
			name:  "explicit empty array returns non-nil empty slice",
			input: map[string]any{"claude_builtin_tools": []any{}},
			want:  []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := configFromMap(tc.input).BuiltinTools
			if tc.want == nil {
				if got != nil {
					t.Fatalf("BuiltinTools = %#v, want nil", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuiltinTools = %#v, want %#v", got, tc.want)
			}
		})
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
