package thread

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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

// TestBuildStartAssemblyInputCarriesLaunchSkills p20.4 §4.4：验证
// StartRequest.LaunchSkillNames/ForceLaunchSkills 会被投影到 contract.StartInput。
func TestBuildStartAssemblyInputCarriesLaunchSkills(t *testing.T) {
	input := buildStartAssemblyInput(StartRequest{
		Name:                  "Launch Skill Thread",
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev prompt",
		LaunchSkillNames:      []string{"planner", "reviewer"},
		ForceLaunchSkills:     true,
	}, "agent-launch", contract.BuildCtx{Provider: "codex", CWD: "/repo", Model: "gpt-5.4"})
	if len(input.LaunchSkillNames) != 2 || input.LaunchSkillNames[0] != "planner" || input.LaunchSkillNames[1] != "reviewer" {
		t.Fatalf("StartInput.LaunchSkillNames = %#v, want [planner reviewer]", input.LaunchSkillNames)
	}
	if !input.ForceLaunchSkills {
		t.Fatalf("StartInput.ForceLaunchSkills = false, want true")
	}
	emptyInput := buildStartAssemblyInput(StartRequest{Name: "Plain Thread"}, "agent-plain", contract.BuildCtx{Provider: "codex"})
	if emptyInput.LaunchSkillNames != nil {
		t.Fatalf("StartInput.LaunchSkillNames should be nil by default, got %#v", emptyInput.LaunchSkillNames)
	}
	if emptyInput.ForceLaunchSkills {
		t.Fatalf("StartInput.ForceLaunchSkills should be false by default")
	}
}

// TestBuildStartAssemblyMirrorsLaunchSkillsIntoSnapshot p20.4 §4.4：验证
// buildStartAssembly 会把 launch skill 镜像到 assembly 与 Snapshot，并纳入 Hash。
func TestBuildStartAssemblyMirrorsLaunchSkillsIntoSnapshot(t *testing.T) {
	assembly := buildStartAssembly(StartRequest{
		Provider:              "codex",
		Name:                  "Launch Skill Thread",
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev prompt",
		LaunchSkillNames:      []string{"planner", "reviewer"},
		ForceLaunchSkills:     true,
	})
	if len(assembly.LaunchSkillNames) != 2 || assembly.LaunchSkillNames[0] != "planner" {
		t.Fatalf("assembly.LaunchSkillNames = %#v", assembly.LaunchSkillNames)
	}
	if !assembly.ForceLaunchSkills {
		t.Fatalf("assembly.ForceLaunchSkills = false, want true")
	}
	if len(assembly.Snapshot.LaunchSkillNames) != 2 || assembly.Snapshot.LaunchSkillNames[1] != "reviewer" {
		t.Fatalf("assembly.Snapshot.LaunchSkillNames = %#v", assembly.Snapshot.LaunchSkillNames)
	}
	if !assembly.Snapshot.ForceLaunchSkills {
		t.Fatalf("assembly.Snapshot.ForceLaunchSkills = false, want true")
	}
	baseHash := assembly.Snapshot.Hash
	if baseHash == "" {
		t.Fatalf("assembly.Snapshot.Hash is empty")
	}
	another := buildStartAssembly(StartRequest{
		Provider:              "codex",
		Name:                  "Launch Skill Thread",
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev prompt",
		LaunchSkillNames:      []string{"planner"},
		ForceLaunchSkills:     true,
	})
	if another.Snapshot.Hash == baseHash {
		t.Fatalf("snapshot hash should change when launch skill set changes (both=%s)", baseHash)
	}
	forceFlip := buildStartAssembly(StartRequest{
		Provider:              "codex",
		Name:                  "Launch Skill Thread",
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev prompt",
		LaunchSkillNames:      []string{"planner", "reviewer"},
		ForceLaunchSkills:     false,
	})
	if forceFlip.Snapshot.Hash == baseHash {
		t.Fatalf("snapshot hash should change when ForceLaunchSkills flips (both=%s)", baseHash)
	}
}

// TestToProviderStartAssemblyMirrorsLaunchSkills p20.4 §4.4：验证
// contract.StartAssembly → dto.StartAssembly 双向映射会把 launch skill 同时带到
// assembly-level 与 snapshot-level；零值继续 omitempty。
func TestToProviderStartAssemblyMirrorsLaunchSkills(t *testing.T) {
	assembly := buildStartAssembly(StartRequest{
		Provider:              "codex",
		Name:                  "Launch Skill Thread",
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev prompt",
		LaunchSkillNames:      []string{"planner", "reviewer"},
		ForceLaunchSkills:     true,
	})
	providerAssembly := toProviderStartAssembly(assembly)
	if len(providerAssembly.LaunchSkillNames) != 2 || providerAssembly.LaunchSkillNames[0] != "planner" {
		t.Fatalf("providerAssembly.LaunchSkillNames = %#v", providerAssembly.LaunchSkillNames)
	}
	if !providerAssembly.ForceLaunchSkills {
		t.Fatalf("providerAssembly.ForceLaunchSkills = false, want true")
	}
	if len(providerAssembly.Snapshot.LaunchSkillNames) != 2 || !providerAssembly.Snapshot.ForceLaunchSkills {
		t.Fatalf("providerAssembly.Snapshot launch = %#v/force=%v",
			providerAssembly.Snapshot.LaunchSkillNames, providerAssembly.Snapshot.ForceLaunchSkills)
	}
	if providerAssembly.Snapshot.Hash != assembly.Snapshot.Hash {
		t.Fatalf("snapshot hash drift: provider=%s assembly=%s",
			providerAssembly.Snapshot.Hash, assembly.Snapshot.Hash)
	}
	blank := toProviderStartAssembly(contract.StartAssembly{})
	if blank.LaunchSkillNames != nil || blank.ForceLaunchSkills {
		t.Fatalf("blank launch fields = %#v/force=%v", blank.LaunchSkillNames, blank.ForceLaunchSkills)
	}
	var _ dto.StartAssembly = blank
}
