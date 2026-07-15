package common

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

type badSchemaToolProvider struct{}

func (badSchemaToolProvider) ListTools(context.Context) ([]MCPTool, error) {
	return []MCPTool{{Name: "bad_schema", InputSchema: json.RawMessage(`"not-object"`)}}, nil
}

func (badSchemaToolProvider) CallTool(context.Context, string, json.RawMessage) (any, error) {
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

func TestServerToolsListNilProviderReturnsInternalError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), nil)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertJSONRPCError(t, output.Bytes(), codeInternal, "tool provider unavailable")
}

func TestServerToolsListRejectsInvalidToolSchema(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), badSchemaToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Error *jsonRPCError `json:"error,omitempty"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if resp.Error == nil {
		t.Fatalf("tools/list error = nil, want invalid schema rejection; raw=%s", output.String())
	}
	if !strings.Contains(resp.Error.Message, "inputSchema must be a JSON object") {
		t.Fatalf("tools/list error = %#v, want inputSchema object rejection; raw=%s", resp.Error, output.String())
	}
}

func TestServerInitializeAcceptsStandardClientFields(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"codex-cli","version":"0.142.2"},"_meta":{"trace":"init-1"}}}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Error  *jsonRPCError `json:"error,omitempty"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("initialize error = %#v; raw=%s", resp.Error, output.String())
	}
	if resp.Result.ProtocolVersion != "2024-11-05" || resp.Result.ServerInfo.Name != "test" {
		t.Fatalf("initialize result = %#v; raw=%s", resp.Result, output.String())
	}
}

func TestHTTPInitializeAcceptsStandardMCPMeta(t *testing.T) {
	server := NewHTTPServer("test", "dev", testToolProvider{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":31,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"codex-cli"},"_meta":{"trace":"init-http"}}}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error  *jsonRPCError `json:"error,omitempty"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, rec.Body.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("initialize error = %#v; body=%s", resp.Error, rec.Body.String())
	}
	if resp.Result.ProtocolVersion != "2024-11-05" {
		t.Fatalf("protocolVersion = %q, want 2024-11-05; body=%s", resp.Result.ProtocolVersion, rec.Body.String())
	}
}

func TestServerHandlesToolsCall(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if len(resp.Result.Content) != 1 || resp.Result.Content[0].Type != "text" || resp.Result.Content[0].Text != `{"ok":true}` {
		t.Fatalf("Run() content = %#v, want JSON text content; raw=%s", resp.Result.Content, output.String())
	}
}

func TestServerToolsCallAcceptsStandardMCPMeta(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"demo_tool","arguments":{"query":"ToolCallParams"},"_meta":{"progressToken":"codex-call-1"}}}`)
	var output bytes.Buffer
	var gotName string
	var gotArgs json.RawMessage
	provider := captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		gotName = name
		gotArgs = append(json.RawMessage(nil), args...)
		if scope, ok := ToolScopeFromContext(ctx); ok && scope.CWD != "" {
			t.Fatalf("scope CWD = %q, want _meta to stay outside trusted scope", scope.CWD)
		}
		return map[string]any{"ok": true}, nil
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Error  *jsonRPCError `json:"error,omitempty"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if resp.Error != nil {
		t.Fatalf("tools/call error = %#v; raw=%s", resp.Error, output.String())
	}
	if resp.Result.IsError {
		t.Fatalf("isError = true, want false; output=%s", output.String())
	}
	if gotName != "demo_tool" {
		t.Fatalf("tool name = %q, want demo_tool", gotName)
	}
	if string(gotArgs) != `{"query":"ToolCallParams"}` {
		t.Fatalf("arguments = %s, want original arguments without _meta", gotArgs)
	}
}

func TestToolsCallRejectsMissingNameBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		body string
		http bool
	}{
		{
			name: "stdio missing name",
			body: `{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"arguments":{}}}`,
		},
		{
			name: "stdio blank name",
			body: `{"jsonrpc":"2.0","id":33,"method":"tools/call","params":{"name":"  ","arguments":{}}}`,
		},
		{
			name: "http missing name",
			body: `{"jsonrpc":"2.0","id":34,"method":"tools/call","params":{"arguments":{}}}`,
			http: true,
		},
		{
			name: "http blank name",
			body: `{"jsonrpc":"2.0","id":35,"method":"tools/call","params":{"name":"  ","arguments":{}}}`,
			http: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
				called = true
				return map[string]any{"unexpected": true}, nil
			}}
			var raw []byte
			if tt.http {
				server := NewHTTPServer("test", "dev", provider)
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(tt.body))
				server.handleMCP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
				}
				raw = rec.Body.Bytes()
			} else {
				var output bytes.Buffer
				server := NewServer("test", "dev", NewStdioTransport(bytes.NewBufferString(tt.body), &output), provider)
				if err := server.Run(context.Background()); err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				raw = output.Bytes()
			}
			if called {
				t.Fatalf("CallTool() was called; raw=%s", raw)
			}
			assertJSONRPCError(t, raw, codeInvalidParams, "tool name is required")
		})
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

func TestToolsCallUsesInjectedToolErrorClassifier(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":26,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("domain conflict")
	}}
	classifier := func(toolName string, err error) (ToolErrorClassification, bool) {
		if toolName != "demo_tool" || err == nil {
			return ToolErrorClassification{}, false
		}
		return ToolErrorClassification{
			Code: "domain_conflict",
			Hint: "next: choose a different domain key",
		}, true
	}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider, WithToolErrorClassifier(classifier))
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, output.Bytes())
	if envelope.Code != "domain_conflict" {
		t.Fatalf("envelope code = %q, want domain_conflict; output=%s", envelope.Code, output.String())
	}
	if envelope.Hint != "next: choose a different domain key" {
		t.Fatalf("envelope hint = %q", envelope.Hint)
	}
}

func TestToolsCallTypedNilErrorReturnsStructuredToolError(t *testing.T) {
	const message = "context_mode=focused requires non-empty context field"
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":28,"method":"tools/call","params":{"name":"launch_agent","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		var typedNil map[string]any
		return typedNil, errors.New(message)
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, output.Bytes())
	if envelope.Success {
		t.Fatalf("envelope success = true, want false")
	}
	if envelope.Error != message {
		t.Fatalf("envelope error = %q, want %q; output=%s", envelope.Error, message, output.String())
	}
	if envelope.Code != "launch_request_invalid" {
		t.Fatalf("envelope code = %q, want launch_request_invalid; output=%s", envelope.Code, output.String())
	}
}

func TestHTTPToolsCallTypedNilErrorReturnsStructuredToolError(t *testing.T) {
	const message = "context_mode=focused requires non-empty context field"
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		var typedNil map[string]any
		return typedNil, errors.New(message)
	}}
	server := NewHTTPServer("test", "dev", provider)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":29,"method":"tools/call","params":{"name":"launch_agent","arguments":{}}}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, rec.Body.Bytes())
	if envelope.Error != message {
		t.Fatalf("envelope error = %q, want %q; body=%s", envelope.Error, message, rec.Body.String())
	}
	if envelope.Code != "launch_request_invalid" {
		t.Fatalf("envelope code = %q, want launch_request_invalid; body=%s", envelope.Code, rec.Body.String())
	}
}

func TestHTTPToolsCallUsesInjectedToolErrorClassifier(t *testing.T) {
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("domain conflict")
	}}
	classifier := func(toolName string, err error) (ToolErrorClassification, bool) {
		if toolName != "demo_tool" || err == nil {
			return ToolErrorClassification{}, false
		}
		return ToolErrorClassification{
			Code: "domain_conflict",
			Hint: "next: choose a different domain key",
		}, true
	}
	server := NewHTTPServer("test", "dev", provider, WithHTTPToolErrorClassifier(classifier))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, rec.Body.Bytes())
	if envelope.Code != "domain_conflict" {
		t.Fatalf("envelope code = %q, want domain_conflict; body=%s", envelope.Code, rec.Body.String())
	}
	if envelope.Hint != "next: choose a different domain key" {
		t.Fatalf("envelope hint = %q", envelope.Hint)
	}
}

