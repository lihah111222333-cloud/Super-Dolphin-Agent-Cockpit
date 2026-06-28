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

func TestPrepareCodexToolSurfaceAdvertisesBatchReportShortName(t *testing.T) {
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{
		Name:        "orchestration_get_agent_reports",
		Description: "batch reports",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}}
	h := &Handler{
		toolLifecycle:      fakeActiveLifecycleReader("/repo", map[string][]string{"orch": {"orchestration_get_agent_reports"}}),
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"orch": orch}),
	}

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

func TestPrepareCodexToolSurfaceAdvertisesThirdVersionOrchestrationShortNames(t *testing.T) {
	legacyNames := []string{
		"orchestration_launch_agent",
		"orchestration_send_message",
		"orchestration_get_agent_reports",
		"orchestration_interrupt_agent",
		"orchestration_recover_agent",
		"orchestration_stop_agent",
	}
	orchTools := make([]mcpdto.MCPTool, 0, len(legacyNames))
	for _, name := range legacyNames {
		orchTools = append(orchTools, mcpdto.MCPTool{
			Name:        name,
			Description: name,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		})
	}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{
		"orch": &fakeMCPClient{tools: orchTools},
	})}

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

	assertDynamicToolNames(t, tools, []string{
		"launch_agent",
		"send_message",
		"get_agent_reports",
		"interrupt_agent",
		"recover_agent",
		"stop_agent",
	})
	assertNoLegacyOrchestrationTools(t, tools)
}

func assertNoLegacyOrchestrationTools(t *testing.T, tools []contract.DynamicToolSchema) {
	t.Helper()
	for _, tool := range tools {
		if strings.HasPrefix(tool.Name, "orchestration_") {
			t.Fatalf("dynamic tools advertised legacy alias %q", tool.Name)
		}
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
