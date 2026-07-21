package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestPrepareCodexToolSurfaceNamespacesExternalDuplicateToolName(t *testing.T) {
	playwright := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "query", Description: "browser query", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	sqlite := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "query", Description: "sqlite query", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"playwright": playwright, "sqlite": sqlite})}

	tools, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: "playwright", Command: []string{"npx", "@playwright/mcp@latest"}},
			{Name: "sqlite", Command: []string{"npx", "-y", "@bytebase/dbhub@0.23.0", "--dsn=sqlite:///tmp/app.db"}},
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
	if !playwright.calledWith("query") {
		t.Fatalf("playwright calls = %#v, want query", playwright.calls)
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
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{
		{Name: "foo", Description: "first", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__orch__foo", Description: "alias collision", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindOrch: orch})}

	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:  "agent-1",
		CWD:      "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: mcpdto.ClientKindOrch, Command: []string{"mcp-orch"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), `codex surface alias "mcp__orch__foo"`) {
		t.Fatalf("PrepareCodexToolSurface() error = %v, want alias conflict failure", err)
	}
}