func TestHTTPToolsListRejectsInvalidToolSchema(t *testing.T) {
	server := NewHTTPServer("test", "dev", badSchemaToolProvider{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":36,"method":"tools/list"}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertJSONRPCError(t, rec.Body.Bytes(), codeInternal, "inputSchema must be a JSON object")
}

func TestHTTPToolsListNilProviderReturnsInternalError(t *testing.T) {
	server := NewHTTPServer("test", "dev", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":37,"method":"tools/list"}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertJSONRPCError(t, rec.Body.Bytes(), codeInternal, "tool provider unavailable")
}

func TestHTTPRejectsOversizedBody(t *testing.T) {
	server := NewHTTPServer("test", "dev", testToolProvider{})
	rec := httptest.NewRecorder()
	body := strings.Repeat(" ", 10*1024*1024) + "{}"
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertJSONRPCError(t, rec.Body.Bytes(), codeParseError, "request body exceeds")
}

func TestToolErrorClassifiesMissingAstGrepDependency(t *testing.T) {
	envelope := NewToolErrorEnvelope("grep", errors.New("sg not found in PATH"))

	if envelope.Code != "dependency_missing" {
		t.Fatalf("envelope code = %q, want dependency_missing", envelope.Code)
	}
	if !strings.Contains(envelope.Hint, "sg") || !strings.Contains(strings.ToLower(envelope.Hint), "path") {
		t.Fatalf("envelope hint = %q, want sg/PATH install guidance", envelope.Hint)
	}
	if envelope.Meta["tool"] != "grep" {
		t.Fatalf("envelope meta tool = %v, want grep", envelope.Meta["tool"])
	}
}

func TestToolErrorClassifiesRegularFileNotFound(t *testing.T) {
	envelope := NewToolErrorEnvelope("file", errors.New("open foo: no such file or directory"))

	if envelope.Code != "file_not_found" {
		t.Fatalf("envelope code = %q, want file_not_found", envelope.Code)
	}
	if !strings.Contains(envelope.Hint, "file_path") {
		t.Fatalf("envelope hint = %q, want file_path guidance", envelope.Hint)
	}
}

func TestToolsCallErrorEnvelopeSetsMCPIsError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":24,"method":"tools/call","params":{"name":"file","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, NewCodedToolError("path_outside_workspace", errors.New("outside"), false, "stay inside roots")
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if !resp.Result.IsError {
		t.Fatalf("isError = false, want true; output=%s", output.String())
	}
}

func TestToolsCallPreservesStructuredValueReturnedWithError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":27,"method":"tools/call","params":{"name":"edit","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]any{
			"success": false,
			"error":   "patch matched multiple locations",
			"meta": map[string]any{
				"candidate_locations": []string{"sample.go:1-L1"},
			},
		}, errors.New("patch matched multiple locations")
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Result struct {
			IsError           bool            `json:"isError"`
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if !resp.Result.IsError {
		t.Fatalf("isError = false, want true; output=%s", output.String())
	}
	if !bytes.Contains(resp.Result.StructuredContent, []byte("candidate_locations")) {
		t.Fatalf("structuredContent = %s, want original structured payload; output=%s", resp.Result.StructuredContent, output.String())
	}
}

func TestToolsCallSuccessFalsePayloadSetsMCPIsError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":25,"method":"tools/call","params":{"name":"edit","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]any{"success": false, "error": "edit failed"}, nil
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if !resp.Result.IsError {
		t.Fatalf("isError = false, want true; output=%s", output.String())
	}
}

func TestToolsCallStringPayloadDoesNotSetMCPIsError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":26,"method":"tools/call","params":{"name":"inspect","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return "hover text", nil
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if resp.Result.IsError {
		t.Fatalf("isError = true, want false for string payload; output=%s", output.String())
	}
}

