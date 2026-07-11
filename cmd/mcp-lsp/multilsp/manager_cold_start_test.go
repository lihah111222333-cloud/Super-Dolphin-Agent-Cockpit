package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// coldStartDiagnosticsMaxWait 只给 race 模式下的 goroutine 调度留余量；精确 deadline 另有专门测试覆盖。
const coldStartDiagnosticsMaxWait = 500 * time.Millisecond

func TestWaitDiagnosticsStableWaitsForDelayedColdStartDiagnostics(t *testing.T) {
	for _, tc := range coldStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			root := t.TempDir()
			target := tc.write(t, root)
			mgr := newDiagnosticsTestManager(t, Config{
				WorkspaceRoot:                    root,
				DiagnosticsInitialDelay:          time.Millisecond,
				DiagnosticsPollInterval:          time.Millisecond,
				DiagnosticsMaxWait:               coldStartDiagnosticsMaxWait,
				DisableInitialWorkspaceBootstrap: true,
			})
			ctx, cancel := context.WithTimeout(common.WithToolScope(context.Background(), common.ToolScope{
				CWD:            root,
				WorkspaceRoots: []string{root},
				Family:         "lsp",
			}), 5*time.Second)
			goroutines := newTestGoroutineGroup(t)
			defer func() {
				cancel()
			}()
			uri, _ := resolveDiagnosticsScopeForTarget(t, mgr, ctx, target, "cold-start")
			published := make(chan struct{})
			goroutines.Go(func() {
				select {
				case <-time.After(30 * time.Millisecond):
					_ = mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri})
					close(published)
				case <-ctx.Done():
				}
			})

			if err := mgr.WaitDiagnosticsStable(ctx, []string{uri}); err != nil {
				t.Fatalf("WaitDiagnosticsStable() error = %v, want delayed cold-start diagnostics to satisfy readiness", err)
			}
			select {
			case <-published:
			default:
				t.Fatal("WaitDiagnosticsStable returned before delayed diagnostics were published")
			}
		})
	}
}

func TestDefinitionWaitsForColdStartDiagnosticsBeforeRequest(t *testing.T) {
	for _, tc := range coldStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			root := t.TempDir()
			target := tc.write(t, root)
			factory := &coldStartDefinitionFactory{readyDelay: 30 * time.Millisecond}
			mgr := NewManager(Config{
				WorkspaceRoot:                    root,
				ClientFactory:                    factory,
				DiagnosticsInitialDelay:          time.Millisecond,
				DiagnosticsPollInterval:          time.Millisecond,
				DiagnosticsMaxWait:               coldStartDiagnosticsMaxWait,
				DisableInitialWorkspaceBootstrap: true,
			}).(*manager)
			defer func() { _ = mgr.Close() }()
			ctx, cancel := context.WithTimeout(common.WithToolScope(context.Background(), common.ToolScope{
				CWD:            root,
				WorkspaceRoots: []string{root},
				Family:         "lsp",
			}), 5*time.Second)
			defer cancel()

			defs, err := mgr.Definition(ctx, target, protocol.Position{Line: 0, Character: 0})
			if err != nil {
				t.Fatalf("Definition() error = %v, want delayed cold-start diagnostics to make definition ready", err)
			}
			if len(defs) != 1 {
				t.Fatalf("Definition() results = %#v, want one location after diagnostics readiness", defs)
			}
			client := factory.clientAt(t)
			if got := client.beforeReadyRequestCount(); got != 0 {
				t.Fatalf("definition request raced cold-start diagnostics %d time(s)", got)
			}
			if got := client.openedLanguageID(); got != tc.languageID {
				t.Fatalf("DidOpen language ID = %q, want %q", got, tc.languageID)
			}
		})
	}
}

