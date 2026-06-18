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

func TestPrepareCodexToolSurfaceNamespacesExternalDuplicateToolName(t *testing.T) {
	postgres := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "query", Description: "postgres query", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	sqlite := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "query", Description: "sqlite query", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"postgres": postgres, "sqlite": sqlite})}

	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: "postgres", Command: []string{"mcp-server-postgres"}},
			{Name: "sqlite", Command: []string{"npx", "-y", "@bytebase/dbhub", "--dsn=sqlite:///tmp/app.db"}},
		}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"query", "mcp__sqlite__query"})

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"query","arguments":{"sql":"select 1"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(query) error = %v", err)
	}
	if !postgres.calledWith("query") {
		t.Fatalf("postgres calls = %#v, want query", postgres.calls)
	}

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"mcp__sqlite__query","arguments":{"sql":"select 1"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-2","_cwd":"/repo"}`)})
	if err != nil {
		t.Fatalf("HandleToolCall(mcp__sqlite__query) error = %v", err)
	}
	if !sqlite.calledWith("query") {
		t.Fatalf("sqlite calls = %#v, want query", sqlite.calls)
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
