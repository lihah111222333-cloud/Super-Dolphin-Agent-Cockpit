package thread

import (
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestBuildStartAssemblyInputCarriesChildAgentMetadata(t *testing.T) {
	input := buildStartAssemblyInput(StartRequest{
		ParentAgentID:         "agent-root",
		AgentType:             "worker",
		AgentMemoryScope:      "project",
		Name:                  "Worker",
		Prompt:                "legacy prompt",
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev prompt",
	}, "agent-child", contract.BuildCtx{
		Provider: "codex",
		CWD:      "/tmp/project",
		Model:    "gpt-5.5",
	})
	if input.ParentAgentID != "agent-root" || input.AgentType != "worker" || input.AgentMemoryScope != "project" {
		t.Fatalf("buildStartAssemblyInput() = %#v, want child-agent metadata", input)
	}
	if input.CWD != "/tmp/project" || input.Provider != "codex" || input.Model != "gpt-5.5" {
		t.Fatalf("buildStartAssemblyInput() basic fields = %#v", input)
	}
}

func TestToProviderStartAssemblyCarriesRuntimeContext(t *testing.T) {
	assembly := toProviderStartAssembly(contract.StartAssembly{
		BaseInstructions: "base",
		UserContext: map[string]string{
			"runtimeExtras": "可用专家: main/expert/prompt",
		},
		UserContextText: "# runtimeExtras\n可用专家: main/expert/prompt",
		SystemContext:   contract.SystemContext{"gitStatus": "## main\n M prompt.go"},
	})

	if assembly.UserContext["runtimeExtras"] != "可用专家: main/expert/prompt" {
		t.Fatalf("UserContext not carried to provider start assembly: %#v", assembly.UserContext)
	}
	if assembly.UserContextText == "" {
		t.Fatalf("UserContextText not carried to provider start assembly")
	}
	if assembly.SystemContext["gitStatus"] == "" {
		t.Fatalf("SystemContext not carried to provider start assembly: %#v", assembly.SystemContext)
	}
}

func TestBuildStartSessionConfigCarriesTurnContextFields(t *testing.T) {
	cfg := buildStartSessionConfig(StartRequest{
		ApprovalPolicy: "on-request",
		ModelProvider:  "openai",
		Summary:        "summary",
		Effort:         "high",
		Personality:    "strict",
	}, contract.StartInput{
		ParentAgentID:                "agent-root",
		AgentType:                    "worker",
		PromptKey:                    "main/launch-fav",
		Provider:                     "codex",
		CWD:                          "/repo",
		Model:                        "gpt-5.5",
		GitRoot:                      "/repo",
		IsWorktree:                   true,
		Language:                     "Chinese",
		EnabledTools:                 []string{"lsp_file", "spawn_agent"},
		AdditionalWorkingDirectories: []string{"/repo/extra"},
		ClaudeMdExcludes:             []string{"/repo/**/CLAUDE.local.md"},
		MCPSnapshot: contract.MCPSnapshot{
			Servers:      []string{"lsp"},
			Tools:        []string{"mcp__lsp__lsp_grep"},
			Instructions: map[string]string{"lsp": "Use the LSP MCP first."},
		},
		SessionFlags: map[string]bool{"verification_required": true},
	}, contract.StartAssembly{DeveloperInstructions: "dev prompt"})
	requireSessionConfigValue(t, cfg, "provider", "codex")
	requireSessionConfigValue(t, cfg, "gitRoot", "/repo")
	requireSessionConfigValue(t, cfg, "language", "Chinese")
	requireSessionConfigValue(t, cfg, "isWorktree", true)
	requireSessionConfigStringSlice(t, cfg, "enabledTools", []string{"lsp_file", "spawn_agent"})
	requireSessionConfigMapValue(t, cfg, "mcpInstructions", "lsp", "Use the LSP MCP first.")
	requireSessionConfigMapValue(t, cfg, "sessionFlags", "verification_required", true)
	requireSessionConfigStringSlice(t, cfg, "claudeMdExcludes", []string{"/repo/**/CLAUDE.local.md"})
	requireSessionConfigValue(t, cfg, "parentAgentId", "agent-root")
	requireSessionConfigValue(t, cfg, "agentType", "worker")
	requireSessionConfigValue(t, cfg, "promptKey", "main/launch-fav")
	requireSessionConfigValue(t, cfg, "prompt_key", "main/launch-fav")
	requireSessionConfigValue(t, cfg, "threadKind", "child_agent")
}

func TestBuildStartSessionConfigDoesNotTreatProviderAsModelProvider(t *testing.T) {
	cfg := buildStartSessionConfig(StartRequest{
		Provider:      "codex",
		ModelProvider: "codex",
	}, contract.StartInput{
		Provider: "codex",
		CWD:      "/repo",
	}, contract.StartAssembly{})

	if got, exists := cfg["modelProvider"]; exists {
		t.Fatalf("modelProvider = %#v, want omitted when it only repeats provider", got)
	}
	requireSessionConfigValue(t, cfg, "provider", "codex")
}

func TestBuildStartSessionConfigKeepsExplicitModelProviderOverride(t *testing.T) {
	cfg := buildStartSessionConfig(StartRequest{
		Provider:      "codex",
		ModelProvider: "openai",
	}, contract.StartInput{
		Provider: "codex",
		CWD:      "/repo",
	}, contract.StartAssembly{})

	requireSessionConfigValue(t, cfg, "modelProvider", "openai")
	requireSessionConfigValue(t, cfg, "provider", "codex")
}

func requireSessionConfigValue(t *testing.T, cfg map[string]any, key string, want any) {
	t.Helper()
	if cfg[key] != want {
		t.Fatalf("buildStartSessionConfig() %s = %#v, want %#v", key, cfg[key], want)
	}
}

func requireSessionConfigStringSlice(t *testing.T, cfg map[string]any, key string, want []string) {
	t.Helper()
	got, ok := cfg[key].([]string)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("buildStartSessionConfig() %s = %#v, want %#v", key, cfg[key], want)
	}
}

