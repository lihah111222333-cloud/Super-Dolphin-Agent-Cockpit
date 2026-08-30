package toolbridge

import (
	"errors"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

func TestProxyToolsList_OrchIncludesHostAndSurvivesPeerDown(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: ToolNameRetiredHostWrite, Description: "retired host write"}}}
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindOrch: {listToolsPeer(nil, errors.New("orch down"))},
		}},
		hostTools:      host,
		proxyAuthToken: newProxyAuthToken(),
	}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v, want result", got.Error)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one host tool", result["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["name"] != ToolNameRetiredHostWrite {
		t.Fatalf("tool = %#v, want %s", tools[0], ToolNameRetiredHostWrite)
	}
}

func TestProxyToolsList_LSPDoesNotIncludeHostMemory(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: ToolNameRetiredHostWrite, Description: "retired host write"}}}
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindLSP: {listToolsPeer([]dto.MCPTool{{Name: "inspect", Description: "lsp"}}, nil)},
		}},
		hostTools:      host,
		proxyAuthToken: newProxyAuthToken(),
	}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want object", got.Result)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one lsp tool", result["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "inspect" {
		t.Fatalf("tool = %#v, want inspect", tool)
	}
}

func TestProxyToolsList_MultiplePeersAmbiguousLikeToolsCall(t *testing.T) {
	h := &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindLSP: {
				listToolsPeer([]dto.MCPTool{{Name: "inspect", Description: "first"}}, nil),
				listToolsPeer([]dto.MCPTool{{Name: "xref", Description: "second"}}, nil),
			},
		}},
		proxyAuthToken: newProxyAuthToken(),
	}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error == nil {
		t.Fatalf("proxy tools/list error = nil, want %v parity with tools/call", ErrAmbiguousPeer)
	}
	if got.Error.Code != jsonRPCCodeInternal {
		t.Fatalf("proxy tools/list error code = %d, want %d", got.Error.Code, jsonRPCCodeInternal)
	}
	assertPublicToolErrorPayload(t, got.Error.Message, ErrAmbiguousPeer.Error())
}

func TestProxyToolsList_LSPFiltersPeerRetiredHostRead(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindLSP: {listToolsPeer([]dto.MCPTool{{Name: ToolNameRetiredHostRead, Description: "peer retired host"}, {Name: "inspect", Description: "lsp"}}, nil)},
	}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameRetiredHostRead) {
		t.Fatalf("tools = %#v, must filter retired host read for non-orch proxy list", tools)
	}
	if !proxyToolsContainName(tools, "inspect") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchIncludesHostRetiredRead(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: ToolNameRetiredHostRead,
		tools:       []dto.MCPTool{{Name: ToolNameRetiredHostRead, Description: "retired host read"}},
	}
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "launch_agent", Description: "peer orch"}}, nil)}}}, hostTools: host, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if !proxyToolsContainName(tools, ToolNameRetiredHostRead) {
		t.Fatalf("tools = %#v, want %s", tools, ToolNameRetiredHostRead)
	}
	if !proxyToolsContainName(tools, "launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchFiltersPeerRetiredReadWhenHostUnavailable(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameRetiredHostRead, Description: "peer retired host"}, {Name: "launch_agent", Description: "peer orch"}}, nil)},
	}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameRetiredHostRead) {
		t.Fatalf("tools = %#v, must filter retired host read when host is unavailable", tools)
	}
	if !proxyToolsContainName(tools, "launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchFiltersPeerRetiredReadWhenHostHidden(t *testing.T) {
	// The retired name remains reserved even when no host schema is published;
	// it must never fall through to an MCP peer.
	host := &stubHostToolRegistry{hasToolName: ToolNameRetiredHostRead}
	h := &Handler{hostTools: host, registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameRetiredHostRead, Description: "peer retired host"}, {Name: "launch_agent", Description: "peer orch"}}, nil)},
	}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameRetiredHostRead) {
		t.Fatalf("tools = %#v, must filter retired host read when host tool is hidden", tools)
	}
	if !proxyToolsContainName(tools, "launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func proxyToolsContainName(tools []any, name string) bool {
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if ok && tool["name"] == name {
			return true
		}
	}
	return false
}
