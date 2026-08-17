package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	lsptools "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestDirectToolsCallReadFileReturnsPlainTextContent(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write file fixture: %v", err)
	}
	args := map[string]any{
		"action":    "read_file",
		"file_path": "main.go",
		"limit":     2,
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":            "file",
			"arguments":       json.RawMessage(rawArgs),
			"_cwd":            root,
			"_workspaceRoots": []string{root},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var output bytes.Buffer
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler:  ToolHandler(lsptools.NewFileHandler(lsptools.Config{WorkspaceRoot: root})),
	}}
	server := newTestMCPServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs}, common.WithToolCallResultPolicy(lspToolCallResultPolicy()))
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	response := decodeDirectToolsCallResponse(t, output.Bytes())
	if len(response.Result.Content) != 1 {
		t.Fatalf("content items = %d, want 1; output=%s", len(response.Result.Content), output.String())
	}
	text := response.Result.Content[0].Text
	if strings.HasPrefix(text, `"`) {
		t.Fatalf("content text = %q, want unquoted plain text", text)
	}
	if !strings.Contains(text, "ROW\tline=1\ttext=package main") {
		t.Fatalf("content text = %q, want line-numbered file text", text)
	}
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Fatalf("mcp-lsp tools/call returned structuredContent: %s", response.Result.StructuredContent)
	}
}

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
