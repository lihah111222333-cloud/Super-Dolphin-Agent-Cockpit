package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	lsptools "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func registerPlainTextRendererForTest(t *testing.T) {
	t.Helper()
	common.RegisterToolResultPlainTextRenderer(lsptools.FormatToPlainText)
	t.Cleanup(func() {
		common.RegisterToolResultPlainTextRenderer(nil)
	})
}

func TestDirectToolsCallReadFileReturnsPlainTextContent(t *testing.T) {
	registerPlainTextRendererForTest(t)

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
	server := newTestMCPServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs})
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
	if !strings.Contains(text, "1: package main") {
		t.Fatalf("content text = %q, want line-numbered file text", text)
	}
	var structured struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(response.Result.StructuredContent, &structured); err != nil {
		t.Fatalf("unmarshal structuredContent: %v; raw=%s", err, response.Result.StructuredContent)
	}
	if !strings.Contains(structured.Value, "1: package main") {
		t.Fatalf("structuredContent.value = %q, want line-numbered file text", structured.Value)
	}
}

func TestDirectToolsCallUsesRegisteredPlainTextFormatter(t *testing.T) {
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
	if !strings.Contains(text, "Document Symbol Outline:") || !strings.Contains(text, "`main`") {
		t.Fatalf("content text = %q, want document symbol outline", text)
	}
	var structured struct {
		Items []protocol.DocumentSymbol `json:"items"`
		Total int                       `json:"total"`
	}
	if err := json.Unmarshal(response.Result.StructuredContent, &structured); err != nil {
		t.Fatalf("unmarshal structuredContent: %v; raw=%s", err, response.Result.StructuredContent)
	}
	if structured.Total != 1 || len(structured.Items) != 1 || structured.Items[0].Name != "main" {
		t.Fatalf("structuredContent = %#v, want original document symbol array wrapper", structured)
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
	registerPlainTextRendererForTest(t)

	var output bytes.Buffer
	server := newTestMCPServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	response := decodeDirectToolsCallResponse(t, output.Bytes())
	if len(response.Result.Content) != 1 {
		t.Fatalf("content items = %d, want 1; output=%s", len(response.Result.Content), output.String())
	}
	return response
}
