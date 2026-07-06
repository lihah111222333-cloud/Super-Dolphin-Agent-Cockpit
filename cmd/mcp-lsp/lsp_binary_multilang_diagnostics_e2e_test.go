//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
)

const (
	fakeMultilangDiagnosticsEnv     = "MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS"
	fakeMultilangDiagnosticDelayEnv = "MCP_LSP_FAKE_MULTILANG_DIAGNOSTIC_DELAY"
	binaryColdStartDiagnosticsDelay = 1750 * time.Millisecond
	binaryColdStartDiagnosticsSlack = 250 * time.Millisecond
)

func TestMcpLSPBinaryDiagnosticsColdStartCoversAllLSPClientLanguages_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)

	for _, tc := range binaryColdStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			root := t.TempDir()
			target := tc.write(t, root)

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, []string{
				fakeMultilangDiagnosticDelayEnv + "=" + binaryColdStartDiagnosticsDelay.String(),
			})
			defer client.close(t)

			client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

			startedAt := time.Now()
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			elapsed := time.Since(startedAt)
			requireMCPToolSuccess(t, client, diagnostics, tc.languageID+" diagnostics")
			if elapsed < binaryColdStartDiagnosticsDelay-binaryColdStartDiagnosticsSlack {
				t.Fatalf("%s diagnostics returned in %s, want it to wait for delayed cold-start diagnostics >= %s; structured=%s stderr=%s",
					tc.languageID, elapsed, binaryColdStartDiagnosticsDelay-binaryColdStartDiagnosticsSlack,
					diagnostics.Result.StructuredContent, client.stderrString())
			}

			payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
			if !payload.HasFile(target) {
				t.Fatalf("%s diagnostics missing target %s: payload=%#v raw=%s text=%q stderr=%s",
					tc.languageID, target, payload, diagnostics.Result.StructuredContent,
					diagnostics.Result.ContentText(), client.stderrString())
			}
			message := payload.FirstMessageForFile(t, target)
			want := "fake cold-start diagnostic for " + tc.languageID
			if !strings.Contains(message, want) {
				t.Fatalf("%s diagnostics message = %q, want %q; payload=%#v raw=%s stderr=%s",
					tc.languageID, message, want, payload, diagnostics.Result.StructuredContent, client.stderrString())
			}
		})
	}
}

func TestFakeMultilangDiagnosticsLangserverHelper(t *testing.T) {
	if os.Getenv(fakeMultilangDiagnosticsEnv) != "1" {
		return
	}
	runFakeMultilangDiagnosticsLangserver()
	os.Exit(0)
}

type binaryColdStartLanguageCase struct {
	languageID string
	write      func(t *testing.T, root string) string
}

func binaryColdStartLanguageCases(t *testing.T) []binaryColdStartLanguageCase {
	t.Helper()
	cases := []binaryColdStartLanguageCase{
		{languageID: "css", write: writeBinaryColdStartCSSFixture},
		{languageID: "go", write: writeBinaryColdStartGoFixture},
		{languageID: "gomod", write: writeBinaryColdStartGoModFixture},
		{languageID: "gosum", write: writeBinaryColdStartGoSumFixture},
		{languageID: "gowork", write: writeBinaryColdStartGoWorkFixture},
		{languageID: "java", write: writeBinaryColdStartJavaFixture},
		{languageID: "javascript", write: writeBinaryColdStartJavaScriptFixture},
		{languageID: "javascriptreact", write: writeBinaryColdStartJavaScriptReactFixture},
		{languageID: "python", write: writeBinaryColdStartPythonFixture},
		{languageID: "rust", write: writeBinaryColdStartRustFixture},
		{languageID: "shellscript", write: writeBinaryColdStartShellFixture},
		{languageID: "sql", write: writeBinaryColdStartSQLFixture},
		{languageID: "typescript", write: writeBinaryColdStartTypeScriptFixture},
		{languageID: "typescriptreact", write: writeBinaryColdStartTypeScriptReactFixture},
	}
	assertBinaryColdStartCasesCoverDefaultLSPClientLanguages(t, cases)
	return cases
}

