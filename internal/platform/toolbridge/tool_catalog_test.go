package toolbridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

func TestListToolCatalogReturnsCanonicalWorkspaceTools(t *testing.T) {
	root := t.TempDir()
	owner := newFakeMCPToolLifecycleOwner()
	h := &Handler{
		hostTools: &stubHostToolRegistry{tools: []mcpdto.MCPTool{{
			Name: "host_echo", Description: "Host echo",
		}}},
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent", Description: "Launch"}}, nil)},
			mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{
				{Name: "edit", Description: "Edit source"},
				{Name: "completion", Description: "Complete source"},
				{Name: "grep", Description: "Search source"},
			}, nil)},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
	}

	got, err := h.ListToolCatalog(context.Background(), root)
	if err != nil {
		t.Fatalf("ListToolCatalog() error = %v", err)
	}
	assertToolCatalogEntry(t, got, "host", "host_echo", "Host echo")
	assertToolCatalogEntry(t, got, mcpdto.ClientKindLSP, "lsp_edit", "Edit source")
	assertToolCatalogEntry(t, got, mcpdto.ClientKindLSP, "lsp_completion", "Complete source")
	assertToolCatalogEntry(t, got, mcpdto.ClientKindLSP, "grep", "Search source")
	assertToolCatalogEntry(t, got, mcpdto.ClientKindOrch, "launch_agent", "Launch")
	if got[0].ServerName != "host" || got[1].ServerName != mcpdto.ClientKindOrch || got[2].ToolName != "lsp_edit" {
		t.Fatalf("catalog order = %+v, want host, orchestration, then canonical LSP tools", got)
	}
}

func TestListToolCatalogFailsClosedWhenPeerDiscoveryFails(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		mcpdto.ClientKindLSP:  {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
	}}}

	got, err := h.ListToolCatalog(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "orch down") || len(got) != 0 {
		t.Fatalf("tools=%+v error=%v, want fail-closed orch error", got, err)
	}
}

func TestListToolCatalogFiltersDisabledWorkspaceTool(t *testing.T) {
	root := t.TempDir()
	owner := newFakeMCPToolLifecycleOwner()
	owner.setDecision(root, mcpdto.ClientKindLSP, "grep", contract.MCPToolLifecycleDisabled, "disabled by user")
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent"}}, nil)},
			mcpdto.ClientKindLSP:  {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
	}

	got, err := h.ListToolCatalog(context.Background(), root)
	if err != nil {
		t.Fatalf("ListToolCatalog() error = %v", err)
	}
	for _, item := range got {
		if item.ToolName == "grep" {
			t.Fatalf("disabled grep leaked into catalog: %+v", got)
		}
	}
	assertToolCatalogEntry(t, got, mcpdto.ClientKindOrch, "launch_agent", "")
}

func TestListToolCatalogRejectsSamePeerCanonicalCollision(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent"}}, nil)},
		mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{
			{Name: "edit"},
			{Name: "lsp_edit"},
		}, nil)},
	}}}

	got, err := h.ListToolCatalog(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "canonical name") || len(got) != 0 {
		t.Fatalf("tools=%+v error=%v, want canonical collision", got, err)
	}
}

func assertToolCatalogEntry(t *testing.T, items []ToolCatalogEntry, serverName, toolName, description string) {
	t.Helper()
	for _, item := range items {
		if item.ServerName == serverName && item.ToolName == toolName {
			if item.DisplayName != toolName || item.Description != description || !item.Enabled || item.DisabledReason != "" {
				t.Fatalf("catalog entry = %+v", item)
			}
			return
		}
	}
	t.Fatalf("missing %s/%s in %+v", serverName, toolName, items)
}
