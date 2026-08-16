//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeGoplsPlainTextContractEnv = "MCP_LSP_FAKE_GOPLS_PLAIN_TEXT_CONTRACT"

// TestMcpLSPBinaryToolsCallPlainTextOnlyContract_E2E 经过真实 binary/stdin/stdout 锁定 mcp-lsp 单一纯文本结果通道。
func TestMcpLSPBinaryToolsCallPlainTextOnlyContract_E2E(t *testing.T) {
	root, target := writePlainTextContractFixture(t)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsPlainTextContractEnv + "=1",
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	assertBinaryGrepPlainTextContract(t, client)
	assertBinaryFoldingPlainTextContract(t, client, target)
	assertBinaryCallHierarchyPlainTextContract(t, client, target)
	assertBinarySemanticTokensPlainTextContract(t, client, target)
	assertBinaryInvalidRegexPlainTextContract(t, client)
	assertDiagnosticsGateConsumesPlainText(t, ctx, binary, fakeGoplsBinDir)
}

func assertBinaryGrepPlainTextContract(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	t.Run("grep counts all matches while returning ten rows", func(t *testing.T) {
		result := client.callTool(t, "grep", map[string]any{
			"action": "text_search", "query": "plain_text_needle", "path": ".", "max_results": 10,
		})
		text := assertPlainTextOnlyMCPResult(t, result, false)
		assertLineProtocolSummary(t, text, "OK total=16 showing=10 truncated=1 unit=match", 10)
	})
}

func assertBinaryFoldingPlainTextContract(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	t.Run("folding range total is independent from display limit", func(t *testing.T) {
		result := client.callTool(t, "structure", map[string]any{
			"action": "folding_range", "file_path": target, "language_id": "go", "max_results": 5,
		})
		text := assertPlainTextOnlyMCPResult(t, result, false)
		assertLineProtocolSummary(t, text, "OK total=16 showing=5 truncated=1 unit=range", 5)
	})
}

func assertBinaryCallHierarchyPlainTextContract(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	t.Run("call hierarchy limits nested edges and final text bytes", func(t *testing.T) {
		result := client.callTool(t, "xref", map[string]any{
			"action": "call_hierarchy", "pos": target + ":3:6", "language_id": "go",
			"direction": "outgoing", "max_results": 5,
		})
		text := assertPlainTextOnlyMCPResult(t, result, false)
		assertLineProtocolSummary(t, text, "OK total=41 showing=5 truncated=1 unit=edge", 5)
		if len([]byte(text)) > 4*1024 {
			t.Errorf("call hierarchy content = %d bytes, want <= 4096; text=%q", len([]byte(text)), text)
		}
	})
}

func assertBinarySemanticTokensPlainTextContract(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	t.Run("semantic tokens decode initialize legend by token tuples", func(t *testing.T) {
		result := client.callTool(t, "structure", map[string]any{
			"action": "semantic_tokens", "file_path": target, "language_id": "go", "max_results": 5,
		})
		text := assertPlainTextOnlyMCPResult(t, result, false)
		assertLineProtocolSummary(t, text, "OK total=12 showing=5 truncated=1 unit=token", 5)
		for _, want := range []string{"LEGEND\t", "type=function", "modifiers=declaration"} {
			if !strings.Contains(text, want) {
				t.Errorf("semantic token text missing %q: %q", want, text)
			}
		}
		if strings.Contains(text, "No semantic tokens decoded") || strings.Contains(text, "[0,") {
			t.Errorf("semantic token text exposed undecoded/raw integers: %q", text)
		}
	})
}

func assertBinaryInvalidRegexPlainTextContract(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	t.Run("invalid regex is actionable invalid params", func(t *testing.T) {
		result := client.callTool(t, "grep", map[string]any{
			"action": "text_search", "query": "[", "path": ".", "regex": true,
		})
		text := assertPlainTextOnlyMCPResult(t, result, true)
		lines := strings.Split(text, "\n")
		if len(lines) < 3 || lines[0] != "ERROR code=invalid_params retryable=0" ||
			!strings.HasPrefix(lines[1], "MESSAGE\t") || !strings.HasPrefix(lines[2], "HINT\t") {
			t.Errorf("invalid regex text does not match stable error protocol: %q", text)
		}
	})
}

