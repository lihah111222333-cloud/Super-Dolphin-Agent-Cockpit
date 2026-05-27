package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestRenderReadContentDefaultLimitIsTwoHundredFifty(t *testing.T) {
	var body strings.Builder
	for line := 1; line <= 260; line++ {
		if line > 1 {
			body.WriteByte('\n')
		}
		fmt.Fprintf(&body, "line-%03d", line)
	}

	got := renderReadContent(body.String(), 1, 0)
	if !strings.Contains(got, "250: line-250") {
		t.Fatalf("read_file default output missing line 250: %q", got)
	}
	if strings.Contains(got, "251: line-251") {
		t.Fatalf("read_file default output includes line 251: %q", got)
	}
	if !strings.Contains(got, "...[showing lines 1-250 of 260 total, use offset=251 to continue]") {
		t.Fatalf("read_file continuation hint = %q, want 250-line default", got)
	}
}

func TestFileHandlerAppliesSixteenKiBOutputBudget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "large.txt")
	if err := os.WriteFile(target, []byte(largeLineFileContent(260, 120)), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := callFileTool(t, root, fileToolInput{Action: "read_file", FilePath: target})
	if err != nil {
		t.Fatalf("read_file returned error: %v", err)
	}
	payload, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("read_file result type = %T, want overflow envelope", got)
	}
	if payload["error_code"] != "result_too_large" {
		t.Fatalf("error_code = %#v, want result_too_large", payload["error_code"])
	}
	if payload["budget_bytes"] != middleware.ToolBudget("file") {
		t.Fatalf("budget_bytes = %#v, want %d", payload["budget_bytes"], middleware.ToolBudget("file"))
	}
}

func callFileTool(t *testing.T, root string, input fileToolInput) (any, error) {
	t.Helper()
	handler := NewFileHandler(Config{WorkspaceRoot: root})
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return handler(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), payload)
}

func largeLineFileContent(lines int, width int) string {
	var body strings.Builder
	for line := 1; line <= lines; line++ {
		if line > 1 {
			body.WriteByte('\n')
		}
		fmt.Fprintf(&body, "line-%03d %s", line, strings.Repeat("x", width))
	}
	return body.String()
}
