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

func TestCodexSurfaceOrchReportRegistryMapsAllShortNamesAndRejectsLegacyAliases(t *testing.T) {
	h, orch, tools := prepareOrchReportSurfaceFromLegacyPeerNames(t)
	assertAllOrchReportShortNamesAdvertised(t, tools)
	for _, alias := range orchestrationToolAliasesForTest(t) {
		assertOrchReportShortCallRoutesToLegacyPeerRealName(t, h, orch, alias)
		assertOrchReportLegacyCallsRejectedBeforePeer(t, h, orch, alias)
	}
}

func prepareOrchReportSurfaceFromLegacyPeerNames(t *testing.T) (*Handler, *fakeMCPClient, []contract.DynamicToolSchema) {
	t.Helper()
	orch := &fakeMCPClient{tools: orchestrationLegacyPeerToolsForTest(t)}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{mcpdto.ClientKindOrch: orch})}
	tools, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{
			Name:    mcpdto.ClientKindOrch,
			Command: []string{"mcp-orch"},
		}}},
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	return h, orch, tools
}

func assertAllOrchReportShortNamesAdvertised(t *testing.T, tools []contract.DynamicToolSchema) {
	t.Helper()
	for _, canonical := range contract.OrchestrationToolCanonicalNames() {
		assertDynamicToolNames(t, tools, []string{canonical})
	}
}

func assertOrchReportShortCallRoutesToLegacyPeerRealName(t *testing.T, h *Handler, orch *fakeMCPClient, alias contract.OrchestrationToolAlias) {
	t.Helper()
	before := len(orch.calls)
	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: orchestrationToolCallParamsForTest(alias.Canonical),
	})
	if err != nil {
		t.Fatalf("HandleToolCall(%q) error = %v", alias.Canonical, err)
	}
	if result == nil {
		t.Fatalf("HandleToolCall(%q) result = nil", alias.Canonical)
	}
	if got := orch.calls[before:]; len(got) != 1 || got[0] != alias.LegacyPeerRealName {
		t.Fatalf("orch calls after %q = %#v, want one peer realName %q", alias.Canonical, got, alias.LegacyPeerRealName)
	}
}

func assertOrchReportLegacyCallsRejectedBeforePeer(t *testing.T, h *Handler, orch *fakeMCPClient, alias contract.OrchestrationToolAlias) {
	t.Helper()
	for _, legacyCall := range []string{alias.LegacyPeerRealName, wrappedMCPToolName(mcpdto.ClientKindOrch, alias.LegacyPeerRealName)} {
		before := len(orch.calls)
		_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
			Params: orchestrationToolCallParamsForTest(legacyCall),
		})
		if err == nil {
			t.Fatalf("HandleToolCall(%q) error = nil, want legacy orchestration alias rejected", legacyCall)
		}
		if !strings.Contains(err.Error(), "unknown codex surface tool") {
			t.Fatalf("HandleToolCall(%q) error = %v, want unknown codex surface tool", legacyCall, err)
		}
		if got := orch.calls[before:]; len(got) != 0 {
			t.Fatalf("legacy call %q reached orchestration peer: new calls=%#v", legacyCall, got)
		}
	}
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
