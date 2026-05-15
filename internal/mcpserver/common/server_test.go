package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type testToolProvider struct{}

func (testToolProvider) ListTools(context.Context) ([]MCPTool, error) {
	return []MCPTool{{Name: "demo_tool", InputSchema: json.RawMessage(`{}`)}}, nil
}

func (testToolProvider) CallTool(context.Context, string, json.RawMessage) (any, error) {
	return map[string]any{"ok": true}, nil
}

type captureToolProvider struct {
	call func(context.Context, string, json.RawMessage) (any, error)
}

func (p captureToolProvider) ListTools(context.Context) ([]MCPTool, error) {
	return nil, nil
}

func (p captureToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	return p.call(ctx, name, args)
}

func TestServerHandlesToolsList(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"tools":[{"name":"demo_tool"`)) {
		t.Fatalf("Run() output = %s", output.String())
	}
}

func TestServerHandlesToolsCall(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"content":[{"type":"text","text":"{\"ok\":true}"`)) {
		t.Fatalf("Run() output = %s", output.String())
	}
}

func TestToolsCallReturnsStructuredToolError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"demo_tool","arguments":{"bad":true}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("decode params: json: unknown field \"bad\"")
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, output.Bytes())
	if envelope.Success {
		t.Fatalf("envelope success = true, want false")
	}
	if envelope.Code != "schema_invalid" {
		t.Fatalf("envelope code = %q, want schema_invalid; output=%s", envelope.Code, output.String())
	}
	if envelope.Error == "" || envelope.Hint == "" {
		t.Fatalf("envelope missing error/hint: %#v", envelope)
	}
}

func TestTimeoutErrorIsStructuredRetryable(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"slow_tool","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, context.DeadlineExceeded
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, output.Bytes())
	if envelope.Code != "lsp_timeout" {
		t.Fatalf("envelope code = %q, want lsp_timeout; output=%s", envelope.Code, output.String())
	}
	if !envelope.Retryable {
		t.Fatalf("envelope retryable = false, want true")
	}
}

func TestRecoveryErrorIsStructured(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"panic_tool","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		panic("boom")
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, output.Bytes())
	if envelope.Code != "internal_panic" {
		t.Fatalf("envelope code = %q, want internal_panic; output=%s", envelope.Code, output.String())
	}
	if envelope.Retryable {
		t.Fatalf("envelope retryable = true, want false")
	}
	if !strings.Contains(envelope.Error, "panic") {
		t.Fatalf("envelope error = %q, want panic context", envelope.Error)
	}
}

func TestToolCallParamsScopeNormalizesKnownMCPFamilies(t *testing.T) {
	params := ToolCallParams{}
	cases := map[string]string{
		"mcp-lsp":  "lsp",
		"MCP-ORCH": "orch",
		"mcp-ida":  "ida",
		"custom":   "custom",
	}
	for input, want := range cases {
		if got := params.Scope(input).Family; got != want {
			t.Fatalf("Scope(%q).Family = %q, want %q", input, got, want)
		}
	}
}

func decodeToolErrorEnvelopeFromOutput(t *testing.T, raw []byte) ToolErrorEnvelope {
	t.Helper()
	var resp struct {
		Error  *jsonRPCError `json:"error,omitempty"`
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &resp); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	if resp.Error != nil {
		t.Fatalf("response has JSON-RPC error %#v, want structured tool result; raw=%s", resp.Error, raw)
	}
	if len(resp.Result.StructuredContent) == 0 {
		t.Fatalf("response missing structuredContent; raw=%s", raw)
	}
	var envelope ToolErrorEnvelope
	if err := json.Unmarshal(resp.Result.StructuredContent, &envelope); err != nil {
		t.Fatalf("unmarshal structuredContent %s: %v", resp.Result.StructuredContent, err)
	}
	return envelope
}

