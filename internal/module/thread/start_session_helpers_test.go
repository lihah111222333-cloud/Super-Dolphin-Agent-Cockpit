package thread

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestBuildStartAssemblyInputCarriesChildAgentMetadata(t *testing.T) {
	input := buildStartAssemblyInput(StartRequest{
		ParentAgentID:         "agent-root",
		AgentType:             "worker",
		Name:                  "Worker",
		Prompt:                "legacy prompt",
		BaseInstructions:      "system prompt",
		DeveloperInstructions: "dev prompt",
	}, "agent-child", contract.BuildCtx{
		Provider: "codex",
		CWD:      "/tmp/project",
		Model:    "gpt-5.4",
	})
	if input.ParentAgentID != "agent-root" || input.AgentType != "worker" {
		t.Fatalf("buildStartAssemblyInput() = %#v, want child-agent metadata", input)
	}
	if input.CWD != "/tmp/project" || input.Provider != "codex" || input.Model != "gpt-5.4" {
		t.Fatalf("buildStartAssemblyInput() basic fields = %#v", input)
	}
}
