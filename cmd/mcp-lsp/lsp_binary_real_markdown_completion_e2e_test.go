//go:build e2e && (darwin || linux || windows)

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcpLSPBinaryRealMarkdownCompletionRegistration_E2E locks the real
// Markdown server capability path at the checked-in fixture position. The
// position is a heading, so an empty completion list is valid; a typed
// capability_unsupported response is not. The test deliberately does not
// require or manufacture a completion item.
func TestMcpLSPBinaryRealMarkdownCompletionRegistration_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Markdown completion registration e2e in short mode")
	}
	requireHostBinariesForE2E(t, []realLSPDiagnosticsCase{{
		languageID: "markdown",
		binaries:   []string{"vscode-markdown-language-server"},
	}})

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	target := copyRealMarkdownCompletionFixture(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	completionID := time.Now().UnixNano()
	t.Logf("stage=completion_request_start request_id=%d method=textDocument/completion position=%s", completionID, target+":1:3")
	completion, completed := callMarkdownCompletionWithDeadline(t, client, map[string]any{
		"pos":         target + ":1:3",
		"max_results": 20,
	})
	if !completed {
		t.Fatalf("stage=completion_response_timeout request_id=%d method=textDocument/completion timeout=90s stderr=%s", completionID, client.stderrString())
	}
	t.Logf("stage=completion_response_received request_id=%d method=textDocument/completion", completionID)
	requireMCPToolSuccess(t, client, completion, "markdown completion at api.md:1:3")
	if strings.Contains(completion.Result.ContentText(), "capability_unsupported") {
		t.Fatalf("Markdown completion returned capability_unsupported: text=%q stderr=%s", completion.Result.ContentText(), client.stderrString())
	}
	pathCompletionID := time.Now().UnixNano()
	pathPosition := target + ":11:18"
	t.Logf("stage=path_completion_request_start request_id=%d method=textDocument/completion position=%s", pathCompletionID, pathPosition)
	pathCompletion, completed := callMarkdownCompletionWithDeadline(t, client, map[string]any{
		"pos":         pathPosition,
		"max_results": 20,
	})
	if !completed {
		t.Fatalf("stage=path_completion_response_timeout request_id=%d method=textDocument/completion timeout=90s stderr=%s", pathCompletionID, client.stderrString())
	}
	labels := completionLabelsFromContent(t, pathCompletion)
	t.Logf("stage=path_completion_response_received request_id=%d method=textDocument/completion labels=%v", pathCompletionID, labels)
	if len(labels) == 0 {
		t.Fatalf("Markdown path completion returned no real candidates at %s", pathPosition)
	}
}

func callMarkdownCompletionWithDeadline(t *testing.T, client *mcpLSPBinaryClient, args map[string]any) (mcpLSPBinaryResponse, bool) {
	t.Helper()
	result := make(chan mcpLSPBinaryResponse, 1)
	go func() {
		path := strings.Split(fmt.Sprint(args["pos"]), ":")[0]
		result <- client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": path})
	}()
	select {
	case response := <-result:
		return response, true
	case <-time.After(90 * time.Second):
		return mcpLSPBinaryResponse{}, false
	}
}

func copyRealMarkdownCompletionFixture(t *testing.T, root string) string {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	relative := filepath.FromSlash("markdown/docs/api.md")
	source := filepath.Join(repoRoot, "bin", "LSP", "test", relative)
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read checked-in Markdown fixture %q: %v", source, err)
	}
	target := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("create Markdown fixture directory %q: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatalf("write Markdown fixture %q: %v", target, err)
	}
	referenceSource := filepath.Join(repoRoot, "bin", "LSP", "test", "markdown", "docs", "reference.md")
	referenceContent, err := os.ReadFile(referenceSource)
	if err != nil {
		t.Fatalf("read checked-in Markdown reference fixture %q: %v", referenceSource, err)
	}
	if err := os.WriteFile(filepath.Join(root, "markdown", "docs", "reference.md"), referenceContent, 0o600); err != nil {
		t.Fatalf("write Markdown reference fixture: %v", err)
	}
	return target
}