func TestServerToolsCallUsesTrustedTopLevelScope(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"demo_tool","arguments":{"agent_id":"evil","cwd":"/evil"},"_agentId":"trusted-agent","_threadId":"trusted-thread","_callId":"trusted-call","_cwd":"/trusted/root"}}`)
	var output bytes.Buffer
	called := false
	provider := trustedTopLevelScopeProvider(t, &called)

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !called {
		t.Fatal("CallTool() was not called")
	}
	if !bytes.Contains(output.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("Run() output = %s", output.String())
	}
}

func trustedTopLevelScopeProvider(t *testing.T, called *bool) captureToolProvider {
	t.Helper()
	return captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		*called = true
		if name != "demo_tool" {
			t.Fatalf("CallTool() name = %q, want demo_tool", name)
		}
		scope, ok := ToolScopeFromContext(ctx)
		if !ok {
			t.Fatal("ToolScopeFromContext() missing trusted scope")
		}
		if scope.AgentID != "trusted-agent" || scope.ThreadID != "trusted-thread" || scope.CallID != "trusted-call" {
			t.Fatalf("scope identity = %#v, want trusted top-level metadata", scope)
		}
		if scope.CWD != "/trusted/root" {
			t.Fatalf("scope cwd = %q, want /trusted/root", scope.CWD)
		}
		if got := WorkspaceRootFromContext(ctx, "/fallback"); got != "/trusted/root" {
			t.Fatalf("WorkspaceRootFromContext() = %q, want /trusted/root", got)
		}
		if !bytes.Contains(args, []byte(`"agent_id":"evil"`)) || !bytes.Contains(args, []byte(`"cwd":"/evil"`)) {
			t.Fatalf("CallTool() arguments = %s, want original untrusted payload preserved as args only", args)
		}
		return map[string]any{"ok": true}, nil
	}}
}

func TestHTTPDirectPeerRequiresTrustedScopeMetadata(t *testing.T) {
	called := false
	provider := captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		called = true
		scope, ok := ToolScopeFromContext(ctx)
		if !ok {
			t.Fatal("ToolScopeFromContext() missing trusted metadata for direct HTTP peer")
		}
		if scope.AgentID != "agent-http" || scope.ThreadID != "thread-http" || scope.CallID != "call-http" {
			t.Fatalf("scope = %#v, want trusted direct HTTP metadata", scope)
		}
		if scope.CWD != "/trusted/http/root" {
			t.Fatalf("scope.CWD = %q, want /trusted/http/root", scope.CWD)
		}
		return map[string]any{"isolation_pass": true}, nil
	}}
	server := NewHTTPServer("mcp-lsp", "dev", provider)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"lsp_file","arguments":{"cwd":"/forged"},"_agentId":"agent-http","_threadId":"thread-http","_callId":"call-http","_cwd":"/trusted/http/root"}}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("CallTool() was not called")
	}
	if !strings.Contains(rec.Body.String(), `"isolation_pass":true`) {
		t.Fatalf("HTTP body = %s, want isolation pass payload", rec.Body.String())
	}
}

func TestHTTPDirectPeerWithoutTrustedMetadataIsNotMultiAgentIsolationPass(t *testing.T) {
	provider := captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		if scope, ok := ToolScopeFromContext(ctx); ok {
			if scope.AgentID != "" || scope.ThreadID != "" || scope.CallID != "" || scope.CWD != "" {
				t.Fatalf("ToolScopeFromContext() = %#v, want family-only scope without trusted identity/cwd", scope)
			}
		}
		return map[string]any{"isolation_pass": false, "reason": "scope_missing"}, nil
	}}
	server := NewHTTPServer("mcp-lsp", "dev", provider)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"lsp_file","arguments":{"agent_id":"forged-agent","cwd":"/forged"}}}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"isolation_pass":false`) || !strings.Contains(rec.Body.String(), `"scope_missing"`) {
		t.Fatalf("HTTP body = %s, want explicit non-isolation result", rec.Body.String())
	}
}

func TestRawJSONRPCInvalidRequestReturnsErrorAndContinues(t *testing.T) {
	input := bytes.NewBufferString(strings.Join([]string{
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		``,
	}, "\n"))
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"code":-32600`)) {
		t.Fatalf("Run() output = %s, want invalid request error", output.String())
	}
	if !bytes.Contains(output.Bytes(), []byte(`"id":2`)) || !bytes.Contains(output.Bytes(), []byte(`"result":{}`)) {
		t.Fatalf("Run() output = %s, want later valid ping response", output.String())
	}
}

func TestRawJSONStreamSyntaxErrorClosesConnection(t *testing.T) {
	input := bytes.NewBufferString(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		``,
	}, "\n"))
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want raw syntax/desync to close connection")
	}
	if bytes.Contains(output.Bytes(), []byte(`"id":2`)) {
		t.Fatalf("Run() output = %s, want no response after raw syntax error", output.String())
	}
}

func TestFramedStdioMalformedFrameStopsConnection(t *testing.T) {
	var input bytes.Buffer
	input.WriteString(framedPayload(`{"jsonrpc":"2.0","id":1,"method":`))
	input.WriteString(framedPayload(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(&input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want malformed framed JSON to stop connection")
	}
	if bytes.Contains(output.Bytes(), []byte(`"id":2`)) {
		t.Fatalf("Run() output = %s, want no response after malformed frame", output.String())
	}
}

func TestRollbackKeepsHTTPMCPCompatibility(t *testing.T) {
	server := NewHTTPServer("mcp-lsp", "dev", testToolProvider{})
	addr, err := server.Start(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	resp, err := http.Post("http://"+addr+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("POST /mcp error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /mcp status = %d body=%s, want 200", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(`"demo_tool"`)) {
		t.Fatalf("POST /mcp body = %s, want tools/list response", body)
	}
}

func framedPayload(payload string) string {
	return "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + payload
}
