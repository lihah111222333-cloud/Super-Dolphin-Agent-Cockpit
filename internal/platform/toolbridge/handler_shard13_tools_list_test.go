package toolbridge

import (
	"errors"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

func TestProxyToolsList_OrchIncludesHostAndSurvivesPeerDown(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "memory_write", Description: "host memory"}}}
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
	if !ok || tool["name"] != "memory_write" {
		t.Fatalf("tool = %#v, want memory_write", tools[0])
	}
}

func TestProxyToolsList_LSPDoesNotIncludeHostMemory(t *testing.T) {
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: "memory_write", Description: "host memory"}}}
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
	if !strings.Contains(got.Error.Message, ErrAmbiguousPeer.Error()) {
		t.Fatalf("proxy tools/list error = %+v, want %v", got.Error, ErrAmbiguousPeer)
	}
}

func TestProxyToolsList_LSPFiltersPeerMemoryRead(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindLSP: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "inspect", Description: "lsp"}}, nil)},
	}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, must filter peer memory_read for non-orch proxy list", tools)
	}
	if !proxyToolsContainName(tools, "inspect") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchIncludesHostMemoryRead(t *testing.T) {
	host := NewMemoryReadHostToolRegistry(&stubAgentMemoryReader{enabled: true, toolsEnabled: true}, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true})
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: "launch_agent", Description: "peer orch"}}, nil)}}}, hostTools: host, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if !proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, want memory_read", tools)
	}
	if !proxyToolsContainName(tools, "launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchFiltersPeerMemoryReadWhenReaderUnavailable(t *testing.T) {
	h := &Handler{registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "launch_agent", Description: "peer orch"}}, nil)},
	}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, must filter peer memory_read when host reader is unavailable", tools)
	}
	if !proxyToolsContainName(tools, "launch_agent") {
		t.Fatalf("tools = %#v, want non-memory peer tool preserved", tools)
	}
}

func TestProxyToolsList_OrchFiltersPeerMemoryReadWhenMemoryReadToolsDisabled(t *testing.T) {
	reader := &stubAgentMemoryReader{enabled: true, toolsEnabled: true}
	host := NewMemoryReadHostToolRegistry(reader, MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: false})
	h := &Handler{hostTools: host, registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		dto.ClientKindOrch: {listToolsPeer([]dto.MCPTool{{Name: ToolNameMemoryRead, Description: "peer memory"}, {Name: "launch_agent", Description: "peer orch"}}, nil)},
	}}, proxyAuthToken: newProxyAuthToken()}

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", `{"jsonrpc":"2.0","id":"req-1","method":"tools/list"}`)
	if got.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", got.Error)
	}
	result := got.Result.(map[string]any)
	tools := result["tools"].([]any)
	if proxyToolsContainName(tools, ToolNameMemoryRead) {
		t.Fatalf("tools = %#v, must filter peer memory_read when memory_read tools disabled", tools)
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
