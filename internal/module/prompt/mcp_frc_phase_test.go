package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestMCPInstructionsProviderSkipsFullSectionWhenDeltaEnabled(t *testing.T) {
	provider := MCPInstructionsProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{MCPSnapshot: MCPSnapshot{
		Servers:                  []string{"lsp"},
		Instructions:             map[string]string{"lsp": "Use the LSP MCP first."},
		InstructionsDeltaEnabled: true,
	}}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil when delta mode is enabled", *text)
	}
}

func TestMCPInstructionsProviderTurnAttachmentsDiffAddAndRemove(t *testing.T) {
	provider := MCPInstructionsProvider{tracker: newMCPInstructionsTracker()}
	ctx := SectionContext{Turn: &TurnInput{ThreadID: "thread-mcp-delta"}}
	ctx.BuildCtx.MCPSnapshot = MCPSnapshot{
		Servers:                  []string{"lsp"},
		Instructions:             map[string]string{"lsp": "Use the LSP MCP first."},
		InstructionsDeltaEnabled: true,
	}
	first := provider.ResolveTurnAttachments(context.Background(), ctx)
	if len(first) != 1 || !strings.Contains(first[0].Content, "## lsp") {
		t.Fatalf("first delta attachments = %#v, want add diff for lsp", first)
	}
	second := provider.ResolveTurnAttachments(context.Background(), ctx)
	if len(second) != 0 {
		t.Fatalf("second delta attachments = %#v, want empty when state is unchanged", second)
	}
	ctx.BuildCtx.MCPSnapshot = MCPSnapshot{
		Servers:                  []string{"orch"},
		Instructions:             map[string]string{"orch": "Use DAG tools for orchestration state."},
		InstructionsDeltaEnabled: true,
	}
	third := provider.ResolveTurnAttachments(context.Background(), ctx)
	if len(third) != 1 {
		t.Fatalf("third delta attachments = %#v, want one combined delta attachment", third)
	}
	if !strings.Contains(third[0].Content, "## orch") || !strings.Contains(third[0].Content, "## lsp") || !strings.Contains(third[0].Content, "no longer apply") {
		t.Fatalf("third delta content = %q, want add+revoke diff", third[0].Content)
	}
}

func TestAssembleTurnIncludesMCPInstructionsInRuntimeExtras(t *testing.T) {
	svc := NewService(&Config{}, nil)
	turn, err := svc.AssembleTurn(context.Background(), TurnInput{
		ThreadID:    "thread-mcp-runtime",
		CurrentDate: "2026-04-15",
		MCPSnapshot: MCPSnapshot{
			Servers:      []string{"lsp"},
			Instructions: map[string]string{"lsp": "Use the LSP MCP first."},
		},
	})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	if !strings.Contains(turn.UserContextText, "# MCP Server Instructions") || !strings.Contains(turn.UserContextText, "Use the LSP MCP first.") {
		t.Fatalf("UserContextText = %q, want MCP instructions in runtime extras", turn.UserContextText)
	}
}

func TestFRCProviderResolvesForSupportedModel(t *testing.T) {
	provider := FRCProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{
		Model:     "gpt-5.5",
		FRCConfig: (&contract.FRCConfig{Enabled: true, SupportedModels: []string{"gpt-5.5"}, KeepRecent: 4}).Normalize(),
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil || !strings.Contains(*text, "4 most recent") {
		t.Fatalf("Resolve() = %v, want frc guidance with keepRecent", text)
	}
}

func TestFRCProviderSkipsUnsupportedModel(t *testing.T) {
	provider := FRCProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{
		Model:     "claude-sonnet",
		FRCConfig: (&contract.FRCConfig{Enabled: true, SupportedModels: []string{"gpt-5.5"}, KeepRecent: 2}).Normalize(),
	}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil for unsupported model", *text)
	}
}
