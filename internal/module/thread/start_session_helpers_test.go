package thread

import (
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
		Model:    "gpt-5.4",
	})
	if input.ParentAgentID != "agent-root" || input.AgentType != "worker" || input.AgentMemoryScope != "project" {
		t.Fatalf("buildStartAssemblyInput() = %#v, want child-agent metadata", input)
	}
	if input.CWD != "/tmp/project" || input.Provider != "codex" || input.Model != "gpt-5.4" {
		t.Fatalf("buildStartAssemblyInput() basic fields = %#v", input)
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
		Provider:                     "codex",
		CWD:                          "/repo",
		Model:                        "gpt-5.4",
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
	if cfg["provider"] != "codex" || cfg["gitRoot"] != "/repo" || cfg["language"] != "Chinese" {
		t.Fatalf("buildStartSessionConfig() basic context = %#v", cfg)
	}
	if cfg["isWorktree"] != true {
		t.Fatalf("buildStartSessionConfig() isWorktree = %#v, want true", cfg["isWorktree"])
	}
	if got, ok := cfg["enabledTools"].([]string); !ok || len(got) != 2 || got[0] != "lsp_file" || got[1] != "spawn_agent" {
		t.Fatalf("buildStartSessionConfig() enabledTools = %#v", cfg["enabledTools"])
	}
	if got, ok := cfg["mcpInstructions"].(map[string]any); !ok || got["lsp"] != "Use the LSP MCP first." {
		t.Fatalf("buildStartSessionConfig() mcpInstructions = %#v", cfg["mcpInstructions"])
	}
	if got, ok := cfg["sessionFlags"].(map[string]any); !ok || got["verification_required"] != true {
		t.Fatalf("buildStartSessionConfig() sessionFlags = %#v", cfg["sessionFlags"])
	}
	if got, ok := cfg["claudeMdExcludes"].([]string); !ok || len(got) != 1 || got[0] != "/repo/**/CLAUDE.local.md" {
		t.Fatalf("buildStartSessionConfig() claudeMdExcludes = %#v", cfg["claudeMdExcludes"])
	}
	if cfg["parentAgentId"] != "agent-root" || cfg["agentType"] != "worker" || cfg["threadKind"] != "child_agent" {
		t.Fatalf("buildStartSessionConfig() child metadata = %#v", cfg)
	}
}
