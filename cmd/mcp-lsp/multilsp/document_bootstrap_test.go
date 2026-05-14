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

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

func TestDocumentRequestBootstrapsFreshSnapshotForJavaScript(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"multilsp-test"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	target := filepath.Join(root, "app.js")
	if err := os.WriteFile(target, []byte("function staleName() { return 1; }\n"), 0o644); err != nil {
		t.Fatalf("write stale app.js: %v", err)
	}

	factory := &recordingClientFactory{}
	manager := NewManager(Config{
		WorkspaceRoot:      root,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})
	defer func() {
		if err := manager.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	if err := manager.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("bootstrap stale document: %v", err)
	}
	client := factory.currentClient()
	if client == nil {
		t.Fatal("expected bootstrap to create an LSP client")
	}
	if got := client.openCount(); got != 1 {
		t.Fatalf("initial bootstrap should open the JS document once, got %d", got)
	}

	if err := os.WriteFile(target, []byte("function freshName() { return 2; }\n"), 0o644); err != nil {
		t.Fatalf("write fresh app.js: %v", err)
	}
	client.expectRequestContent("freshName")

	symbols, err := manager.DocumentSymbol(ctx, target)
	if err != nil {
		t.Fatalf("document symbol after external edit: %v", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "freshName" {
		t.Fatalf("expected fresh symbol result, got %#v", symbols)
	}
	if got := client.changeCount(); got != 1 {
		t.Fatalf("document request should push exactly one DidChange after disk edit, got %d", got)
	}
	if !client.anyDocumentContains("freshName") {
		t.Fatalf("client snapshot was not refreshed, documents=%#v", client.documentSnapshot())
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