func requireSessionConfigMapValue(t *testing.T, cfg map[string]any, key, nestedKey string, want any) {
	t.Helper()
	got, ok := cfg[key].(map[string]any)
	if !ok || got[nestedKey] != want {
		t.Fatalf("buildStartSessionConfig() %s = %#v, want %s=%#v", key, cfg[key], nestedKey, want)
	}
}

func TestBuildStartSessionConfigCarriesCodexDisabledNativeTools(t *testing.T) {
	cfg := buildStartSessionConfig(StartRequest{}, contract.StartInput{
		Provider: "codex",
	}, contract.StartAssembly{
		SuppressedTools: []string{" apply_patch ", "", "shell"},
	})
	got, ok := cfg["codexDisabledNativeTools"].([]string)
	if !ok {
		t.Fatalf("codexDisabledNativeTools = %#v, want []string", cfg["codexDisabledNativeTools"])
	}
	if want := []string{"apply_patch", "shell"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codexDisabledNativeTools = %#v, want %#v", got, want)
	}

	claudeCfg := buildStartSessionConfig(StartRequest{}, contract.StartInput{
		Provider: "claude",
	}, contract.StartAssembly{
		SuppressedTools: []string{"shell"},
	})
	if _, exists := claudeCfg["codexDisabledNativeTools"]; exists {
		t.Fatalf("claude config must not carry Codex policy key: %#v", claudeCfg)
	}
}

func TestBuildStartSessionConfigFiltersSpawnAgentWhenPersistentManagedLaunchEnabled(t *testing.T) {
	cfg := buildStartSessionConfig(StartRequest{}, contract.StartInput{
		EnabledTools: []string{"spawn_agent", "orchestration_launch_agent", "request_user_input"},
		SessionFlags: map[string]bool{"persistent_subagent_default": true},
	}, contract.StartAssembly{})

	got, ok := cfg["enabledTools"].([]string)
	if !ok || len(got) != 2 || got[0] != "orchestration_launch_agent" || got[1] != "request_user_input" {
		t.Fatalf("buildStartSessionConfig() enabledTools = %#v, want managed-only child-agent tools", cfg["enabledTools"])
	}
}
