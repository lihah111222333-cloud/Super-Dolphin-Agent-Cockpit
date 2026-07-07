//go:build e2e
// +build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeGoplsOrphanedFilesWarning = "warning: while diagnosing orphaned files: session is shut down"
const fakeGoplsPullDiagnosticsOnlyEnv = "MCP_LSP_FAKE_GOPLS_PULL_DIAGNOSTICS_ONLY"
const fakeGoplsSuppressDiagnosticProviderEnv = "MCP_LSP_FAKE_GOPLS_SUPPRESS_DIAGNOSTIC_PROVIDER"
const fakeGoplsRequireE2EBuildFlagEnv = "MCP_LSP_FAKE_GOPLS_REQUIRE_E2E_BUILDFLAG"

func TestMcpLSPBinaryGoplsOrphanedFilesShutdownWarningIsNotErrorLog_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		"MCP_LSP_FAKE_GOPLS_SHUTDOWN_WARNING=1",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	if diagnostics.Result.IsError {
		t.Fatalf("diagnostics returned MCP error result; text=%q structured=%s stderr=%s",
			diagnostics.Result.ContentText(), diagnostics.Result.StructuredContent, client.stderrString())
	}

	stderr := waitForFakeGoplsWarningStderr(t, client)
	if strings.Contains(stderr, "level=ERROR") || strings.Contains(stderr, `"level":"ERROR"`) {
		t.Fatalf("gopls shutdown orphaned-files warning was logged as ERROR; stderr=%s", stderr)
	}
}

func TestMcpLSPBinaryGoplsDiagnosticsFallsBackToPullWhenPublishIsSilent_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsPullDiagnosticsOnlyEnv + "=1",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	structure := client.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, structure, "go document_symbol")
	requireToolResultContains(t, structure, "main", "go document_symbol")

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "go diagnostics")
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if payload.Total != 0 || len(payload.Data) != 0 {
		t.Fatalf("go diagnostics payload = %#v, want empty diagnostics; raw=%s text=%q stderr=%s",
			payload, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
}

func TestMcpLSPBinaryGoplsDiagnosticsTreatsReadyBootstrapWithoutPublishAsEmpty_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsPullDiagnosticsOnlyEnv + "=1",
		fakeGoplsSuppressDiagnosticProviderEnv + "=1",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	structure := client.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, structure, "go document_symbol")
	requireToolResultContains(t, structure, "main", "go document_symbol")

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "go diagnostics without publish")
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if payload.Total != 0 || len(payload.Data) != 0 {
		t.Fatalf("go diagnostics payload = %#v, want empty diagnostics; raw=%s text=%q stderr=%s",
			payload, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
}

func TestMcpLSPBinaryGoplsDiagnosticsUsesBuildTagsForE2EFile_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writeFakeGoplsE2EBuildTagFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsPullDiagnosticsOnlyEnv + "=1",
		fakeGoplsRequireE2EBuildFlagEnv + "=1",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   target,
		"language_id": "go",
	})
	requireMCPToolSuccess(t, client, diagnostics, "go e2e build-tag diagnostics")
}

func TestFakeGoplsShutdownWarningHelper(t *testing.T) {
	if os.Getenv("MCP_LSP_FAKE_GOPLS_SHUTDOWN_WARNING") != "1" {
		return
	}
	runFakeGoplsShutdownWarningLangserver()
	os.Exit(0)
}

func writeFakeGoplsGoFixture(t *testing.T, root string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/gopls-warning\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatalf("write go.mod fixture: %v", err)
	}
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write Go fixture: %v", err)
	}
	return target
}

func writeFakeGoplsE2EBuildTagFixture(t *testing.T, root string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/gopls-e2e\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatalf("write go.mod fixture: %v", err)
	}
	target := filepath.Join(root, "lsp_binary_e2e_probe_test.go")
	content := strings.Join([]string{
		"//go:build e2e",
		"",
		"package main",
		"",
		"func TestE2EProbe(t testingT) {}",
		"",
		"type testingT interface {",
		"\tHelper()",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write e2e Go fixture: %v", err)
	}
	return target
}

func writeFakeGoplsShutdownWarningLangserver(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gopls")
	script := "#!/bin/sh\nMCP_LSP_FAKE_GOPLS_SHUTDOWN_WARNING=1 exec " + shellQuote(os.Args[0]) + " -test.run=TestFakeGoplsShutdownWarningHelper -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake gopls: %v", err)
	}
	return dir
}

