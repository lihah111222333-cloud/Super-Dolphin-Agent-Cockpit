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

func TestServerInitializeAcceptsStandardClientFields(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"codex-cli","version":"0.142.2"}}}`)
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
