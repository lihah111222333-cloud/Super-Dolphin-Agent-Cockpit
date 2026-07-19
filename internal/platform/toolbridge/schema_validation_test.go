package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

func TestHostToolInputSchemaRejectsUnknownFieldsBeforeHandler(t *testing.T) {
	host := &stubHostToolRegistry{
		hasToolName: testHostToolName,
		tools: []mcpdto.MCPTool{{
			Name:        testHostToolName,
			InputSchema: strictObjectSchema(t, "query"),
		}},
		result: map[string]any{"ok": true},
	}
	h := &Handler{hostTools: host}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{
		Name:      testHostToolName,
		Arguments: json.RawMessage(`{"query":"x","dryRun":true}`),
		CWD:       t.TempDir(),
	})
	if err != nil {
		t.Fatalf("callHostTool() error = %v, want structured validation result", err)
	}
	if host.calls != 0 {
		t.Fatalf("host handler calls = %d, want 0 before schema validation failure", host.calls)
	}
	if got == nil || got.Success {
		t.Fatalf("callHostTool() result = %#v, want failed validation result", got)
	}
	if !strings.Contains(got.ContentItems[0].Text, "dryRun") {
		t.Fatalf("validation result = %#v, want dryRun in error", got)
	}
}

func TestCodexSurfaceInputSchemaRejectsUnknownFieldsBeforeDispatch(t *testing.T) {
	lsp := &fakeMCPClient{tools: []mcpdto.MCPTool{{
		Name:        "grep",
		InputSchema: strictObjectSchema(t, "query"),
	}}}
	h := &Handler{stdioClientFactory: fakeClientFactory(map[string]mcpClient{"lsp": lsp})}
	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), contract.CodexToolSurfaceScope{
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/repo",
		Manifest:         testLSPManifest(),
	})
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}

	_, err = h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: json.RawMessage(`{"name":"grep","arguments":{"query":"x","dryRun":true},"_agentId":"agent-1","_threadId":"provider-thread-1","_callId":"call-1","_cwd":"/repo"}`)})
	if err == nil || !strings.Contains(err.Error(), "dryRun") {
		t.Fatalf("HandleToolCall() error = %v, want dryRun schema rejection", err)
	}
	if len(lsp.calls) != 0 {
		t.Fatalf("lsp calls = %#v, want no dispatch after schema validation failure", lsp.calls)
	}
}

func TestPeerProxyInputSchemaRejectsUnknownFieldsBeforeForwarding(t *testing.T) {
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, _ any, result any) error {
		switch method {
		case ProxyMethodToolsList:
			out, ok := result.(*peerToolsListResult)
			if !ok {
				t.Fatalf("tools/list result type = %T, want *peerToolsListResult", result)
			}
			*out = peerToolsListResult{Tools: []mcpdto.MCPTool{{
				Name:        "grep",
				InputSchema: strictObjectSchema(t, "query"),
			}}, toolsPresent: true}
			return nil
		case ProxyMethodToolsCall:
			t.Fatal("tools/call reached peer despite schema-forbidden field")
			return nil
		default:
			t.Fatalf("Callback() method = %q, want tools/list or tools/call", method)
			return nil
		}
	}}}
	h, _ := newHandlerForTest(peer)

	list := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-list","method":"tools/list"}`)
	if list.Error != nil {
		t.Fatalf("proxy tools/list error = %+v", list.Error)
	}
	got := callProxyRequest(t, h, "/mcp/lsp/agent-1", `{"jsonrpc":"2.0","id":"req-schema","method":"tools/call","params":{"name":"grep","arguments":{"query":"x","dryRun":true}}}`)
	if got.Error == nil {
		t.Fatalf("proxy response error = nil, want schema validation error")
	}
	if !strings.Contains(got.Error.Message, "dryRun") {
		t.Fatalf("proxy error = %+v, want schema error mentioning dryRun", got.Error)
	}
}

func strictObjectSchema(t *testing.T, fields ...string) json.RawMessage {
	t.Helper()
	properties := make(map[string]any, len(fields))
	for _, field := range fields {
		properties[field] = map[string]any{"type": "string"}
	}
	raw, err := json.Marshal(map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	})
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	return raw
}

func testLSPManifest() providerdto.MCPManifest {
	return providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{{Name: "lsp", Command: []string{"mcp-lsp"}}}}
}
