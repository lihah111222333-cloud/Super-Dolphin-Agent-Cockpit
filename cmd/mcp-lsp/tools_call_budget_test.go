package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	lsptools "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestDirectToolsCallGrepContentWithinSixteenKiBBudget(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	writeLargeDirectGrepFixture(t, root)
	args := map[string]any{
		"action":      "text_search",
		"query":       "needle",
		"path":        root,
		"glob":        "*.txt",
		"max_results": 50,
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
			"name":            "grep",
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
		Manifest: ToolManifest{Name: "grep"},
		Handler:  ToolHandler(lsptools.NewGrepHandler(lsptools.Config{WorkspaceRoot: root})),
	}}
	server := common.NewServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	response := decodeDirectToolsCallResponse(t, output.Bytes())
	if len(response.Result.Content) != 1 {
		t.Fatalf("content items = %d, want 1; output=%s", len(response.Result.Content), output.String())
	}
	budget := middleware.ToolBudget("grep")
	if got := len(response.Result.Content[0].Text); got > budget {
		t.Fatalf("content text = %d bytes, want <= %d", got, budget)
	}
	if got := len(response.Result.StructuredContent); got > budget {
		t.Fatalf("structuredContent = %d bytes, want <= %d", got, budget)
	}
	if !strings.Contains(response.Result.Content[0].Text, "Warning: results were truncated") {
		t.Fatalf("content text missing truncation warning: %s", response.Result.Content[0].Text)
	}
	var payload struct {
		DroppedForPayload int `json:"dropped_for_payload"`
	}
	if err := json.Unmarshal(response.Result.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal structuredContent: %v; raw=%s", err, response.Result.StructuredContent)
	}
	if payload.DroppedForPayload == 0 {
		t.Fatalf("direct tools/call grep did not drop rows; structuredContent=%s", response.Result.StructuredContent)
	}
}

func TestDirectToolsCallGrepSingleTSVFileHonorsGlob(t *testing.T) {
	root := canonicalToolTestRoot(t, t.TempDir())
	target := filepath.Join(root, "index.tsv")
	if err := os.WriteFile(target, []byte("path\tmodule\ncmd/mcp-orch/main.go\tcmd\n"), 0o600); err != nil {
		t.Fatalf("write grep fixture: %v", err)
	}
	request := mustDirectToolCallRequest(t, root, "grep", map[string]any{
		"action":      "text_search",
		"query":       "cmd/mcp-orch/main.go",
		"path":        target,
		"glob":        "*.tsv",
		"max_results": 10,
	})
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "grep"},
		Handler:  ToolHandler(lsptools.NewGrepHandler(lsptools.Config{WorkspaceRoot: root})),
	}}
	response := runDirectToolCallForPlainText(t, request, defs)
	var payload struct {
		Data    map[string]struct{} `json:"data"`
		Total   int                 `json:"total"`
		Showing int                 `json:"showing"`
	}
	if err := json.Unmarshal(response.Result.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal structuredContent: %v; raw=%s", err, response.Result.StructuredContent)
	}
	if payload.Total != 1 || payload.Showing != 1 {
		t.Fatalf("grep totals = total:%d showing:%d, want single TSV match", payload.Total, payload.Showing)
	}
	if _, ok := payload.Data[target]; !ok {
		t.Fatalf("grep data = %#v, want match for %s", payload.Data, target)
	}
	if !strings.Contains(response.Result.Content[0].Text, "cmd/mcp-orch/main.go") {
		t.Fatalf("content text = %q, want TSV match", response.Result.Content[0].Text)
	}
}

type directToolsCallResponse struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
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

func writeLargeDirectGrepFixture(t *testing.T, root string) {
	t.Helper()
	for i := range 50 {
		name := fmt.Sprintf("file-%02d-%s.txt", i, strings.Repeat("x", 120))
		body := "needle " + strings.Repeat("payload", 30) + "\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write grep fixture: %v", err)
		}
	}
}