func TestOutsideWorkspaceRootErrorIsStructured(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{"name":"file","arguments":{}}}`)
	var output bytes.Buffer
	provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New(`path "/tmp/outside.go" is outside workspace roots [/repo]`)
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	envelope := decodeToolErrorEnvelopeFromOutput(t, output.Bytes())
	if envelope.Code != "path_outside_workspace" {
		t.Fatalf("envelope code = %q, want path_outside_workspace; output=%s", envelope.Code, output.String())
	}
	if envelope.Retryable {
		t.Fatalf("envelope retryable = true, want false")
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
		// archguard:ignore panic_count -- this test verifies JSON-RPC panic recovery envelope behavior.
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

func TestValidateJSONRPCIDAcceptsProtocolTypes(t *testing.T) {
	tests := []struct {
		name string
		id   json.RawMessage
	}{
		{name: "absent"},
		{name: "null", id: json.RawMessage("null")},
		{name: "string", id: json.RawMessage(`"request-1"`)},
		{name: "number", id: json.RawMessage("1.25")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateJSONRPCID(tt.id); err != nil {
				t.Fatalf("validateJSONRPCID(%s) error = %v", tt.id, err)
			}
		})
	}
}

func TestTransportsAcceptAbsentAndNullJSONRPCID(t *testing.T) {
	tests := []struct {
		name         string
		idField      string
		http         bool
		wantResponse bool
	}{
		{name: "stdio absent"},
		{name: "stdio null", idField: `,"id":null`, wantResponse: true},
		{name: "http absent", http: true},
		{name: "http null", idField: `,"id":null`, http: true, wantResponse: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
				calls++
				return map[string]any{"ok": true}, nil
			}}
			body := `{"jsonrpc":"2.0"` + tt.idField + `,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`
			var raw []byte
			if tt.http {
				server := NewHTTPServer("test", "dev", provider)
				rec := httptest.NewRecorder()
				server.handleMCP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
				raw = rec.Body.Bytes()
			} else {
				var output bytes.Buffer
				server := NewServer("test", "dev", NewStdioTransport(strings.NewReader(body), &output), provider)
				if err := server.Run(context.Background()); err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				raw = output.Bytes()
			}
			if calls != 1 {
				t.Fatalf("provider calls = %d, want 1", calls)
			}
			if tt.wantResponse != (len(bytes.TrimSpace(raw)) != 0) {
				t.Fatalf("response = %s, wantResponse = %v", raw, tt.wantResponse)
			}
			if tt.wantResponse {
				var resp jsonRPCResponse
				decodeJSONRPCOutput(t, raw, &resp)
				if string(resp.ID) != "null" || resp.Error != nil {
					t.Fatalf("response = %#v, want successful id:null; raw=%s", resp, raw)
				}
			}
		})
	}
}

func TestTransportsRejectInvalidJSONRPCIDBeforeProvider(t *testing.T) {
	tests := []struct {
		name string
		id   string
		http bool
	}{
		{name: "stdio object", id: `{"nested":1}`},
		{name: "stdio array", id: `[1]`},
		{name: "stdio boolean", id: "true"},
		{name: "http object", id: `{"nested":1}`, http: true},
		{name: "http array", id: `[1]`, http: true},
		{name: "http boolean", id: "true", http: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			provider := captureToolProvider{call: func(context.Context, string, json.RawMessage) (any, error) {
				calls++
				return map[string]any{"ok": true}, nil
			}}
			body := `{"jsonrpc":"2.0","id":` + tt.id + `,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`
			var raw []byte
			if tt.http {
				server := NewHTTPServer("test", "dev", provider)
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
				server.handleMCP(rec, req)
				raw = rec.Body.Bytes()
			} else {
				var output bytes.Buffer
				server := NewServer("test", "dev", NewStdioTransport(strings.NewReader(body), &output), provider)
				if err := server.Run(context.Background()); err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				raw = output.Bytes()
			}
			var resp jsonRPCResponse
			decodeJSONRPCOutput(t, raw, &resp)
			if resp.Error == nil || resp.Error.Code != codeInvalidReq {
				t.Fatalf("response error = %#v, want invalid request; raw=%s", resp.Error, raw)
			}
			if string(resp.ID) != "null" {
				t.Fatalf("response id = %s, want null; raw=%s", resp.ID, raw)
			}
			if calls != 0 {
				t.Fatalf("provider calls = %d, want 0", calls)
			}
		})
	}
}

func assertJSONRPCError(t *testing.T, raw []byte, wantCode int, wantMessage string) {
	t.Helper()
	var resp struct {
		Error *jsonRPCError `json:"error,omitempty"`
	}
	decodeJSONRPCOutput(t, raw, &resp)
	if resp.Error == nil {
		t.Fatalf("JSON-RPC error = nil, want code %d; raw=%s", wantCode, raw)
	}
	if resp.Error.Code != wantCode {
		t.Fatalf("JSON-RPC code = %d, want %d; raw=%s", resp.Error.Code, wantCode, raw)
	}
	if !strings.Contains(resp.Error.Message, wantMessage) {
		t.Fatalf("JSON-RPC message = %q, want contains %q; raw=%s", resp.Error.Message, wantMessage, raw)
	}
}
