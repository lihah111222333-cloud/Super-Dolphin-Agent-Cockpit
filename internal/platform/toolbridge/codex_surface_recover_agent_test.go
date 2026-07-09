package toolbridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestCodexSurfaceExposesRecoverAgentShortName(t *testing.T) {
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{
		Name:        "recover_agent",
		Description: "recover agent",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"orch": orch})}

	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name:    "orch",
			Command: []string{"mcp-orch"},
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	assertDynamicToolNames(t, tools, []string{"recover_agent"})
	assertNoLegacyRecoverAgentTool(t, tools)

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(
		`{"name":"recover_agent","arguments":{"agent_id":"agent-a"},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`,
	)})
	if err != nil {
		t.Fatalf("HandleToolCall(recover_agent) error = %v", err)
	}
	if result == nil {
		t.Fatal("HandleToolCall(recover_agent) result = nil")
	}
	if !orch.calledWith("recover_agent") {
		t.Fatalf("orch calls = %#v, want recover_agent", orch.calls)
	}
}

func assertNoLegacyRecoverAgentTool(t *testing.T, tools []contract.DynamicToolSchema) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == "orchestration_recover_agent" {
			t.Fatalf("dynamic tools advertised legacy alias %q", tool.Name)
		}
	}
}