func runFakeGoplsShutdownWarningLangserver() {
	reader := bufio.NewReader(os.Stdin)
	var goroutines sync.WaitGroup
	defer goroutines.Wait()
	writer := &fakeLSPWriter{w: os.Stdout, goroutines: &goroutines}
	for {
		raw, err := readFakeLSPFramedMessage(reader)
		if err != nil {
			return
		}
		var req fakeLSPRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			continue
		}
		if req.Method == "exit" {
			return
		}
		if fakeGoplsHandleNotification(writer, req) {
			continue
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			continue
		}
		if fakeGoplsRequiresE2EBuildFlag(req) && !fakeGoplsInitializeHasBuildTag(req.Params, "e2e") {
			_ = writer.writeError(req.ID, -32002, "missing gopls buildFlags -tags=e2e")
			continue
		}
		_ = writer.writeResponse(req.ID, fakeGoplsResult(req))
	}
}

func fakeGoplsRequiresE2EBuildFlag(req fakeLSPRequest) bool {
	return req.Method == "initialize" && os.Getenv(fakeGoplsRequireE2EBuildFlagEnv) == "1"
}

func fakeGoplsInitializeHasBuildTag(raw json.RawMessage, tag string) bool {
	if buildFlagsContainTag(strings.Fields(os.Getenv("GOFLAGS")), tag) {
		return true
	}
	var params struct {
		InitializationOptions struct {
			BuildFlags []string `json:"buildFlags"`
			Settings   struct {
				BuildFlags []string `json:"buildFlags"`
				Gopls      struct {
					BuildFlags []string `json:"buildFlags"`
				} `json:"gopls"`
			} `json:"settings"`
		} `json:"initializationOptions"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return false
	}
	for _, flags := range [][]string{
		params.InitializationOptions.BuildFlags,
		params.InitializationOptions.Settings.BuildFlags,
		params.InitializationOptions.Settings.Gopls.BuildFlags,
	} {
		if buildFlagsContainTag(flags, tag) {
			return true
		}
	}
	return false
}

func buildFlagsContainTag(flags []string, tag string) bool {
	for _, flag := range flags {
		for _, part := range strings.Split(flag, "=") {
			if part == tag || strings.Contains(part, tag+",") || strings.Contains(part, ","+tag) {
				return true
			}
		}
	}
	return false
}

func fakeGoplsHandleNotification(writer *fakeLSPWriter, req fakeLSPRequest) bool {
	if len(bytes.TrimSpace(req.ID)) != 0 || req.Method != "textDocument/didOpen" {
		return false
	}
	var params fakeLSPDidOpenParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return true
	}
	uri := strings.TrimSpace(params.TextDocument.URI)
	if uri == "" {
		return true
	}
	if os.Getenv(fakeGoplsPullDiagnosticsOnlyEnv) == "1" {
		return true
	}
	writer.goAsync(func() {
		_ = writer.writeNotification("window/logMessage", map[string]any{
			"type":    1,
			"message": fakeGoplsOrphanedFilesWarning,
		})
		_ = writer.writeNotification("textDocument/publishDiagnostics", map[string]any{
			"uri":         uri,
			"diagnostics": []any{},
		})
	})
	return true
}

func fakeGoplsResult(req fakeLSPRequest) any {
	switch req.Method {
	case "initialize":
		capabilities := map[string]any{
			"textDocumentSync":       1,
			"hoverProvider":          true,
			"documentSymbolProvider": true,
		}
		if os.Getenv(fakeGoplsSuppressDiagnosticProviderEnv) != "1" {
			capabilities["diagnosticProvider"] = map[string]any{
				"interFileDependencies": true,
				"workspaceDiagnostics":  false,
			}
		}
		return map[string]any{"capabilities": capabilities}
	case "textDocument/documentSymbol":
		return []map[string]any{{
			"name": "main",
			"kind": 12,
			"range": map[string]any{
				"start": map[string]any{"line": 2, "character": 0},
				"end":   map[string]any{"line": 2, "character": 14},
			},
			"selectionRange": map[string]any{
				"start": map[string]any{"line": 2, "character": 5},
				"end":   map[string]any{"line": 2, "character": 9},
			},
		}}
	case "textDocument/hover":
		return map[string]any{
			"contents": map[string]any{
				"kind":  "markdown",
				"value": "```go\nfunc main()\n```",
			},
		}
	case "textDocument/diagnostic":
		return map[string]any{
			"kind":  "full",
			"items": []any{},
		}
	case "shutdown":
		return nil
	default:
		return nil
	}
}

func waitForFakeGoplsWarningStderr(t *testing.T, client *mcpLSPBinaryClient) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		stderr := client.stderrString()
		if strings.Contains(stderr, fakeGoplsOrphanedFilesWarning) {
			return stderr
		}
		if time.Now().After(deadline) {
			t.Fatalf("stderr never received fake gopls warning %q; stderr=%s", fakeGoplsOrphanedFilesWarning, stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