func assertPlainTextOnlyMCPResult(t *testing.T, response mcpLSPBinaryResponse, wantError bool) string {
	t.Helper()
	if response.Result.IsError != wantError {
		t.Errorf("tools/call isError = %v, want %v; text=%q structured=%s",
			response.Result.IsError, wantError, response.Result.ContentText(), response.Result.StructuredContent)
	}
	if len(bytes.TrimSpace(response.Result.StructuredContent)) != 0 {
		t.Errorf("tools/call result must omit structuredContent; got %s", response.Result.StructuredContent)
	}
	text := response.Result.ContentText()
	if strings.TrimSpace(text) == "" {
		t.Fatal("tools/call content text is empty")
	}
	if json.Valid([]byte(text)) {
		t.Errorf("tools/call content must not be JSON: %s", text)
	}
	return text
}

func assertLineProtocolSummary(t *testing.T, text, wantHeader string, wantRows int) {
	t.Helper()
	lines := strings.Split(text, "\n")
	if lines[0] != wantHeader {
		t.Errorf("line protocol header = %q, want %q; text=%q", lines[0], wantHeader, text)
	}
	rows := 0
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "ROW\t") {
			rows++
		}
	}
	if rows != wantRows {
		t.Errorf("line protocol ROW count = %d, want %d; text=%q", rows, wantRows, text)
	}
}

func assertDiagnosticsGateConsumesPlainText(t *testing.T, parent context.Context, binary, fakeGoplsBinDir string) {
	t.Helper()
	t.Run("diagnostics gate consumes the same text channel", func(t *testing.T) {
		repoRoot := repoRootForMcpLSPBinaryTest(t)
		ctx, cancel := context.WithTimeout(parent, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "run", "./scripts/lsp_diagnostics_gate",
			"--root", repoRoot, "--file", "cmd/mcp-lsp/main.go", "--peer", binary, "--timeout", "45s")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(),
			fakeGoplsPlainTextContractEnv+"=1",
			"PATH="+fakeGoplsBinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"AGENT_LSP_SHARED_CACHE_DIR="+filepath.Join(t.TempDir(), "gate-lsp-cache"),
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("diagnostics gate failed to consume mcp-lsp result text: %v\n%s", err, out)
		}
	})
}

func writePlainTextContractFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/plaintext\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	var source strings.Builder
	source.WriteString("package plaintext\n\nfunc target() {\n")
	for i := range 16 {
		fmt.Fprintf(&source, "\t_ = \"plain_text_needle_%02d\"\n", i)
	}
	source.WriteString("}\n")
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte(source.String()), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return root, target
}

func fakeGoplsPlainTextFoldingRanges() []map[string]any {
	ranges := make([]map[string]any, 0, 16)
	for i := range 16 {
		ranges = append(ranges, map[string]any{
			"startLine": i, "startCharacter": 0, "endLine": i + 1, "endCharacter": 1, "kind": "region",
		})
	}
	return ranges
}

func fakeGoplsPlainTextSemanticTokens() map[string]any {
	data := make([]int, 0, 12*5)
	for i := range 12 {
		deltaLine := 1
		if i == 0 {
			deltaLine = 0
		}
		data = append(data, deltaLine, 0, 4, i%4, 1)
	}
	return map[string]any{"resultId": "plain-text-contract", "data": data}
}

func fakeGoplsPlainTextStructureResult(method string) any {
	switch method {
	case "textDocument/foldingRange":
		return fakeGoplsPlainTextFoldingRanges()
	case "textDocument/semanticTokens/full":
		return fakeGoplsPlainTextSemanticTokens()
	default:
		return nil
	}
}

func fakeGoplsPlainTextHierarchyCalls(req fakeLSPRequest) []map[string]any {
	var params struct {
		Item struct {
			URI string `json:"uri"`
		} `json:"item"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil
	}
	calls := make([]map[string]any, 0, 41)
	for i := range 41 {
		item := fakeGoplsPlainTextHierarchyItem(params.Item.URI, i)
		key := "from"
		if strings.HasSuffix(req.Method, "outgoingCalls") {
			key = "to"
		}
		calls = append(calls, map[string]any{
			key: item,
			"fromRanges": []map[string]any{{
				"start": map[string]any{"line": i + 2, "character": 5},
				"end":   map[string]any{"line": i + 2, "character": 9},
			}},
		})
	}
	return calls
}

func fakeGoplsPlainTextHierarchyItem(uri string, index int) map[string]any {
	line := index + 2
	return map[string]any{
		"name": fmt.Sprintf("edge_%02d", index), "kind": 12, "uri": uri,
		"range": map[string]any{
			"start": map[string]any{"line": line, "character": 0},
			"end":   map[string]any{"line": line, "character": 10},
		},
		"selectionRange": map[string]any{
			"start": map[string]any{"line": line, "character": 5},
			"end":   map[string]any{"line": line, "character": 9},
		},
	}
}
