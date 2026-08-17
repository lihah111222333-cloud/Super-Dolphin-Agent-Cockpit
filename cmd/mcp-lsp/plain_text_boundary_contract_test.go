package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const boundaryContractText = "OK total=0 showing=0 truncated=0 unit=item"

// TestMcpLSPPlainTextBoundaryContract 锁定 mcp-lsp 三条入口的唯一纯文本结果边界。
func TestMcpLSPPlainTextBoundaryContract(t *testing.T) {
	t.Run("direct stdio omits structured content", testDirectLSPBoundaryOmitsStructuredContent)
	t.Run("scoped path omits structured content", testScopedLSPBoundaryOmitsStructuredContent)
	t.Run("legacy HTTP omits structured content", testHTTPLSPBoundaryOmitsStructuredContent)
}

func testHTTPLSPBoundaryOmitsStructuredContent(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	server := common.NewHTTPServer("mcp-lsp", "dev", registryToolProvider{defs: boundaryContractToolDefinitions()},
		common.WithHTTPLoggerRuntime(newTestLoggerRuntime()), common.WithHTTPToolCallResultPolicy(lspToolCallResultPolicy()))
	addr, err := server.Start(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start legacy HTTP mcp-lsp: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Stop(ctx); err != nil {
			t.Errorf("stop legacy HTTP mcp-lsp: %v", err)
		}
	})
	endpoint := "http://" + addr + "/mcp"
	sessionID := initializeBoundaryHTTPSession(t, endpoint)
	request := mustDirectToolCallRequest(t, root, "file", map[string]any{"action": "read_file"})
	httpRequest, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(request))
	if err != nil {
		t.Fatalf("create legacy HTTP tools/call: %v", err)
	}
	httpRequest.Header.Set("Mcp-Session-Id", sessionID)
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatalf("legacy HTTP tools/call: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read legacy HTTP tools/call: %v", err)
	}
	decoded := decodeDirectToolsCallResponse(t, body)
	if len(bytes.TrimSpace(decoded.Result.StructuredContent)) != 0 || decoded.Result.Content[0].Text != boundaryContractText {
		t.Fatalf("legacy HTTP mcp-lsp result violated text-only policy: %s", body)
	}
}

func initializeBoundaryHTTPSession(t *testing.T, endpoint string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"boundary-test"}}}`,
	))
	if err != nil {
		t.Fatalf("create legacy HTTP initialize: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("legacy HTTP initialize: %v", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK || response.Header.Get("Mcp-Session-Id") == "" {
		t.Fatalf("legacy HTTP initialize status=%d session=%q", response.StatusCode, response.Header.Get("Mcp-Session-Id"))
	}
	return response.Header.Get("Mcp-Session-Id")
}

func testDirectLSPBoundaryOmitsStructuredContent(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	request := mustDirectToolCallRequest(t, root, "file", map[string]any{"action": "read_file"})
	response := runDirectToolCallForPlainText(t, request, boundaryContractToolDefinitions())
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("direct mcp-lsp tools/call returned structuredContent: %s", response.Result.StructuredContent)
	}
	if got := response.Result.Content[0].Text; got != boundaryContractText {
		t.Fatalf("direct mcp-lsp content = %q, want %q", got, boundaryContractText)
	}
}

func testScopedLSPBoundaryOmitsStructuredContent(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	params := mustScopedToolCallParams(t, root, "file", map[string]any{"action": "read_file"})
	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: boundaryContractToolDefinitions()}, "lsp", params)
	if err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v", err)
	}
	wrapped := requireBoundaryEnvelope(t, result)
	if _, ok := wrapped["structuredContent"]; ok {
		t.Fatalf("scoped mcp-lsp tools/call returned structuredContent: %#v", wrapped["structuredContent"])
	}
	if got := boundaryEnvelopeText(t, wrapped); got != boundaryContractText {
		t.Fatalf("scoped mcp-lsp content = %q, want %q", got, boundaryContractText)
	}
}

func boundaryContractToolDefinitions() []toolDefinition {
	return []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return boundaryContractText, nil
		},
	}}
}

func mustScopedToolCallParams(t *testing.T, root, name string, arguments any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name": name, "arguments": arguments, "_cwd": root, "_workspaceRoots": []string{root},
	})
	if err != nil {
		t.Fatalf("marshal scoped tool call: %v", err)
	}
	return raw
}

func requireBoundaryEnvelope(t *testing.T, result any) map[string]any {
	t.Helper()
	wrapped, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("tools/call result = %T, want map envelope", result)
	}
	return wrapped
}

func boundaryEnvelopeText(t *testing.T, wrapped map[string]any) string {
	t.Helper()
	content, ok := wrapped["content"].([]map[string]string)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want one text item", wrapped["content"])
	}
	return content[0]["text"]
}
