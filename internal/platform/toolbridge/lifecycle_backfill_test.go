package toolbridge

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

func TestPrepareCodexToolSurfaceBackfillsDiscoveredMCPTools(t *testing.T) {
	root := t.TempDir()
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep"}}}
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "orchestration_launch_agent"}}}
	backfiller := &recordingMCPToolLifecycleBackfiller{}
	h := &Handler{
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp, "orch": orch}),
		lifecycle:          backfiller,
	}

	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
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

	assertMCPToolLifecycleBackfill(t, backfiller.requests, root, mcpdto.ClientKindLSP, "grep")
	assertMCPToolLifecycleBackfill(t, backfiller.requests, root, mcpdto.ClientKindOrch, "orchestration_launch_agent")
}

func TestListToolsForCodexBackfillsPeerDiscovery(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	backfiller := &recordingMCPToolLifecycleBackfiller{}
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindOrch: {listToolsPeer([]mcpdto.MCPTool{{Name: "orchestration_launch_agent"}}, nil)},
			mcpdto.ClientKindLSP:  {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		lifecycle: backfiller,
	}

	if _, err := h.ListToolsForCodex(context.Background()); err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}

	assertMCPToolLifecycleBackfill(t, backfiller.requests, root, mcpdto.ClientKindLSP, "grep")
	assertMCPToolLifecycleBackfill(t, backfiller.requests, root, mcpdto.ClientKindOrch, "orchestration_launch_agent")
}

func TestProxyToolsListBackfillsPeerDiscoveryFromBindingCWD(t *testing.T) {
	root := t.TempDir()
	backfiller := &recordingMCPToolLifecycleBackfiller{}
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			mcpdto.ClientKindLSP: {listToolsPeer([]mcpdto.MCPTool{{Name: "grep"}}, nil)},
		}},
		bindingStore: &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
			"agent-1": {AgentID: "agent-1", CWD: root},
		}},
		lifecycle:      backfiller,
		proxyAuthToken: newProxyAuthToken(),
	}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}

	assertMCPToolLifecycleBackfill(t, backfiller.requests, root, mcpdto.ClientKindLSP, "grep")
}

type recordingMCPToolLifecycleBackfiller struct {
	requests []MCPToolLifecycleBackfillRequest
	err      error
}

func (b *recordingMCPToolLifecycleBackfiller) BackfillMCPTools(_ context.Context, req MCPToolLifecycleBackfillRequest) error {
	cloned := req
	cloned.Tools = append([]contract.MCPToolLifecycleObservedTool(nil), req.Tools...)
	b.requests = append(b.requests, cloned)
	return b.err
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
