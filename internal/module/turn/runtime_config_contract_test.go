package turn

import "testing"

func TestMergePrepareInputRuntimeUsesRuntimeConfigKeyAliases(t *testing.T) {
	t.Parallel()

	input := mergePrepareInputRuntime(PrepareInput{}, map[string]any{
		"provider":                       "codex",
		"prompt_key":                     "main/review",
		"cwd":                            "/repo",
		"model":                          "gpt-5",
		"git_root":                       "/repo",
		"is_worktree":                    true,
		"language":                       "zh-CN",
		"enabled_tools":                  []any{"file", "launch_agent"},
		"additional_working_directories": []any{"/repo/packages/api"},
		"session_flags":                  map[string]any{"persistent_subagent_default": true},
		"output_style_config": map[string]any{
			"name":   "Review",
			"prompt": "Keep it brief.",
		},
		"scratchpad_dir": "/repo/.tmp/scratchpad",
		"frc_config": map[string]any{
			"enabled": true,
		},
		"mcp_tools":                      []any{"mcp__lsp__grep"},
		"mcp_instructions":               map[string]any{"lsp": "Use LSP first."},
		"disable_provider_native_skills": true,
	})

	if input.Provider != "codex" || input.PromptKey != "main/review" || input.CWD != "/repo" || input.Model != "gpt-5" {
		t.Fatalf("basic runtime fields = %#v", input)
	}
	assertRuntimeConfigRepositoryFields(t, input)
	assertRuntimeConfigToolFields(t, input)
	assertRuntimeConfigPromptFields(t, input)
	assertRuntimeConfigMCPFields(t, input)
}

func TestValidateProviderNativeSkillsRuntimeConfigRejectsMalformedValue(t *testing.T) {
	t.Parallel()

	if err := validateProviderNativeSkillsRuntimeConfig(map[string]any{
		"providerNativeSkills": "false",
	}); err == nil {
		t.Fatal("validateProviderNativeSkillsRuntimeConfig() error = nil, want type rejection")
	}
}

func TestValidateProviderNativeSkillsRuntimeConfigRejectsConflictingAliases(t *testing.T) {
	t.Parallel()

	cases := []map[string]any{
		{"providerNativeSkills": true, "provider_native_skills": false},
		{"disableProviderNativeSkills": true, "disable_provider_native_skills": false},
		{"providerNativeSkills": true, "disableProviderNativeSkills": true},
		{"providerNativeSkills": false, "disableProviderNativeSkills": false},
	}
	for _, cfg := range cases {
		if err := validateProviderNativeSkillsRuntimeConfig(cfg); err == nil {
			t.Fatalf("validateProviderNativeSkillsRuntimeConfig(%#v) error = nil, want conflict rejection", cfg)
		}
	}
}

func TestValidateProviderNativeSkillsRuntimeConfigAllowsEquivalentAliases(t *testing.T) {
	t.Parallel()

	for _, cfg := range []map[string]any{
		{"providerNativeSkills": true, "provider_native_skills": true},
		{"disableProviderNativeSkills": true, "disable_provider_native_skills": true},
		{"providerNativeSkills": true, "disableProviderNativeSkills": false},
		{"providerNativeSkills": false, "disableProviderNativeSkills": true},
	} {
		if err := validateProviderNativeSkillsRuntimeConfig(cfg); err != nil {
			t.Fatalf("validateProviderNativeSkillsRuntimeConfig(%#v) error = %v, want nil", cfg, err)
		}
	}
}

func assertRuntimeConfigRepositoryFields(t *testing.T, input PrepareInput) {
	t.Helper()

	if input.GitRoot != "/repo" || !input.IsWorktree || input.Language != "zh-CN" {
		t.Fatalf("repository runtime fields = %#v", input)
	}
}

func assertRuntimeConfigToolFields(t *testing.T, input PrepareInput) {
	t.Helper()

	if got := input.EnabledTools; len(got) != 2 || got[0] != "file" || got[1] != "launch_agent" {
		t.Fatalf("EnabledTools = %#v", got)
	}
	if got := input.AdditionalWorkingDirectories; len(got) != 1 || got[0] != "/repo/packages/api" {
		t.Fatalf("AdditionalWorkingDirectories = %#v", got)
	}
	if !input.SessionFlags["persistent_subagent_default"] || !input.ManualSkillSelection {
		t.Fatalf("flags/manual = %#v manual=%v", input.SessionFlags, input.ManualSkillSelection)
	}
}

func assertRuntimeConfigPromptFields(t *testing.T, input PrepareInput) {
	t.Helper()

	if input.OutputStyleConfig == nil || input.OutputStyleConfig.Name != "Review" || input.OutputStyleConfig.Prompt != "Keep it brief." {
		t.Fatalf("OutputStyleConfig = %#v", input.OutputStyleConfig)
	}
	if input.ScratchpadDir != "/repo/.tmp/scratchpad" {
		t.Fatalf("ScratchpadDir = %q", input.ScratchpadDir)
	}
	if input.FRCConfig == nil || !input.FRCConfig.Enabled {
		t.Fatalf("FRCConfig = %#v", input.FRCConfig)
	}
}

func assertRuntimeConfigMCPFields(t *testing.T, input PrepareInput) {
	t.Helper()

	if got := input.MCPSnapshot.Tools; len(got) != 1 || got[0] != "mcp__lsp__grep" {
		t.Fatalf("MCPSnapshot.Tools = %#v", got)
	}
	if input.MCPSnapshot.Instructions["lsp"] != "Use LSP first." {
		t.Fatalf("MCPSnapshot.Instructions = %#v", input.MCPSnapshot.Instructions)
	}
}
