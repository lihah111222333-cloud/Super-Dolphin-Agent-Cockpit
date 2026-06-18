package toolbridge

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestPrepareCodexToolSurfaceAdvertisesBatchReportShortName(t *testing.T) {
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{
		Name:        "orchestration_get_agent_reports",
		Description: "batch reports",
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
	assertDynamicToolNames(t, tools, []string{"get_agent_reports"})
	assertNoLegacyBatchReportTool(t, tools)

	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(
		`{"name":"get_agent_reports","arguments":{"agent_ids":["agent-a","agent-b"]},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`,
	)})
	if err != nil {
		t.Fatalf("HandleToolCall(get_agent_reports) error = %v", err)
	}
	if result == nil {
		t.Fatal("HandleToolCall(get_agent_reports) result = nil")
	}
	if !orch.calledWith("orchestration_get_agent_reports") {
		t.Fatalf("orch calls = %#v, want orchestration_get_agent_reports", orch.calls)
	}
}

func assertNoLegacyBatchReportTool(t *testing.T, tools []contract.DynamicToolSchema) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == "orchestration_get_agent_reports" {
			t.Fatalf("dynamic tools advertised legacy alias %q", tool.Name)
		}
	}
}
