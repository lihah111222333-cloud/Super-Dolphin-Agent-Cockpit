package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestDirectToolsCallUsesExplicitPlainTextFormatter(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	request := mustDirectToolCallRequest(t, root, "structure", map[string]any{"action": "document_symbol"})
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "structure"},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return []protocol.DocumentSymbol{{
				Name:           "main",
				Kind:           protocol.SymbolKindFunction,
				Range:          protocol.Range{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 3}},
				SelectionRange: protocol.Range{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 1}},
			}}, nil
		},
	}}
	response := runDirectToolCallForPlainText(t, request, defs)
	text := response.Result.Content[0].Text
	if strings.HasPrefix(text, "[") {
		t.Fatalf("content text = %q, want formatted outline text", text)
	}
	if !strings.Contains(text, "main") {
		t.Fatalf("content text = %q, want document symbol outline", text)
	}
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("mcp-lsp tools/call returned structuredContent: %s", response.Result.StructuredContent)
	}
}

func TestDirectToolsCallErrorUsesMcpLSPLineProtocol(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	request := mustDirectToolCallRequest(t, root, "grep", map[string]any{})
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "grep"},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return nil, common.NewCodedToolError(
				"invalid_params", errors.New("invalid regex"), false,
				"fix-regex-syntax-or-set-regex=false-for-literal-search",
			)
		},
	}}
	response := runDirectToolCallForPlainText(t, request, defs)
	if !response.Result.IsError {
		t.Fatal("tools/call isError = false, want true")
	}
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("mcp-lsp tools/call returned structuredContent: %s", response.Result.StructuredContent)
	}
	want := "ERROR code=invalid_params retryable=0\n" +
		"MESSAGE\tinvalid regex\n" +
		"HINT\tfix-regex-syntax-or-set-regex=false-for-literal-search\n" +
		"ATTR\ttool=grep"
	if got := response.Result.Content[0].Text; got != want {
		t.Fatalf("error content = %q, want %q", got, want)
	}
}

func mustDirectToolCallRequest(t *testing.T, root, name string, arguments any) []byte {
	t.Helper()
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":            name,
			"arguments":       arguments,
			"_cwd":            root,
			"_workspaceRoots": []string{root},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return request
}

func runDirectToolCallForPlainText(t *testing.T, request []byte, defs []toolDefinition) directToolsCallResponse {
	t.Helper()

	var output bytes.Buffer
	server := newTestMCPServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs}, common.WithToolCallResultPolicy(lspToolCallResultPolicy()))
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	response := decodeDirectToolsCallResponse(t, output.Bytes())
	if len(response.Result.Content) != 1 {
		t.Fatalf("content items = %d, want 1; output=%s", len(response.Result.Content), output.String())
	}
	return response
}

type directToolsCallResponse struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	} `json:"result"`
}

func decodeDirectToolsCallResponse(t *testing.T, raw []byte) directToolsCallResponse {
	t.Helper()
	if _, body, ok := bytes.Cut(raw, []byte("\r\n\r\n")); ok {
		raw = body
	}
	var response directToolsCallResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("unmarshal tools/call response: %v; raw=%s", err, string(raw))
	}
	return response
}
