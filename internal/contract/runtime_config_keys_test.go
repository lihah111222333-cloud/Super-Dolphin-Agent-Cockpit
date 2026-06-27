package contract_test

import (
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestRuntimeConfigFieldsKeepCanonicalKeyAndAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field contract.RuntimeConfigField
		want  []string
	}{
		{name: "provider", field: contract.RuntimeConfigProvider, want: []string{"provider"}},
		{name: "prompt key", field: contract.RuntimeConfigPromptKey, want: []string{"promptKey", "prompt_key"}},
		{name: "cwd", field: contract.RuntimeConfigCWD, want: []string{"cwd"}},
		{name: "git root", field: contract.RuntimeConfigGitRoot, want: []string{"gitRoot", "git_root"}},
		{name: "worktree", field: contract.RuntimeConfigIsWorktree, want: []string{"isWorktree", "is_worktree"}},
		{name: "enabled tools", field: contract.RuntimeConfigEnabledTools, want: []string{"enabledTools", "enabled_tools", "tools"}},
		{name: "additional working dirs", field: contract.RuntimeConfigAdditionalWorkingDirectories, want: []string{"additionalWorkingDirectories", "additional_working_directories"}},
		{name: "session flags", field: contract.RuntimeConfigSessionFlags, want: []string{"sessionFlags", "session_flags"}},
		{name: "output style", field: contract.RuntimeConfigOutputStyleConfig, want: []string{"outputStyleConfig", "output_style_config"}},
		{name: "scratchpad", field: contract.RuntimeConfigScratchpadDir, want: []string{"scratchpadDir", "scratchpad_dir"}},
		{name: "frc", field: contract.RuntimeConfigFRCConfig, want: []string{"frcConfig", "frc_config"}},
		{name: "provider native skills", field: contract.RuntimeConfigProviderNativeSkills, want: []string{"providerNativeSkills", "provider_native_skills"}},
		{name: "disable provider native skills", field: contract.RuntimeConfigDisableProviderNativeSkills, want: []string{"disableProviderNativeSkills", "disable_provider_native_skills"}},
		{name: "mcp tools", field: contract.RuntimeConfigMCPTools, want: []string{"mcpTools", "mcp_tools"}},
		{name: "tool env", field: contract.RuntimeConfigEnv, want: []string{"env"}},
		{name: "auto approve", field: contract.RuntimeConfigAutoApprove, want: []string{"autoApprove", "auto_approve"}},
		{name: "binary dir", field: contract.RuntimeConfigBinaryDir, want: []string{"binary_dir", "binaryDir"}},
		{name: "codex disabled native tools", field: contract.RuntimeConfigCodexDisabledNativeTools, want: []string{"codexDisabledNativeTools"}},
		{name: "model", field: contract.RuntimeConfigModel, want: []string{"model"}},
		{name: "language", field: contract.RuntimeConfigLanguage, want: []string{"language"}},
		{name: "summary", field: contract.RuntimeConfigSummary, want: []string{"summary"}},
		{name: "mcp servers", field: contract.RuntimeConfigMCPServers, want: []string{"mcpServers", "mcp_servers"}},
		{name: "mcp instructions", field: contract.RuntimeConfigMCPInstructions, want: []string{"mcpInstructions", "mcp_instructions"}},
		{name: "mcp instructions delta enabled", field: contract.RuntimeConfigMCPInstructionsDeltaEnabled, want: []string{"mcpInstructionsDeltaEnabled", "mcp_instructions_delta_enabled"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.field.Keys()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Keys() = %#v, want %#v", got, tt.want)
			}
			if tt.field.Canonical != tt.want[0] {
				t.Fatalf("Canonical = %q, want first key %q", tt.field.Canonical, tt.want[0])
			}

			got[0] = "mutated"
			if reflect.DeepEqual(tt.field.Keys(), got) {
				t.Fatalf("Keys() returned mutable backing slice for %#v", tt.field)
			}
		})
	}
}