func assertBinaryColdStartCasesCoverDefaultLSPClientLanguages(t *testing.T, cases []binaryColdStartLanguageCase) {
	t.Helper()
	got := make([]string, 0, len(cases))
	for _, tc := range cases {
		got = append(got, tc.languageID)
	}
	slices.Sort(got)
	want := defaultBinaryLSPClientLanguageIDs(t)
	if !slices.Equal(got, want) {
		t.Fatalf("binary cold-start language coverage = %#v, want default LSP client languages %#v", got, want)
	}
}

func defaultBinaryLSPClientLanguageIDs(t *testing.T) []string {
	t.Helper()
	registry := multilsp.NewDefaultLanguageAdapterRegistry()
	ids := make([]string, 0)
	for _, languageID := range registry.LanguageIDs() {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("missing adapter for default language %q", languageID)
		}
		if adapter.CapabilityPolicy().RequiresLSPClient {
			ids = append(ids, languageID)
		}
	}
	slices.Sort(ids)
	return ids
}

func writeFakeMultilangDiagnosticsLangservers(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		fakeMultilangDiagnosticsEnv + "=1 exec " + shellQuote(os.Args[0]) +
		" -test.run=TestFakeMultilangDiagnosticsLangserverHelper -- \"$@\"\n"
	for _, name := range []string{
		"bash-language-server",
		"gopls",
		"jdtls",
		"pyright-langserver",
		"rust-analyzer",
		"sql-language-server",
		"typescript-language-server",
		"vscode-css-language-server",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return dir
}

func runFakeMultilangDiagnosticsLangserver() {
	reader := bufio.NewReader(os.Stdin)
	var goroutines sync.WaitGroup
	defer goroutines.Wait()
	server := &fakeMultilangDiagnosticsServer{
		writer: &fakeLSPWriter{w: os.Stdout, goroutines: &goroutines},
		opened: make(map[string]string),
	}
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
		if server.handleNotification(req) {
			continue
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			continue
		}
		_ = server.writer.writeResponse(req.ID, server.result(req))
	}
}

type fakeMultilangDiagnosticsServer struct {
	mu     sync.Mutex
	writer *fakeLSPWriter
	opened map[string]string
}

type fakeMultilangDidOpenParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
	} `json:"textDocument"`
}

type fakeMultilangDiagnosticParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

func (s *fakeMultilangDiagnosticsServer) handleNotification(req fakeLSPRequest) bool {
	if len(bytes.TrimSpace(req.ID)) != 0 {
		return false
	}
	if req.Method != "textDocument/didOpen" {
		return true
	}
	var params fakeMultilangDidOpenParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return true
	}
	uri := strings.TrimSpace(params.TextDocument.URI)
	languageID := strings.TrimSpace(params.TextDocument.LanguageID)
	if uri == "" || languageID == "" {
		return true
	}
	s.mu.Lock()
	s.opened[uri] = languageID
	s.mu.Unlock()
	return true
}

func (s *fakeMultilangDiagnosticsServer) result(req fakeLSPRequest) any {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync": 1,
				"diagnosticProvider": map[string]any{
					"interFileDependencies": true,
					"workspaceDiagnostics":  false,
				},
			},
		}
	case "textDocument/diagnostic":
		if delay := fakeMultilangDiagnosticDelay(); delay > 0 {
			time.Sleep(delay)
		}
		uri, languageID := s.diagnosticTarget(req)
		return map[string]any{
			"kind":  "full",
			"items": fakeMultilangDiagnostics(uri, languageID),
		}
	case "shutdown":
		return nil
	default:
		return nil
	}
}