func TestDocumentSymbolDoesNotWaitForColdStartDiagnostics(t *testing.T) {
	root := t.TempDir()
	target := writeColdStartJavaScriptFixture(t, root)
	factory := &coldStartDefinitionFactory{readyDelay: 200 * time.Millisecond}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		DiagnosticsInitialDelay:          time.Millisecond,
		DiagnosticsPollInterval:          time.Millisecond,
		DiagnosticsMaxWait:               time.Second,
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx, cancel := context.WithTimeout(common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Family:         "lsp",
	}), 80*time.Millisecond)
	defer cancel()

	symbols, err := mgr.DocumentSymbol(ctx, target)
	if err != nil {
		t.Fatalf("DocumentSymbol() error = %v, want first outline request to skip diagnostics readiness", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "value" {
		t.Fatalf("DocumentSymbol() symbols = %#v, want value outline", symbols)
	}
	client := factory.clientAt(t)
	if got := client.beforeReadyRequestCount(); got == 0 {
		t.Fatal("DocumentSymbol waited for cold-start diagnostics before requesting outline")
	}
	if got := client.openedLanguageID(); got != "javascript" {
		t.Fatalf("DidOpen language ID = %q, want javascript", got)
	}
}

type coldStartLanguageCase struct {
	languageID string
	write      func(t *testing.T, root string) string
}

func coldStartLanguageCases(t *testing.T) []coldStartLanguageCase {
	t.Helper()
	cases := []coldStartLanguageCase{
		{languageID: "css", write: writeColdStartCSSFixture},
		{languageID: "c", write: writeColdStartCFixture},
		{languageID: "cpp", write: writeColdStartCPPFixture},
		{languageID: "csharp", write: writeColdStartCSharpFixture},
		{languageID: "dart", write: writeColdStartDartFixture},
		{languageID: "dockerfile", write: writeColdStartDockerFixture},
		{languageID: "go", write: writeColdStartGoFixture},
		{languageID: "gomod", write: writeColdStartGoModFixture},
		{languageID: "gosum", write: writeColdStartGoSumFixture},
		{languageID: "gowork", write: writeColdStartGoWorkFixture},
		{languageID: "graphql", write: writeColdStartGraphQLFixture},
		{languageID: "html", write: writeColdStartHTMLFixture},
		{languageID: "java", write: writeColdStartJavaFixture},
		{languageID: "javascript", write: writeColdStartJavaScriptFixture},
		{languageID: "javascriptreact", write: writeColdStartJavaScriptReactFixture},
		{languageID: "json", write: writeColdStartJSONFixture},
		{languageID: "kotlin", write: writeColdStartKotlinFixture},
		{languageID: "lua", write: writeColdStartLuaFixture},
		{languageID: "markdown", write: writeColdStartMarkdownFixture},
		{languageID: "objective-c", write: writeColdStartObjectiveCFixture},
		{languageID: "objective-cpp", write: writeColdStartObjectiveCPPFixture},
		{languageID: "php", write: writeColdStartPHPFixture},
		{languageID: "prisma", write: writeColdStartPrismaFixture},
		{languageID: "python", write: writeColdStartPythonFixture},
		{languageID: "ruby", write: writeColdStartRubyFixture},
		{languageID: "rust", write: writeColdStartRustFixture},
		{languageID: "shellscript", write: writeColdStartShellFixture},
		{languageID: "sql", write: writeColdStartSQLFixture},
		{languageID: "svelte", write: writeColdStartSvelteFixture},
		{languageID: "swift", write: writeColdStartSwiftFixture},
		{languageID: "terraform", write: writeColdStartTerraformFixture},
		{languageID: "typescript", write: writeColdStartTypeScriptFixture},
		{languageID: "typescriptreact", write: writeColdStartTypeScriptReactFixture},
		{languageID: "vue", write: writeColdStartVueFixture},
		{languageID: "yaml", write: writeColdStartYAMLFixture},
	}
	assertColdStartCasesCoverDefaultLSPClientLanguages(t, cases)
	return cases
}

func assertColdStartCasesCoverDefaultLSPClientLanguages(t *testing.T, cases []coldStartLanguageCase) {
	t.Helper()
	got := make([]string, 0, len(cases))
	for _, tc := range cases {
		got = append(got, tc.languageID)
	}
	slices.Sort(got)
	want := defaultLSPClientLanguageIDs(t)
	if !slices.Equal(got, want) {
		t.Fatalf("cold-start language coverage = %#v, want default LSP client languages %#v", got, want)
	}
}

func defaultLSPClientLanguageIDs(t *testing.T) []string {
	t.Helper()
	registry := NewDefaultLanguageAdapterRegistry()
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

func writeColdStartCSSFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-css"}`)
	return writeColdStartFile(t, root, "style.css", "body { color: black; }\n")
}

func writeColdStartHTMLFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-html"}`)
	return writeColdStartFile(t, root, "index.html", "<main>Hello</main>\n")
}

func writeColdStartJSONFixture(t *testing.T, root string) string {
	t.Helper()
	return writeColdStartFile(t, root, "package.json", `{"name":"cold-json"}`+"\n")
}

func writeColdStartYAMLFixture(t *testing.T, root string) string {
	t.Helper()
	return writeColdStartFile(t, root, "config.yaml", "name: cold-yaml\n")
}

