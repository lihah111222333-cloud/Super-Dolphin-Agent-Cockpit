package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/stretchr/testify/require"
)

func TestCodexToolSurfaceDeniesDisabledManagedMCPDirectCalls(t *testing.T) {
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "lsp_grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	orch := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "orchestration_launch_agent", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{
		toolLifecycleReader: fakeMCPToolLifecycleReaderWithRecords(
			"/repo",
			map[string]map[string]contract.MCPToolLifecycleState{
				"lsp":  {"lsp_grep": contract.MCPToolLifecycleStateSuspended},
				"orch": {"orchestration_launch_agent": contract.MCPToolLifecycleStateRemoved},
			},
		),
		stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp, "orch": orch}),
	}
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest: providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{
			{Name: "lsp", Command: []string{"mcp-lsp"}},
			{Name: "orch", Command: []string{"mcp-orch"}},
		}},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		wantState string
		wantTool  string
	}{
		{name: "grep", wantState: "suspended", wantTool: "lsp_grep"},
		{name: "lsp_grep", wantState: "suspended", wantTool: "lsp_grep"},
		{name: "mcp__lsp__lsp_grep", wantState: "suspended", wantTool: "lsp_grep"},
		{name: "mcp__lsp__grep", wantState: "suspended", wantTool: "lsp_grep"},
		{name: "launch_agent", wantState: "removed", wantTool: "orchestration_launch_agent"},
		{name: "orchestration_launch_agent", wantState: "removed", wantTool: "orchestration_launch_agent"},
		{name: "mcp__orch__orchestration_launch_agent", wantState: "removed", wantTool: "orchestration_launch_agent"},
		{name: "mcp__orch__launch_agent", wantState: "removed", wantTool: "orchestration_launch_agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
				Params: legacyScopedToolCallParams(tt.name),
			})
			require.ErrorContains(t, err, tt.wantState)
			require.ErrorContains(t, err, tt.wantTool)
		})
	}
	require.Empty(t, lsp.calls, "disabled LSP tool must not reach the MCP client")
	require.Empty(t, orch.calls, "removed orchestration tool must not reach the MCP client")
}

func TestCodexToolSurfaceDeniesManagedMCPWhenLifecycleReaderMissing(t *testing.T) {
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "lsp_grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp})}
	prepareLifecycleTestSurface(t, h)

	_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`),
	})

	require.ErrorContains(t, err, "MCP tool lifecycle reader is not configured")
	require.Empty(t, lsp.calls, "direct call must not reach the MCP client when lifecycle cannot be checked")
}

func TestCodexToolSurfaceDeniesMissingUnknownAndReaderErrorLifecycle(t *testing.T) {
	tests := []struct {
		name    string
		reader  *fakeMCPToolLifecycleReader
		wantErr string
	}{
		{
			name:    "missing row",
			reader:  &fakeMCPToolLifecycleReader{records: map[contract.MCPToolLifecycleKey]contract.MCPToolLifecycleRecord{}},
			wantErr: "lifecycle state is missing",
		},
		{
			name: "unknown state",
			reader: fakeMCPToolLifecycleReaderWithRecords(
				"/repo",
				map[string]map[string]contract.MCPToolLifecycleState{
					"lsp": {"lsp_grep": contract.MCPToolLifecycleState("paused")},
				},
			),
			wantErr: "unknown",
		},
		{
			name:    "reader error",
			reader:  &fakeMCPToolLifecycleReader{err: errors.New("boom")},
			wantErr: "read MCP tool",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{Name: "lsp_grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
			h := &Handler{
				toolLifecycleReader: tt.reader,
				stdioClientFactory:  fakeClientFactory(map[string]mcpClient{"lsp": lsp}),
			}
			prepareLifecycleTestSurface(t, h)

			_, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
				Params: json.RawMessage(`{"name":"grep","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`),
			})

			require.ErrorContains(t, err, tt.wantErr)
			require.Empty(t, lsp.calls, "direct call must not reach the MCP client on lifecycle failure")
		})
	}
}

func TestCodexToolSurfaceLifecycleDoesNotBlockHostDirectOrSkillTools(t *testing.T) {
	host := &fakeHostToolRegistry{tools: []mcpdto.MCPTool{{
		Name:        "host_ping",
		Description: "host ping",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}}
	skillTools := &fakeSkillToolProvider{
		tools: []contract.SkillToolSurfaceTool{{
			Name:        "backend",
			Description: "Return backend skill details",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		}},
		content: "skill ok",
	}
	h := &Handler{
		hostTools:           host,
		skillTools:          skillTools,
		toolLifecycleReader: &fakeMCPToolLifecycleReader{err: errors.New("lifecycle should not block host or skill tools")},
		stdioClientFactory:  fakeClientFactory(map[string]mcpClient{"lsp": &fakeMCPClient{}}),
	}
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	require.NoError(t, err)

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: json.RawMessage(`{"name":"host_ping","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`),
	})
	require.NoError(t, err)
	require.Len(t, host.calls, 1)

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		Params: json.RawMessage(`{"name":"backend","arguments":{},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-2","_cwd":"/repo"}`),
	})
	require.NoError(t, err)
	require.Len(t, skillTools.calls, 1)
}

func prepareLifecycleTestSurface(t *testing.T, h *Handler) {
	t.Helper()
	_, err := h.PrepareCodexToolSurface(context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}},
	})
	require.NoError(t, err)
}
