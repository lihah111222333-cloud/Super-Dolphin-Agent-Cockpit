package multilsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// TestRenamePrewarmsAllTypeScriptProjectConsumersAndKeepsDiagnosticsStable
// reproduces the real TypeScript fixture: the test file is excluded by
// tsconfig, but it is still a same-project consumer that must receive a
// workspace edit after rename.
func TestRenamePrewarmsAllTypeScriptProjectConsumersAndKeepsDiagnosticsStable(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "mathematic.ts")
	index := filepath.Join(root, "src", "index.ts")
	testFile := filepath.Join(root, "src", "mathematic.test.ts")

	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"rename-project"}`)
	writeGenericTestFile(t, filepath.Join(root, "tsconfig.json"), `{
  "compilerOptions": {"target": "es6", "module": "commonjs", "strict": true},
  "include": ["src/**/*"],
  "exclude": ["src/**/*.test.ts"]
}`)
	writeGenericTestFile(t, target, "export class Mathematic {\n  static add(a: number, b: number): number { return a + b }\n}\n")
	writeGenericTestFile(t, index, "import { Mathematic } from './mathematic'\nexport const sum = Mathematic.add(1, 2)\n")
	writeGenericTestFile(t, testFile, "import { Mathematic } from './mathematic'\ntest('sum', () => expect(Mathematic.add(1, 2)).toBe(3))\n")

	client := &renameProjectClient{}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		DisableInitialWorkspaceBootstrap: true,
		DiagnosticsInitialDelay:          time.Millisecond,
		DiagnosticsPollInterval:          time.Millisecond,
		DiagnosticsMaxWait:               200 * time.Millisecond,
		ClientFactory: ClientFactoryFunc(func(_ string, handler protocol.NotificationHandler) (Client, error) {
			client.handler = handler
			return client, nil
		}),
	}).(*manager)
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	})

	ctx := ctxWithCWD(root, "agent-rename-project", "thread-rename-project")
	edit, err := mgr.Rename(ctx, fileURIFromPath(target), protocol.Position{Line: 0, Character: 13}, "MathematicLspProbe")
	if err != nil {
		t.Fatalf("Rename() error = %v", err)
	}

	wantURIs := []string{fileURIFromPath(target), fileURIFromPath(index), fileURIFromPath(testFile)}
	sort.Strings(wantURIs)
	gotURIs := make([]string, 0, len(edit.Changes))
	for uri := range edit.Changes {
		gotURIs = append(gotURIs, uri)
	}
	sort.Strings(gotURIs)
	if len(gotURIs) != len(wantURIs) {
		t.Fatalf("Rename() changed URIs = %v, want all project consumers %v; opened=%v", gotURIs, wantURIs, client.openedHistorySnapshot())
	}
	for index, uri := range wantURIs {
		if gotURIs[index] != uri {
			t.Fatalf("Rename() changed URIs = %v, want all project consumers %v; opened=%v", gotURIs, wantURIs, client.openedHistorySnapshot())
		}
	}

	if got := client.documentSymbolURIs(); len(got) != 2 {
		t.Fatalf("documentSymbol barrier URIs = %v, want index.ts and excluded mathematic.test.ts", got)
	}
	if got := client.openedHistorySnapshot(); len(got) < 3 {
		t.Fatalf("opened project documents = %v, want target plus both consumers", got)
	}

	if err := mgr.WaitDiagnosticsStable(ctx, wantURIs); err != nil {
		t.Fatalf("WaitDiagnosticsStable() error = %v", err)
	}
	diagnostics, err := mgr.Diagnostics(ctx, wantURIs)
	if err != nil {
		t.Fatalf("Diagnostics() error = %v", err)
	}
	if len(diagnostics) != len(wantURIs) {
		t.Fatalf("Diagnostics() returned %d snapshots, want %d empty snapshots: %#v", len(diagnostics), len(wantURIs), diagnostics)
	}
	for _, snapshot := range diagnostics {
		if len(snapshot.Diagnostics) != 0 {
			t.Fatalf("Diagnostics() snapshot for %s = %#v, want stable empty diagnostics", snapshot.URI, snapshot.Diagnostics)
		}
	}
}

func TestWorkspaceSymbolLanguageScopeDoesNotSelectUnrelatedNestedProject(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "a-unrelated", "package.json"), `{"name":"unrelated"}`)
	writeGenericTestFile(t, filepath.Join(root, "z-typescript", "tsconfig.json"), `{"compilerOptions":{"strict":true}}`)

	client := &workspaceRootProbeClient{}
	var factoryRoot string
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		DisableInitialWorkspaceBootstrap: true,
		ClientFactory: ClientFactoryFunc(func(rootDir string, _ protocol.NotificationHandler) (Client, error) {
			factoryRoot = rootDir
			return client, nil
		}),
	}).(*manager)
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	})

	results, err := mgr.WorkspaceSymbol(ctxWithCWD(root, "agent-workspace-symbol", "thread-workspace-symbol"), "Mathematic", "typescript")
	if err != nil {
		t.Fatalf("WorkspaceSymbol() error = %v", err)
	}
	if filepath.Clean(factoryRoot) != filepath.Clean(root) {
		t.Fatalf("workspace symbol client root = %q, want workspace root %q; unrelated nested marker was selected", factoryRoot, root)
	}
	if len(results) != 1 || results[0].WorkspaceSymbol == nil || results[0].WorkspaceSymbol.Name != "Mathematic" {
		t.Fatalf("WorkspaceSymbol() results = %#v, want Mathematic", results)
	}
}

type renameProjectClient struct {
	noopClient

	mu             sync.Mutex
	handler        protocol.NotificationHandler
	opened         map[string]struct{}
	openedHistory  []string
	documentSymbol []string
}

func (c *renameProjectClient) DidOpen(_ context.Context, uri, _ string, _ int, _ string) error {
	c.mu.Lock()
	if c.opened == nil {
		c.opened = make(map[string]struct{})
	}
	c.opened[uri] = struct{}{}
	c.openedHistory = append(c.openedHistory, uri)
	handler := c.handler
	c.mu.Unlock()
	if handler == nil {
		return nil
	}
	return handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{}})
}

func (c *renameProjectClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.opened, uri)
	return nil
}

func (c *renameProjectClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch method {
	case protocol.MethodDocumentSymbol:
		documentParams, ok := params.(protocol.DocumentSymbolParams)
		if ok {
			c.documentSymbol = append(c.documentSymbol, documentParams.TextDocument.URI)
		}
		return json.RawMessage("[]"), nil
	case protocol.MethodRename:
		if _, ok := params.(protocol.RenameParams); !ok {
			return nil, &testRenameProtocolError{method: method}
		}
		changes := make(map[string][]protocol.TextEdit, len(c.opened))
		uris := make([]string, 0, len(c.opened))
		for uri := range c.opened {
			uris = append(uris, uri)
		}
		sort.Strings(uris)
		for _, uri := range uris {
			changes[uri] = []protocol.TextEdit{{
				Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 13}, End: protocol.Position{Line: 0, Character: 23}},
				NewText: "MathematicLspProbe",
			}}
		}
		return json.Marshal(protocol.WorkspaceEdit{Changes: changes})
	default:
		return json.RawMessage("null"), nil
	}
}

func (c *renameProjectClient) openedHistorySnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.openedHistory...)
}

func (c *renameProjectClient) documentSymbolURIs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.documentSymbol...)
}

type testRenameProtocolError struct {
	method string
}

func (e *testRenameProtocolError) Error() string { return "unexpected rename params for " + e.method }

type workspaceRootProbeClient struct{ noopClient }

func (c *workspaceRootProbeClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != protocol.MethodWorkspaceSymbol {
		return json.RawMessage("null"), nil
	}
	return json.Marshal([]protocol.WorkspaceSymbol{{
		Name:     "Mathematic",
		Kind:     int(protocol.SymbolKindClass),
		Location: protocol.WorkspaceSymbolLocation{URI: "file:///workspace/src/mathematic.ts"},
	}})
}
