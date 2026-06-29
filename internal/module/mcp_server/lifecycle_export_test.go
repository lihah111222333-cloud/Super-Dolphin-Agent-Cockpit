package mcpserver

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestExportMCPToolLifecyclePreservesRollbackStates(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})

	setLifecycleForExport(t, svc, SetMCPToolLifecycleRequest{
		ServerName:      "my-search",
		ManifestName:    "manifest-search",
		ToolName:        "search",
		State:           contract.MCPToolLifecycleRemoved,
		Reason:          "replaced by search_v2",
		ReplacementTool: "search_v2",
	})
	setLifecycleForExport(t, svc, SetMCPToolLifecycleRequest{
		ServerName: mcpdto.ClientKindLSP,
		ToolName:   "grep",
		State:      contract.MCPToolLifecycleSuspended,
		Reason:     "policy review",
	})

	got, err := svc.ExportMCPToolLifecycle(context.Background(), ExportMCPToolLifecycleRequest{})
	if err != nil {
		t.Fatalf("ExportMCPToolLifecycle() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ExportMCPToolLifecycle() len = %d, want 2: %#v", len(got), got)
	}
	assertExportMCPToolLifecycleDecision(t, got[0], mcpdto.ClientKindLSP, contract.MCPToolLifecycleSuspended, "", contract.MCPToolLifecycleDenyCodeSuspended)
	assertExportMCPToolLifecycleDecision(t, got[1], "my-search", contract.MCPToolLifecycleRemoved, "search_v2", contract.MCPToolLifecycleDenyCodeRemoved)
}

func setLifecycleForExport(t *testing.T, svc Service, req SetMCPToolLifecycleRequest) {
	t.Helper()
	if _, err := svc.SetMCPToolLifecycle(context.Background(), req); err != nil {
		t.Fatalf("SetMCPToolLifecycle(%s/%s) error = %v", req.ServerName, req.ToolName, err)
	}
}

func assertExportMCPToolLifecycleDecision(
	t *testing.T,
	got contract.MCPToolLifecycleDecision,
	serverName string,
	state contract.MCPToolLifecycleState,
	replacementTool string,
	denyCode string,
) {
	t.Helper()
	if got.ServerName != serverName {
		t.Fatalf("export server = %q, want %q; row=%#v", got.ServerName, serverName, got)
	}
	if got.State != state {
		t.Fatalf("export state = %q, want %q; row=%#v", got.State, state, got)
	}
	if got.ReplacementTool != replacementTool {
		t.Fatalf("export replacement = %q, want %q; row=%#v", got.ReplacementTool, replacementTool, got)
	}
	if got.DenyCode != denyCode {
		t.Fatalf("export denyCode = %q, want %q; row=%#v", got.DenyCode, denyCode, got)
	}
}
