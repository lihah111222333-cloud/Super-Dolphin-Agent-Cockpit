package toolbridge

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

func TestPrepareCodexToolSurfaceBackfillsDiscoveredMCPTools(t *testing.T) {
	root := t.TempDir()
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep"}}}
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "launch_agent"}}}
	owner := newFakeMCPToolLifecycleOwner()
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp, "orch": orch}),
		lifecycle:          owner,
		lifecyclePolicy:    owner,
	}

	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              root,
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: "lsp", Command: []string{"mcp-lsp"}},
			{Name: "orch", Command: []string{"mcp-orch"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	assertMCPToolLifecycleBackfill(t, owner.backfills, root, mcpdto.ClientKindLSP, "grep")
	assertMCPToolLifecycleBackfill(t, owner.backfills, root, mcpdto.ClientKindOrch, "launch_agent")
}

func TestListToolsForCodexBackfillsPeerDiscovery(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	owner := newFakeMCPToolLifecycleOwner()
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "launch_agent"}}, nil)},
			mcpdto.ClientKindLSP:  {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
	}

	if _, err := h.ListToolsForCodex(context.Background()); err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}

	assertMCPToolLifecycleBackfill(t, owner.backfills, root, mcpdto.ClientKindLSP, "grep")
	assertMCPToolLifecycleBackfill(t, owner.backfills, root, mcpdto.ClientKindOrch, "launch_agent")
}

func TestProxyToolsListBackfillsPeerDiscoveryFromBindingCWD(t *testing.T) {
	root := t.TempDir()
	owner := newFakeMCPToolLifecycleOwner()
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		bindingStore: &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
			"agent-1": {AgentID: "agent-1", CWD: root},
		}},
		lifecycle:       owner,
		lifecyclePolicy: owner,
		proxyAuthToken:  newProxyAuthToken(),
	}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}

	assertMCPToolLifecycleBackfill(t, owner.backfills, root, mcpdto.ClientKindLSP, "grep")
}

func assertMCPToolLifecycleBackfill(t *testing.T, requests []MCPToolLifecycleBackfillRequest, workspaceRoot, serverName, toolName string) {
	t.Helper()
	for _, req := range requests {
		if req.WorkspaceRoot != workspaceRoot || req.ServerName != serverName || req.ManifestName != serverName {
			continue
		}
		for _, tool := range req.Tools {
			if tool.ManifestName == serverName && tool.Name == toolName {
				return
			}
		}
	}
	t.Fatalf("backfill requests = %#v, want %s/%s in %s", requests, serverName, toolName, workspaceRoot)
}
