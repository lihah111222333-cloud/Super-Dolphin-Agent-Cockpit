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

func TestMcpLSPBinaryFakeServerDiagnosticsColdStartCoversAllLSPClientLanguages_E2E(t *testing.T) {
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

func TestMcpLSPBinaryDiagnosticsReopensChangedFileBeforeReturning_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	root := t.TempDir()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"diagnostics-reopen"}`)
	target := writeBinaryColdStartFile(t, root, "app.js", "function staleName() { return 1 }\n")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	first := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, first, "initial stale-name diagnostics")
	firstMessage := decodeDiagnosticsStructuredContent(t, first.Result.StructuredContent).FirstMessageForFile(t, target)
	if !strings.Contains(firstMessage, "staleName") {
		t.Fatalf("initial diagnostics message = %q, want staleName; stderr=%s", firstMessage, client.stderrString())
	}

	if err := os.WriteFile(target, []byte("function freshName() { return 2 }\n"), 0o600); err != nil {
		t.Fatalf("rewrite diagnostics target: %v", err)
	}
	second := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, second, "fresh-name diagnostics after rewrite")
	secondMessage := decodeDiagnosticsStructuredContent(t, second.Result.StructuredContent).FirstMessageForFile(t, target)
	if !strings.Contains(secondMessage, "freshName") || strings.Contains(secondMessage, "staleName") {
		t.Fatalf("diagnostics after rewrite = %q, want freshName without staleName; stderr=%s", secondMessage, client.stderrString())
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
		{languageID: "c", write: writeBinaryColdStartCFixture},
		{languageID: "cpp", write: writeBinaryColdStartCPPFixture},
		{languageID: "csharp", write: writeBinaryColdStartCSharpFixture},
		{languageID: "dart", write: writeBinaryColdStartDartFixture},
		{languageID: "dockerfile", write: writeBinaryColdStartDockerFixture},
		{languageID: "go", write: writeBinaryColdStartGoFixture},
		{languageID: "gomod", write: writeBinaryColdStartGoModFixture},
		{languageID: "gosum", write: writeBinaryColdStartGoSumFixture},
		{languageID: "gowork", write: writeBinaryColdStartGoWorkFixture},
		{languageID: "graphql", write: writeBinaryColdStartGraphQLFixture},
		{languageID: "html", write: writeBinaryColdStartHTMLFixture},
		{languageID: "java", write: writeBinaryColdStartJavaFixture},
		{languageID: "javascript", write: writeBinaryColdStartJavaScriptFixture},
		{languageID: "javascriptreact", write: writeBinaryColdStartJavaScriptReactFixture},
		{languageID: "json", write: writeBinaryColdStartJSONFixture},
		{languageID: "kotlin", write: writeBinaryColdStartKotlinFixture},
		{languageID: "lua", write: writeBinaryColdStartLuaFixture},
		{languageID: "markdown", write: writeBinaryColdStartMarkdownFixture},
		{languageID: "objective-c", write: writeBinaryColdStartObjectiveCFixture},
		{languageID: "objective-cpp", write: writeBinaryColdStartObjectiveCPPFixture},
		{languageID: "php", write: writeBinaryColdStartPHPFixture},
		{languageID: "prisma", write: writeBinaryColdStartPrismaFixture},
		{languageID: "python", write: writeBinaryColdStartPythonFixture},
		{languageID: "ruby", write: writeBinaryColdStartRubyFixture},
		{languageID: "rust", write: writeBinaryColdStartRustFixture},
		{languageID: "shellscript", write: writeBinaryColdStartShellFixture},
		{languageID: "sql", write: writeBinaryColdStartSQLFixture},
		{languageID: "svelte", write: writeBinaryColdStartSvelteFixture},
		{languageID: "swift", write: writeBinaryColdStartSwiftFixture},
		{languageID: "terraform", write: writeBinaryColdStartTerraformFixture},
		{languageID: "typescript", write: writeBinaryColdStartTypeScriptFixture},
		{languageID: "typescriptreact", write: writeBinaryColdStartTypeScriptReactFixture},
		{languageID: "vue", write: writeBinaryColdStartVueFixture},
		{languageID: "yaml", write: writeBinaryColdStartYAMLFixture},
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
		"clangd",
		"csharp-ls",
		"dart",
		"docker-langserver",
		"graphql-lsp",
		"gopls",
		"intelephense",
		"jdtls",
		"kotlin-language-server",
		"lua-language-server",
		"pyright-langserver",
		"prisma-language-server",
		"rust-analyzer",
		"sql-language-server",
		"sourcekit-lsp",
		"solargraph",
		"svelteserver",
		"terraform-ls",
		"typescript-language-server",
		"vscode-css-language-server",
		"vscode-html-language-server",
		"vscode-json-language-server",
		"vscode-markdown-language-server",
		"vue-language-server",
		"yaml-language-server",
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
		opened: make(map[string]fakeMultilangOpenedDocument),
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
	opened map[string]fakeMultilangOpenedDocument
}

type fakeMultilangOpenedDocument struct {
	languageID string
	text       string
}

type fakeMultilangDidOpenParams struct {
	TextDocument struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
		Text       string `json:"text"`
	} `json:"textDocument"`
}

type fakeMultilangDidCloseParams struct {
	TextDocument struct {
		URI string `json:"uri"`
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
	if req.Method == "textDocument/didClose" {
		var params fakeMultilangDidCloseParams
		if err := json.Unmarshal(req.Params, &params); err == nil {
			s.mu.Lock()
			delete(s.opened, strings.TrimSpace(params.TextDocument.URI))
			s.mu.Unlock()
		}
		return true
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
	if _, alreadyOpen := s.opened[uri]; !alreadyOpen {
		s.opened[uri] = fakeMultilangOpenedDocument{
			languageID: languageID,
			text:       params.TextDocument.Text,
		}
	}
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
		uri, document := s.diagnosticTarget(req)
		return map[string]any{
			"kind":  "full",
			"items": fakeMultilangDiagnostics(uri, document),
		}
	case "shutdown":
		return nil
	default:
		return nil
	}
}

func (s *fakeMultilangDiagnosticsServer) diagnosticTarget(req fakeLSPRequest) (string, fakeMultilangOpenedDocument) {
	var params fakeMultilangDiagnosticParams
	_ = json.Unmarshal(req.Params, &params)
	uri := strings.TrimSpace(params.TextDocument.URI)
	s.mu.Lock()
	defer s.mu.Unlock()
	document := s.opened[uri]
	if strings.TrimSpace(document.languageID) == "" {
		document.languageID = "unknown"
	}
	return uri, document
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

func fakeMultilangDiagnostics(uri string, document fakeMultilangOpenedDocument) []map[string]any {
	return []map[string]any{{
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": 0, "character": 1},
		},
		"severity": 1,
		"source":   "fake-" + document.languageID,
		"message":  fmt.Sprintf("fake cold-start diagnostic for %s in %s: %s", document.languageID, filepath.Base(uri), strings.TrimSpace(document.text)),
		"code":     "cold-start",
	}}
}

func writeBinaryColdStartCSSFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-css"}`)
	return writeBinaryColdStartFile(t, root, "style.css", "body { color: black; }\n")
}

func writeBinaryColdStartHTMLFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-html"}`)
	return writeBinaryColdStartFile(t, root, "index.html", "<main>Hello</main>\n")
}

func writeBinaryColdStartJSONFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-json"}`+"\n")
}

func writeBinaryColdStartYAMLFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "config.yaml", "name: binary-cold-yaml\n")
}

func writeBinaryColdStartMarkdownFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "README.md", "# Binary Cold Markdown\n")
}

func writeBinaryColdStartVueFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-vue"}`)
	return writeBinaryColdStartFile(t, root, "App.vue", "<template><main>Hello</main></template>\n")
}

func writeBinaryColdStartSvelteFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-svelte"}`)
	return writeBinaryColdStartFile(t, root, "App.svelte", "<main>Hello</main>\n")
}

func writeBinaryColdStartCFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.c", "int main(void) { return 0; }\n")
}

func writeBinaryColdStartCPPFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.cpp", "int main() { return 0; }\n")
}

func writeBinaryColdStartObjectiveCFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.m", "int main(void) { return 0; }\n")
}

func writeBinaryColdStartObjectiveCPPFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeBinaryColdStartFile(t, root, "main.mm", "int main() { return 0; }\n")
}

func writeBinaryColdStartSwiftFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Package.swift", "// swift-tools-version: 6.0\n")
	return writeBinaryColdStartFile(t, root, "Sources/App/main.swift", "print(\"hello\")\n")
}

func writeBinaryColdStartCSharpFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "global.json", `{"sdk":{"rollForward":"latestFeature"}}`)
	writeBinaryColdStartFile(t, root, "App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`)
	return writeBinaryColdStartFile(t, root, "Program.cs", "class Program { static void Main() {} }\n")
}

func writeBinaryColdStartPHPFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "composer.json", `{"name":"example/binary-cold-php"}`)
	return writeBinaryColdStartFile(t, root, "index.php", "<?php echo 'hello';\n")
}

func writeBinaryColdStartRubyFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "Gemfile", "source 'https://rubygems.org'\n")
	return writeBinaryColdStartFile(t, root, "app.rb", "puts 'hello'\n")
}

func writeBinaryColdStartKotlinFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "settings.gradle.kts", "pluginManagement {}\n")
	return writeBinaryColdStartFile(t, root, "src/Main.kt", "fun main() {}\n")
}

func writeBinaryColdStartDartFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "pubspec.yaml", "name: binary_cold_dart\n")
	return writeBinaryColdStartFile(t, root, "lib/main.dart", "void main() {}\n")
}

func writeBinaryColdStartLuaFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, ".luarc.json", "{}\n")
	return writeBinaryColdStartFile(t, root, "init.lua", "local value = 1\n")
}

func writeBinaryColdStartDockerFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "Dockerfile", "FROM scratch\n")
}

func writeBinaryColdStartTerraformFixture(t *testing.T, root string) string {
	t.Helper()
	return writeBinaryColdStartFile(t, root, "main.tf", "terraform {}\n")
}

func writeBinaryColdStartGraphQLFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-graphql"}`)
	return writeBinaryColdStartFile(t, root, "schema.graphql", "type Query { hello: String }\n")
}

func writeBinaryColdStartPrismaFixture(t *testing.T, root string) string {
	t.Helper()
	writeBinaryColdStartFile(t, root, "package.json", `{"name":"binary-cold-prisma"}`)
	return writeBinaryColdStartFile(t, root, "schema.prisma", "datasource db { provider = \"sqlite\" url = \"file:dev.db\" }\n")
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