func writeColdStartMarkdownFixture(t *testing.T, root string) string {
	t.Helper()
	return writeColdStartFile(t, root, "README.md", "# Cold Markdown\n")
}

func writeColdStartVueFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-vue"}`)
	return writeColdStartFile(t, root, "App.vue", "<template><main>Hello</main></template>\n")
}

func writeColdStartSvelteFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-svelte"}`)
	return writeColdStartFile(t, root, "App.svelte", "<main>Hello</main>\n")
}

func writeColdStartCFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeColdStartFile(t, root, "main.c", "int main(void) { return 0; }\n")
}

func writeColdStartCPPFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeColdStartFile(t, root, "main.cpp", "int main() { return 0; }\n")
}

func writeColdStartObjectiveCFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeColdStartFile(t, root, "main.m", "int main(void) { return 0; }\n")
}

func writeColdStartObjectiveCPPFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "compile_flags.txt", "-Wall\n")
	return writeColdStartFile(t, root, "main.mm", "int main() { return 0; }\n")
}

func writeColdStartSwiftFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "Package.swift", "// swift-tools-version: 6.0\n")
	return writeColdStartFile(t, root, "Sources/App/main.swift", "print(\"hello\")\n")
}

func writeColdStartCSharpFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "global.json", `{"sdk":{"rollForward":"latestFeature"}}`)
	writeColdStartFile(t, root, "App.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework></PropertyGroup></Project>`)
	return writeColdStartFile(t, root, "Program.cs", "class Program { static void Main() {} }\n")
}

func writeColdStartPHPFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "composer.json", `{"name":"example/cold-php"}`)
	return writeColdStartFile(t, root, "index.php", "<?php echo 'hello';\n")
}

func writeColdStartRubyFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "Gemfile", "source 'https://rubygems.org'\n")
	return writeColdStartFile(t, root, "app.rb", "puts 'hello'\n")
}

func writeColdStartKotlinFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "settings.gradle.kts", "pluginManagement {}\n")
	return writeColdStartFile(t, root, "src/Main.kt", "fun main() {}\n")
}

func writeColdStartDartFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "pubspec.yaml", "name: cold_dart\n")
	return writeColdStartFile(t, root, "lib/main.dart", "void main() {}\n")
}

func writeColdStartLuaFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, ".luarc.json", "{}\n")
	return writeColdStartFile(t, root, "init.lua", "local value = 1\n")
}

func writeColdStartDockerFixture(t *testing.T, root string) string {
	t.Helper()
	return writeColdStartFile(t, root, "Dockerfile", "FROM scratch\n")
}

func writeColdStartTerraformFixture(t *testing.T, root string) string {
	t.Helper()
	return writeColdStartFile(t, root, "main.tf", "terraform {}\n")
}

func writeColdStartGraphQLFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-graphql"}`)
	return writeColdStartFile(t, root, "schema.graphql", "type Query { hello: String }\n")
}

func writeColdStartPrismaFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-prisma"}`)
	return writeColdStartFile(t, root, "schema.prisma", "datasource db { provider = \"sqlite\" url = \"file:dev.db\" }\n")
}

func writeColdStartGoFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "go.mod", "module example.test/coldgo\n\ngo 1.25.0\n")
	return writeColdStartFile(t, root, "main.go", "package main\n")
}

func writeColdStartGoModFixture(t *testing.T, root string) string {
	t.Helper()
	return writeColdStartFile(t, root, "go.mod", "module example.test/coldgomod\n\ngo 1.25.0\n")
}

func writeColdStartGoSumFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "go.mod", "module example.test/coldgosum\n\ngo 1.25.0\n")
	return writeColdStartFile(t, root, "go.sum", "")
}

func writeColdStartGoWorkFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "module/go.mod", "module example.test/coldgowork\n\ngo 1.25.0\n")
	return writeColdStartFile(t, root, "go.work", "go 1.25.0\n\nuse ./module\n")
}

func writeColdStartJavaFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "pom.xml", "<project></project>\n")
	return writeColdStartFile(t, root, "src/Main.java", "class Main {}\n")
}

func writeColdStartJavaScriptFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-js"}`)
	return writeColdStartFile(t, root, "app.js", "export const value = 1\n")
}

func writeColdStartJavaScriptReactFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "package.json", `{"name":"cold-jsx"}`)
	return writeColdStartFile(t, root, "app.jsx", "export const View = () => null\n")
}

func writeColdStartPythonFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "pyproject.toml", "[project]\nname = \"cold-python\"\n")
	return writeColdStartFile(t, root, "app.py", "value = 1\n")
}

func writeColdStartRustFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "Cargo.toml", "[package]\nname = \"cold_rust\"\nversion = \"0.1.0\"\n")
	return writeColdStartFile(t, root, "src/main.rs", "fn main() {}\n")
}

func writeColdStartShellFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "Makefile", "all:\n\t@true\n")
	return writeColdStartFile(t, root, "scripts/run.sh", "#!/usr/bin/env bash\n")
}

func writeColdStartSQLFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "sqlc.yaml", "version: '2'\n")
	return writeColdStartFile(t, root, "schema.sql", "select 1;\n")
}

func writeColdStartTypeScriptFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "tsconfig.json", `{"compilerOptions":{}}`)
	return writeColdStartFile(t, root, "app.ts", "export const value: number = 1\n")
}

func writeColdStartTypeScriptReactFixture(t *testing.T, root string) string {
	t.Helper()
	writeColdStartFile(t, root, "tsconfig.json", `{"compilerOptions":{"jsx":"react-jsx"}}`)
	return writeColdStartFile(t, root, "app.tsx", "export const View = () => null\n")
}

func writeColdStartFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	writeGenericTestFile(t, path, body)
	return path
}

type coldStartDefinitionFactory struct {
	readyDelay time.Duration
	client     *coldStartDefinitionClient
}

func (f *coldStartDefinitionFactory) NewClient(_ string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv("", nil, handler)
}

func (f *coldStartDefinitionFactory) NewClientWithEnv(_ string, _ []string, handler protocol.NotificationHandler) (Client, error) {
	f.client = &coldStartDefinitionClient{
		handler:    handler,
		readyDelay: f.readyDelay,
	}
	return f.client, nil
}

func (f *coldStartDefinitionFactory) clientAt(t *testing.T) *coldStartDefinitionClient {
	t.Helper()
	if f.client == nil {
		t.Fatal("client was not created")
	}
	return f.client
}

type coldStartDefinitionClient struct {
	mu                  sync.Mutex
	goroutines          sync.WaitGroup
	handler             protocol.NotificationHandler
	readyDelay          time.Duration
	openedURI           string
	openedLanguage      string
	ready               bool
	beforeReadyRequests int
}

func (c *coldStartDefinitionClient) Initialize(context.Context, string) error  { return nil }
func (c *coldStartDefinitionClient) Shutdown(context.Context) error            { return nil }
func (c *coldStartDefinitionClient) Notify(context.Context, string, any) error { return nil }
func (c *coldStartDefinitionClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}
func (c *coldStartDefinitionClient) DidClose(context.Context, string) error { return nil }
func (c *coldStartDefinitionClient) Close() error {
	c.goroutines.Wait()
	return nil
}

func (c *coldStartDefinitionClient) DidOpen(ctx context.Context, uri, languageID string, _ int, _ string) error {
	c.mu.Lock()
	c.openedURI = uri
	c.openedLanguage = languageID
	c.mu.Unlock()
	c.goroutines.Go(func() { c.publishReadyDiagnostics(ctx, uri) })
	return nil
}

func (c *coldStartDefinitionClient) publishReadyDiagnostics(ctx context.Context, uri string) {
	delay := c.readyDelay
	if delay <= 0 {
		delay = 30 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	if c.handler != nil {
		_ = c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri})
	}
}

func (c *coldStartDefinitionClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	c.mu.Lock()
	ready := c.ready
	uri := c.openedURI
	if !ready {
		c.beforeReadyRequests++
	}
	c.mu.Unlock()
	switch method {
	case protocol.MethodDefinition:
		if !ready {
			return json.RawMessage("null"), nil
		}
		return json.Marshal([]protocol.Location{{
			URI: uri,
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 13},
				End:   protocol.Position{Line: 0, Character: 18},
			},
		}})
	case protocol.MethodDocumentSymbol:
		return json.Marshal([]protocol.DocumentSymbol{{
			Name: "value",
			Kind: protocol.SymbolKindVariable,
			Range: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 13},
				End:   protocol.Position{Line: 0, Character: 18},
			},
			SelectionRange: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 13},
				End:   protocol.Position{Line: 0, Character: 18},
			},
		}})
	default:
		return nil, fmt.Errorf("unexpected request method %q", method)
	}
}

func (c *coldStartDefinitionClient) beforeReadyRequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.beforeReadyRequests
}

func (c *coldStartDefinitionClient) openedLanguageID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openedLanguage
}
