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
	var payload struct {
		DroppedForPayload int `json:"dropped_for_payload"`
	}
	if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal content text: %v", err)
	}
	if payload.DroppedForPayload == 0 {
		t.Fatalf("direct tools/call grep did not drop rows; content=%s", response.Result.Content[0].Text)
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
