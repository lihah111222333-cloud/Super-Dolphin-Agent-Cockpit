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

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
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
		{languageID: "go", write: writeColdStartGoFixture},
		{languageID: "gomod", write: writeColdStartGoModFixture},
		{languageID: "gosum", write: writeColdStartGoSumFixture},
		{languageID: "gowork", write: writeColdStartGoWorkFixture},
		{languageID: "java", write: writeColdStartJavaFixture},
		{languageID: "javascript", write: writeColdStartJavaScriptFixture},
		{languageID: "javascriptreact", write: writeColdStartJavaScriptReactFixture},
		{languageID: "python", write: writeColdStartPythonFixture},
		{languageID: "rust", write: writeColdStartRustFixture},
		{languageID: "shellscript", write: writeColdStartShellFixture},
		{languageID: "typescript", write: writeColdStartTypeScriptFixture},
		{languageID: "typescriptreact", write: writeColdStartTypeScriptReactFixture},
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
