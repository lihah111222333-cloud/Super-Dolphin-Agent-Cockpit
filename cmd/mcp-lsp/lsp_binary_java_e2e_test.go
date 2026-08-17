//go:build e2e

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMcpLSPBinaryJavaToolsAndAndroidClasspathDiagnostics_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writeJavaAndroidClasspathFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeJDTLSBinDir := writeFakeJDTLSLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeJDTLSBinDir, nil)
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	read := client.callTool(t, "file", map[string]any{
		"action": "read_file",
		"pos":    target + ":1",
		"limit":  20,
		"scope":  "lines",
	})
	requireMCPToolSuccess(t, client, read, "java read_file")
	requireToolTextContains(t, read, "MainActivity", "java read_file")

	structure := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, structure, "java document_symbol")
	requireToolResultContains(t, structure, "MainActivity", "java document_symbol")

	hover := client.callTool(t, "inspect", map[string]any{
		"action": "hover",
		"pos":    target + ":5:42",
	})
	requireMCPToolSuccess(t, client, hover, "java hover")
	requireToolResultContains(t, hover, "android.app.Activity", "java hover")

	definition := client.callTool(t, "inspect", map[string]any{
		"action": "definition",
		"pos":    target + ":5:14",
	})
	requireMCPToolSuccess(t, client, definition, "java definition")
	requireGroupedLocationTextTotal(t, definition, 1, "java definition")
	requireToolResultContains(t, definition, "MainActivity.java", "java definition")

	completion := client.callTool(t, "completion", map[string]any{
		"pos":         target + ":7:12",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, completion, "java completion")
	if !stringSliceContains(completionLabelsFromContent(t, completion), "onCreate") {
		t.Fatalf("java completion missing onCreate; text=%q stderr=%s",
			completion.Result.ContentText(), client.stderrString())
	}

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "java diagnostics")
	payload := decodeDiagnosticsContentText(t, diagnostics.Result.ContentText())
	message := payload.FirstMessageForFile(t, target)
	if !javaAndroidClasspathMissingMessage(message) {
		t.Fatalf("java diagnostics message = %q, want Android classpath missing error; text=%q stderr=%s",
			message, diagnostics.Result.ContentText(), client.stderrString())
	}
}

func TestMcpLSPBinaryJavaDocumentSymbolSkipsDiagnosticsReadiness_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	root := t.TempDir()
	target := writeJavaAndroidClasspathFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeJDTLSBinDir := writeFakeJDTLSLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeJDTLSBinDir, []string{
		"MCP_LSP_FAKE_JDTLS_SUPPRESS_DIAGNOSTICS=1",
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	structure := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, structure, "java document_symbol without diagnostics")
	requireToolResultContains(t, structure, "MainActivity", "java document_symbol without diagnostics")
}

func javaAndroidClasspathMissingMessage(message string) bool {
	return strings.Contains(message, "The import android cannot be resolved") ||
		strings.Contains(message, "Activity cannot be resolved to a type") ||
		(strings.Contains(message, "Android classpath missing") && strings.Contains(message, "android.app.Activity"))
}

// super-dolphin-ci: helper
func TestFakeJDTLSLangserverHelper(t *testing.T) {
	if os.Getenv("MCP_LSP_FAKE_JDTLS") != "1" {
		return
	}
	runFakeJDTLSLangserver()
	os.Exit(0)
}

func writeFakeJDTLSLangserver(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "jdtls")
	script := "#!/bin/sh\nMCP_LSP_FAKE_JDTLS=1 exec " + shellQuote(os.Args[0]) + " -test.run=TestFakeJDTLSLangserverHelper -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake jdtls: %v", err)
	}
	return dir
}

func runFakeJDTLSLangserver() {
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
		if fakeJDTLSHandleNotification(writer, req) {
			continue
		}
		if strings.TrimSpace(string(req.ID)) == "" {
			continue
		}
		_ = writer.writeResponse(req.ID, fakeJDTLSResult(req))
	}
}

