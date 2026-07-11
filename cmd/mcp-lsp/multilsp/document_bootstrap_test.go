package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestDocumentRequestBootstrapsFreshSnapshotForJavaScript(t *testing.T) {
	root := t.TempDir()
	ctx := ctxWithCWD(root, "agent-bootstrap", "thread-bootstrap")
	writeBootstrapTestFile(t, filepath.Join(root, "package.json"), `{"name":"multilsp-test"}`)

	target := filepath.Join(root, "app.js")
	writeBootstrapTestFile(t, target, "function staleName() { return 1; }\n")

	factory := &recordingClientFactory{}
	manager := NewManager(Config{
		WorkspaceRoot:      root,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})
	t.Cleanup(func() { closeBootstrapTestManager(t, manager) })

	if err := manager.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("bootstrap stale document: %v", err)
	}
	client := requireRecordingClient(t, factory)
	if got := client.openCount(); got != 1 {
		t.Fatalf("initial bootstrap should open the JS document once, got %d", got)
	}

	writeBootstrapTestFile(t, target, "function freshName() { return 2; }\n")
	client.expectRequestContent("freshName")

	symbols, err := manager.DocumentSymbol(ctx, target)
	if err != nil {
		t.Fatalf("document symbol after external edit: %v", err)
	}
	assertFreshDocumentSymbol(t, symbols)
	if got := client.changeCount(); got != 1 {
		t.Fatalf("document request should push exactly one DidChange after disk edit, got %d", got)
	}
	if !client.anyDocumentContains("freshName") {
		t.Fatalf("client snapshot was not refreshed, documents=%#v", client.documentSnapshot())
	}
}

func writeBootstrapTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func closeBootstrapTestManager(t *testing.T, manager Manager) {
	t.Helper()
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func requireRecordingClient(t *testing.T, factory *recordingClientFactory) *recordingClient {
	t.Helper()
	client := factory.currentClient()
	if client == nil {
		t.Fatal("expected bootstrap to create an LSP client")
	}
	return client
}

func assertFreshDocumentSymbol(t *testing.T, symbols []protocol.DocumentSymbol) {
	t.Helper()
	if len(symbols) != 1 || symbols[0].Name != "freshName" {
		t.Fatalf("expected fresh symbol result, got %#v", symbols)
	}
}

type recordingClientFactory struct {
	mu     sync.Mutex
	client *recordingClient
}

func (f *recordingClientFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.client = &recordingClient{documents: map[string]string{}}
	return f.client, nil
}

func (f *recordingClientFactory) currentClient() *recordingClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.client
}

type recordingClient struct {
	mu                sync.Mutex
	documents         map[string]string
	expectedSubstring string
	didOpenCount      int
	didChangeCount    int
}

func (c *recordingClient) Initialize(context.Context, string) error {
	return nil
}

func (c *recordingClient) Shutdown(context.Context) error {
	return nil
}

func (c *recordingClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method != protocol.MethodDocumentSymbol {
		return json.RawMessage("null"), nil
	}
	uri, err := documentURIFromParams(params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	text := c.documents[uri]
	expected := c.expectedSubstring
	c.mu.Unlock()
	if expected != "" && !strings.Contains(text, expected) {
		return nil, fmt.Errorf("document request used stale snapshot: expected %q in %q", expected, text)
	}
	name := expected
	if name == "" {
		name = "symbol"
	}
	return json.Marshal([]protocol.DocumentSymbol{{
		Name:           name,
		Kind:           protocol.SymbolKindFunction,
		Range:          protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 0, Character: len(text)}},
		SelectionRange: protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 0, Character: len(name)}},
	}})
}

func (c *recordingClient) Notify(context.Context, string, any) error {
	return nil
}

func (c *recordingClient) DidOpen(_ context.Context, uri, _ string, _ int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didOpenCount++
	c.documents[uri] = text
	return nil
}

func (c *recordingClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didChangeCount++
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}

func (c *recordingClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.documents, uri)
	return nil
}

func (c *recordingClient) Close() error {
	return nil
}

func (c *recordingClient) expectRequestContent(substring string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expectedSubstring = substring
}

func (c *recordingClient) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didOpenCount
}

func (c *recordingClient) changeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didChangeCount
}

func (c *recordingClient) anyDocumentContains(substring string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, text := range c.documents {
		if strings.Contains(text, substring) {
			return true
		}
	}
	return false
}

func (c *recordingClient) documentSnapshot() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.documents))
	for uri, text := range c.documents {
		out[uri] = text
	}
	return out
}

func documentURIFromParams(params any) (string, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	var decoded protocol.DocumentSymbolParams
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", err
	}
	return decoded.TextDocument.URI, nil
}
