package multilsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestTypeScriptDocumentSymbolRetriesEmptyLSPBeforeFallback(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "user-ui-v2", "src", "lib", "market", "Datafeed.ts")
	writeGenericTestFile(t, filepath.Join(root, "user-ui-v2", "package.json"), `{"name":"user-ui-v2"}`)
	writeGenericTestFile(t, target, strings.Join([]string{
		"export class WJDatafeed {",
		"    private _connect() { return true; }",
		"}",
		"",
	}, "\n"))
	recovered := mustMarshalDocumentSymbols(t, []protocol.DocumentSymbol{testDocumentSymbol("RecoveredFromLSP")})
	factory := &sequentialDocumentSymbolFactory{responses: []json.RawMessage{json.RawMessage("[]"), recovered}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	symbols, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
	}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol(TypeScript retry): %v", err)
	}
	requireSymbolNamesContain(t, collectDocumentSymbolNames(symbols), []string{"RecoveredFromLSP"})
	if got := factory.requestCount(); got != 2 {
		t.Fatalf("documentSymbol requests = %d, want first empty result plus retry", got)
	}
}

func TestTypeScriptDocumentSymbolUsesNavigationTreeFallbackWhenLSPStaysEmpty(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "user-ui-v2", "src", "widgets", "FancyWidget.tsx")
	writeGenericTestFile(t, filepath.Join(root, "user-ui-v2", "package.json"), `{"name":"user-ui-v2"}`)
	content := "export default memo(function FancyWidget() { return null; })\n"
	writeGenericTestFile(t, target, content)
	installFakeTypeScriptNavigationNode(t, fakeTypeScriptNavigationTree(t, content))
	factory := &sequentialDocumentSymbolFactory{responses: []json.RawMessage{json.RawMessage("[]"), json.RawMessage("[]")}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	symbols, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
	}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol(TypeScript navigation fallback): %v", err)
	}
	names := collectDocumentSymbolNames(symbols)
	requireSymbolNamesContain(t, names, []string{"FromNavigationTree", "render"})
	requireSymbolNamesNotContain(t, names, []string{"React"})
	if got := factory.requestCount(); got != 2 {
		t.Fatalf("documentSymbol requests = %d, want retry before navigation fallback", got)
	}
}

type sequentialDocumentSymbolFactory struct {
	responses []json.RawMessage
	client    *sequentialDocumentSymbolClient
}

func (f *sequentialDocumentSymbolFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	client := &sequentialDocumentSymbolClient{
		genericMatrixClient: &genericMatrixClient{handler: handler, documents: map[string]string{}},
		responses:           append([]json.RawMessage(nil), f.responses...),
	}
	f.client = client
	return client, nil
}

func (f *sequentialDocumentSymbolFactory) requestCount() int {
	if f.client == nil {
		return 0
	}
	return f.client.requestCount()
}

type sequentialDocumentSymbolClient struct {
	*genericMatrixClient
	mu        sync.Mutex
	responses []json.RawMessage
	requests  int
}

func (c *sequentialDocumentSymbolClient) Request(context.Context, string, any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	if len(c.responses) == 0 {
		return json.RawMessage("[]"), nil
	}
	index := c.requests - 1
	if index >= len(c.responses) {
		index = len(c.responses) - 1
	}
	return c.responses[index], nil
}

func (c *sequentialDocumentSymbolClient) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func fakeTypeScriptNavigationTree(t *testing.T, content string) string {
	t.Helper()
	nameStart := strings.Index(content, "FancyWidget")
	if nameStart < 0 {
		nameStart = 0
	}
	tree := map[string]any{
		"text":  "<global>",
		"kind":  "script",
		"spans": []map[string]int{{"start": 0, "length": len(content)}},
		"childItems": []map[string]any{
			{
				"text":     "React",
				"kind":     "alias",
				"spans":    []map[string]int{{"start": 0, "length": len("import React from 'react';")}},
				"nameSpan": map[string]int{"start": 7, "length": len("React")},
			},
			{
				"text":     "FromNavigationTree",
				"kind":     "function",
				"spans":    []map[string]int{{"start": 0, "length": len(content)}},
				"nameSpan": map[string]int{"start": nameStart, "length": len("FancyWidget")},
				"childItems": []map[string]any{{
					"text":     "render",
					"kind":     "method",
					"spans":    []map[string]int{{"start": nameStart, "length": len("FancyWidget")}},
					"nameSpan": map[string]int{"start": nameStart, "length": len("FancyWidget")},
				}},
			},
		},
	}
	raw, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal fake navigation tree: %v", err)
	}
	return string(raw)
}

func installFakeTypeScriptNavigationNode(t *testing.T, output string) {
	t.Helper()
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "navigation.json")
	if err := os.WriteFile(outputPath, []byte(output), 0o600); err != nil {
		t.Fatalf("write fake navigation output: %v", err)
	}
	nodePath := filepath.Join(dir, "node")
	script := strings.Join([]string{
		"#!/bin/sh",
		"cat >/dev/null",
		"cat " + shellQuote(outputPath),
		"",
	}, "\n")
	if err := os.WriteFile(nodePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func mustMarshalDocumentSymbols(t *testing.T, symbols []protocol.DocumentSymbol) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(symbols)
	if err != nil {
		t.Fatalf("marshal document symbols: %v", err)
	}
	return raw
}

func testDocumentSymbol(name string) protocol.DocumentSymbol {
	return protocol.DocumentSymbol{
		Name:           name,
		Kind:           protocol.SymbolKindFunction,
		Range:          newRange(0, 0, 0, len(name)),
		SelectionRange: newRange(0, 0, 0, len(name)),
	}
}

func collectDocumentSymbolNames(symbols []protocol.DocumentSymbol) []string {
	names := make([]string, 0, len(symbols))
	var walk func([]protocol.DocumentSymbol)
	walk = func(items []protocol.DocumentSymbol) {
		for _, symbol := range items {
			names = append(names, symbol.Name)
			walk(symbol.Children)
		}
	}
	walk(symbols)
	return names
}

func requireSymbolNamesContain(t *testing.T, got []string, want []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(got))
	for _, name := range got {
		seen[name] = struct{}{}
	}
	for _, name := range want {
		if _, ok := seen[name]; !ok {
			t.Fatalf("symbols = %v, missing %q", got, name)
		}
	}
}

func requireSymbolNamesNotContain(t *testing.T, got []string, want []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(got))
	for _, name := range got {
		seen[name] = struct{}{}
	}
	for _, name := range want {
		if _, ok := seen[name]; ok {
			t.Fatalf("symbols = %v, unexpected %q", got, name)
		}
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