func fakeJDTLSHandleNotification(writer *fakeLSPWriter, req fakeLSPRequest) bool {
	if strings.TrimSpace(string(req.ID)) != "" {
		return false
	}
	if req.Method != "textDocument/didOpen" {
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
	if os.Getenv("MCP_LSP_FAKE_JDTLS_SUPPRESS_DIAGNOSTICS") == "1" {
		return true
	}
	writer.goAsync(func() {
		_ = writer.writeNotification("textDocument/publishDiagnostics", fakeJDTLSDiagnostics(uri))
	})
	return true
}

func fakeJDTLSResult(req fakeLSPRequest) any {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":       1,
				"documentSymbolProvider": true,
				"hoverProvider":          true,
				"definitionProvider":     true,
				"completionProvider": map[string]any{
					"triggerCharacters": []string{"."},
				},
			},
		}
	case "shutdown":
		return nil
	case "textDocument/documentSymbol":
		return fakeJDTLSDocumentSymbols()
	case "textDocument/hover":
		return map[string]any{
			"contents": map[string]any{
				"kind":  "markdown",
				"value": "```java\nandroid.app.Activity\n```",
			},
		}
	case "textDocument/definition":
		params := decodeFakeJavaPositionParams(req.Params)
		return []map[string]any{{
			"uri": params.TextDocument.URI,
			"range": map[string]any{
				"start": map[string]any{"line": 4, "character": 13},
				"end":   map[string]any{"line": 4, "character": 25},
			},
		}}
	case "textDocument/completion":
		return map[string]any{
			"isIncomplete": false,
			"items": []map[string]any{{
				"label":  "onCreate",
				"kind":   2,
				"detail": "void onCreate()",
			}},
		}
	default:
		return nil
	}
}

type fakeJavaPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

func decodeFakeJavaPositionParams(raw json.RawMessage) fakeJavaPositionParams {
	var params fakeJavaPositionParams
	_ = json.Unmarshal(raw, &params)
	return params
}

func fakeJDTLSDocumentSymbols() []map[string]any {
	return []map[string]any{{
		"name": "MainActivity",
		"kind": 5,
		"range": map[string]any{
			"start": map[string]any{"line": 4, "character": 0},
			"end":   map[string]any{"line": 8, "character": 1},
		},
		"selectionRange": map[string]any{
			"start": map[string]any{"line": 4, "character": 13},
			"end":   map[string]any{"line": 4, "character": 25},
		},
		"children": []map[string]any{{
			"name": "onCreate",
			"kind": 6,
			"range": map[string]any{
				"start": map[string]any{"line": 5, "character": 4},
				"end":   map[string]any{"line": 7, "character": 5},
			},
			"selectionRange": map[string]any{
				"start": map[string]any{"line": 5, "character": 16},
				"end":   map[string]any{"line": 5, "character": 24},
			},
		}},
	}}
}

func fakeJDTLSDiagnostics(uri string) map[string]any {
	return map[string]any{
		"uri": uri,
		"diagnostics": []map[string]any{
			{
				"range": map[string]any{
					"start": map[string]any{"line": 2, "character": 7},
					"end":   map[string]any{"line": 2, "character": 14},
				},
				"severity": 1,
				"source":   "Java",
				"message":  "The import android cannot be resolved",
				"code":     "268435846",
			},
			{
				"range": map[string]any{
					"start": map[string]any{"line": 4, "character": 34},
					"end":   map[string]any{"line": 4, "character": 42},
				},
				"severity": 1,
				"source":   "Java",
				"message":  "Activity cannot be resolved to a type",
				"code":     "16777218",
			},
		},
	}
}

func writeJavaAndroidClasspathFixture(t *testing.T, root string) string {
	t.Helper()
	pom := filepath.Join(root, "pom.xml")
	if err := os.WriteFile(pom, []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="http://maven.apache.org/POM/4.0.0 https://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>android-classpath-missing</artifactId>
  <version>1.0.0</version>
</project>
`), 0o600); err != nil {
		t.Fatalf("write Java pom fixture: %v", err)
	}
	target := filepath.Join(root, "src", "main", "java", "com", "example", "MainActivity.java")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir Java fixture dir: %v", err)
	}
	content := `package com.example;

import android.app.Activity;

public class MainActivity extends Activity {
    public void onCreate() {
        this.toString();
    }
}
`
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write Java fixture: %v", err)
	}
	return target
}

func requireMCPToolSuccess(t *testing.T, client *mcpLSPBinaryClient, response mcpLSPBinaryResponse, label string) {
	t.Helper()
	if err := validateMCPToolSuccessResult(response); err != nil {
		t.Fatalf("%s returned invalid content-only result: %v; text=%q stderr=%s",
			label, err, response.Result.ContentText(), client.stderrString())
	}
}

func requireToolTextContains(t *testing.T, response mcpLSPBinaryResponse, want string, label string) {
	t.Helper()
	if !strings.Contains(response.Result.ContentText(), want) {
		t.Fatalf("%s text missing %q; text=%q", label, want, response.Result.ContentText())
	}
}

func requireToolResultContains(t *testing.T, response mcpLSPBinaryResponse, want string, label string) {
	t.Helper()
	if !strings.Contains(response.Result.ContentText(), want) {
		t.Fatalf("%s result missing %q; text=%q", label, want, response.Result.ContentText())
	}
}