func (s *fakeMultilangDiagnosticsServer) diagnosticTarget(req fakeLSPRequest) (string, string) {
	var params fakeMultilangDiagnosticParams
	_ = json.Unmarshal(req.Params, &params)
	uri := strings.TrimSpace(params.TextDocument.URI)
	s.mu.Lock()
	defer s.mu.Unlock()
	languageID := strings.TrimSpace(s.opened[uri])
	if languageID == "" {
		languageID = "unknown"
	}
	return uri, languageID
}

func fakeMultilangDiagnosticDelay() time.Duration {
	raw := strings.TrimSpace(os.Getenv(fakeMultilangDiagnosticDelayEnv))
	if raw == "" {
		return 0
	}
	delay, err := time.ParseDuration(raw)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "invalid %s %q: %v\n", fakeMultilangDiagnosticDelayEnv, raw, err)
		os.Exit(2)
	}
	return delay
}

func fakeMultilangDiagnostics(uri, languageID string) []map[string]any {
	return []map[string]any{{
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": 0, "character": 1},
		},
		"severity": 1,
		"source":   "fake-" + languageID,
		"message":  fmt.Sprintf("fake cold-start diagnostic for %s in %s", languageID, filepath.Base(uri)),
		"code":     "cold-start",
	}}
}

func writeBinaryColdStartCSSFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-css"}`)
	return writeBinaryColdStartFile(t, root, "style.css", "body { color: black; }\n")
}

func writeBinaryColdStartGoFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "go.mod", "module example.test/binarycoldgo\n\ngo 1.25.0\n")
	return writeBinaryColdStartFile(t, root, "main.go", "package main\n")
}

func writeBinaryColdStartGoModFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "go.mod", "module example.test/binarycoldgomod\n\ngo 1.25.0\n")
}

func writeBinaryColdStartGoSumFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "go.mod", "module example.test/binarycoldgosum\n\ngo 1.25.0\n")
	return writeBinaryColdStartFile(t, root, "go.sum", "")
}

func writeBinaryColdStartGoWorkFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "module/go.mod", "module example.test/binarycoldgowork\n\ngo 1.25.0\n")
	return writeBinaryColdStartFile(t, root, "go.work", "go 1.25.0\n\nuse ./module\n")
}

func writeBinaryColdStartJavaFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "pom.xml", "<project></project>\n")
	return writeBinaryColdStartFile(t, root, "src/Main.java", "class Main {}\n")
}

func writeBinaryColdStartJavaScriptFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-js"}`)
	return writeBinaryColdStartFile(t, root, "app.js", "export const value = 1\n")
}

func writeBinaryColdStartJavaScriptReactFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-jsx"}`)
	return writeBinaryColdStartFile(t, root, "app.jsx", "export const View = () => null\n")
}

func writeBinaryColdStartPythonFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "pyproject.toml", "[project]\nname = \"binary-cold-python\"\n")
	return writeBinaryColdStartFile(t, root, "app.py", "value = 1\n")
}

func writeBinaryColdStartRustFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Cargo.toml", "[package]\nname = \"binary_cold_rust\"\nversion = \"0.1.0\"\n")
	return writeBinaryColdStartFile(t, root, "src/main.rs", "fn main() {}\n")
}

func writeBinaryColdStartShellFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Makefile", "all:\n\t@true\n")
	return writeBinaryColdStartFile(t, root, "scripts/run.sh", "#!/usr/bin/env bash\n")
}

func writeBinaryColdStartSQLFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "sqlc.yaml", "version: '2'\n")
	return writeBinaryColdStartFile(t, root, "schema.sql", "select 1;\n")
}

func writeBinaryColdStartTypeScriptFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "tsconfig.json", `{"compilerOptions":{}}`)
	return writeBinaryColdStartFile(t, root, "app.ts", "export const value: number = 1\n")
}

func writeBinaryColdStartTypeScriptReactFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "tsconfig.json", `{"compilerOptions":{"jsx":"react-jsx"}}`)
	return writeBinaryColdStartFile(t, root, "app.tsx", "export const View = () => null\n")
}

func writeBinaryColdStartFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}
