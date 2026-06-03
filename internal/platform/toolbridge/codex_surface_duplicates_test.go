package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestPrepareCodexToolSurfaceFailsOnNonReservedDuplicateToolName(t *testing.T) {
	first := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", Description: "first", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	second := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "grep", Description: "second", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp-a": first, "lsp-b": second})}

	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID: "agent-1",
		CWD:     "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: "lsp-a", Command: []string{"mcp-lsp-a"}},
			{Name: "lsp-b", Command: []string{"mcp-lsp-b"}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `duplicate codex surface tool "grep"`) {
		t.Fatalf("PrepareCodexToolSurface() error = %v, want duplicate grep failure", err)
	}
}

func TestPrepareCodexToolSurfaceFailsOnNonReservedAliasConflict(t *testing.T) {
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{
		{Name: "foo", Description: "first", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__lsp__foo", Description: "alias collision", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp})}

	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:  "agent-1",
		CWD:      "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), `codex surface alias "mcp__lsp__foo"`) {
		t.Fatalf("PrepareCodexToolSurface() error = %v, want alias conflict failure", err)
	}
}
