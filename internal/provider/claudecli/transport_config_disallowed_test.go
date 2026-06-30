package claudecli

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBuildCLIArgsUsesDefaultDisabledNativeToolsByDefault(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{})
	got := flagValues(args, "--disallowedTools")
	want := []string{"Read,Write,Edit,MultiEdit,Bash,BashOutput,KillShell,Grep,Glob,LS,Agent,AskUserQuestion,CronCreate,CronDelete,CronList,EnterPlanMode,ExitPlanMode,EnterWorktree,ExitWorktree,TodoWrite,ListMcpResources,ReadMcpResource,PushNotification,RemoteTrigger,ScheduleWakeup,SendUserFile,SendUserMessage,SendMessage,Task,TaskCreate,TaskGet,TaskList,TaskOutput,TaskStop,TaskUpdate,TeamCreate,TeamDelete,ToolSearch,WaitForMcpServers,ShareOnboardingGuide"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("--disallowedTools = %#v, want %#v", got, want)
	}
}

func TestClaudeLaunchEnvUsesConfigDirForNativeSkills(t *testing.T) {
	t.Parallel()

	got := claudeLaunchEnv(cliLaunchConfig{ClaudeHome: "/tmp/sd-claude-home"})
	want := []string{"CLAUDE_CONFIG_DIR=/tmp/sd-claude-home"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claudeLaunchEnv() = %#v, want %#v", got, want)
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

func TestBuildCLIArgsRemovesSkillWhenProviderNativeSkillsDisabled(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{
		BuiltinTools:                []string{"WebFetch", "Skill", "Task"},
		DisableProviderNativeSkills: true,
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

func TestBuildCLIArgsDisallowsSkillWhenProviderNativeSkillsDisabledWithoutAllowlist(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{
		DisableProviderNativeSkills: true,
	})
	got := flagValues(args, "--disallowedTools")
	if len(got) != 1 {
		t.Fatalf("--disallowedTools count = %#v, want one merged flag", got)
	}
	ids := strings.Split(got[0], ",")
	if !containsDisallowedToolID(ids, "Skill") {
		t.Fatalf("--disallowedTools = %#v, missing Skill", ids)
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "Skill(") {
			t.Fatalf("--disallowedTools = %#v, must not use skill-name blacklist", ids)
		}
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

func TestBuildCLIArgsMergesAdditionalDisallowedToolsWithDefaults(t *testing.T) {
	t.Parallel()

	args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cliLaunchConfig{
		AdditionalDisallowedTools: []string{"Skill(头脑风暴)", "Read", " Skill(编写计划) "},
	})
	got := flagValues(args, "--disallowedTools")
	if len(got) != 1 {
		t.Fatalf("--disallowedTools count = %#v, want one merged flag", got)
	}
	ids := strings.Split(got[0], ",")
	for _, want := range []string{"Read", "Bash", "Task", "Skill(头脑风暴)", "Skill(编写计划)"} {
		if !containsDisallowedToolID(ids, want) {
			t.Fatalf("--disallowedTools = %#v, missing %q", ids, want)
		}
	}
	if countDisallowedToolID(ids, "Read") != 1 {
		t.Fatalf("--disallowedTools = %#v, want Read deduped once", ids)
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

func TestConfigFromMapParsesCamelCaseApprovalPolicyForPermissionMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		policy string
	}{
		{name: "on failure", policy: "on-failure"},
		{name: "untrusted", policy: "untrusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := configFromMap(map[string]any{"approvalPolicy": tc.policy})
			args := buildCLIArgs("claude-sonnet", "system", "/tmp/mcp.json", cfg)

			if cfg.ApprovalPolicy != tc.policy {
				t.Fatalf("ApprovalPolicy = %q, want %q", cfg.ApprovalPolicy, tc.policy)
			}
			if !hasArgValue(args, "--permission-mode", "default") {
				t.Fatalf("args missing secure permission mode for %q: %v", tc.policy, args)
			}
			if hasArgValue(args, "--permission-mode", "bypassPermissions") {
				t.Fatalf("args must not bypass permissions for %q: %v", tc.policy, args)
			}
		})
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

func TestConfigFromMapParsesAdditionalDisallowedToolsKeys(t *testing.T) {
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
			input: map[string]any{"additional_disallowed_tools": []any{"Skill(头脑风暴)", "Skill(编写计划)"}},
			want:  []string{"Skill(头脑风暴)", "Skill(编写计划)"},
		},
		{
			name:  "camelCase string list",
			input: map[string]any{"additionalDisallowedTools": "Skill(执行计划), Skill(子代理驱动开发)"},
			want:  []string{"Skill(执行计划)", "Skill(子代理驱动开发)"},
		},
		{
			name:  "explicit empty array returns non-nil empty slice",
			input: map[string]any{"additional_disallowed_tools": []any{}},
			want:  []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := configFromMap(tc.input).AdditionalDisallowedTools
			if tc.want == nil {
				if got != nil {
					t.Fatalf("AdditionalDisallowedTools = %#v, want nil", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("AdditionalDisallowedTools = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestValidateClaudeSecurityConfigRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input map[string]any
		want  string
	}{
		{
			name:  "disallowed tools rejects object",
			input: map[string]any{"disallowed_tools": map[string]any{"tool": "Read"}},
			want:  "disallowed_tools",
		},
		{
			name:  "additional disallowed tools rejects non-string element",
			input: map[string]any{"additional_disallowed_tools": []any{"Read", 42}},
			want:  "additional_disallowed_tools",
		},
		{
			name:  "builtin tools rejects object",
			input: map[string]any{"builtin_tools": map[string]any{"tool": "Read"}},
			want:  "builtin_tools",
		},
		{
			name:  "provider native skills rejects string",
			input: map[string]any{"providerNativeSkills": "false"},
			want:  "providerNativeSkills",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClaudeSecurityConfig(tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateClaudeSecurityConfig() error = %v, want key %q", err, tc.want)
			}
		})
	}
}

func TestConfigFromMapParsesProviderNativeSkillsIsolation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input map[string]any
		want  bool
	}{
		{
			name:  "default keeps provider native skills",
			input: map[string]any{"effort": "high"},
			want:  false,
		},
		{
			name:  "positive flag false disables provider native skills",
			input: map[string]any{"providerNativeSkills": false},
			want:  true,
		},
		{
			name:  "snake flag false disables provider native skills",
			input: map[string]any{"provider_native_skills": false},
			want:  true,
		},
		{
			name:  "explicit disable flag true disables provider native skills",
			input: map[string]any{"disableProviderNativeSkills": true},
			want:  true,
		},
		{
			name:  "positive flag true keeps provider native skills",
			input: map[string]any{"providerNativeSkills": true},
			want:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := configFromMap(tc.input).DisableProviderNativeSkills
			if got != tc.want {
				t.Fatalf("DisableProviderNativeSkills = %v, want %v", got, tc.want)
			}
		})
	}
}

func containsDisallowedToolID(values []string, want string) bool {
	return slices.Contains(values, want)
}

func countDisallowedToolID(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}
