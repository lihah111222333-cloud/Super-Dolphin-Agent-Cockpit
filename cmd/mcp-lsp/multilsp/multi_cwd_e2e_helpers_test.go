package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

// --- e2e mock client (prefixed to avoid collision with document_bootstrap_test) ---

type e2eClient struct {
	mu        sync.Mutex
	rootDir   string
	handler   protocol.NotificationHandler
	documents map[string]string
	opens     []genericOpenEvent
	requests  []e2eRecordedRequest
	closed    bool
}

type e2eRecordedRequest struct {
	method string
	params any
}

func (c *e2eClient) setWorkspaceFolders([]protocol.WorkspaceFolder) {}
func (c *e2eClient) Initialize(_ context.Context, _ string) error   { return nil }
func (c *e2eClient) Shutdown(_ context.Context) error               { return nil }
func (c *e2eClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *e2eClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, e2eRecordedRequest{method: method, params: params})
	return json.RawMessage("null"), nil
}

func (c *e2eClient) Notify(_ context.Context, _ string, _ any) error { return nil }

func (c *e2eClient) DidOpen(_ context.Context, uri, languageID string, _ int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opens = append(c.opens, genericOpenEvent{uri: uri, language: languageID})
	c.documents[uri] = text
	return nil
}

func (c *e2eClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}

func (c *e2eClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.documents, uri)
	return nil
}

// --- e2e factory ---

type e2eFactory struct {
	mu      sync.Mutex
	clients []*e2eClient
	calls   []genericMatrixFactoryCall
}

func (f *e2eFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *e2eFactory) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &e2eClient{rootDir: rootDir, handler: handler, documents: map[string]string{}}
	f.calls = append(f.calls, genericMatrixFactoryCall{rootDir: rootDir, env: append([]string(nil), env...)})
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *e2eFactory) clientCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

// --- helpers ---

func ctxWithCWD(cwd, agentID, threadID string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  agentID,
		ThreadID: threadID,
		Family:   "lsp",
		CWD:      cwd,
	})
}

func setupWorktreeProject(t *testing.T, root string, modules []string) {
	t.Helper()
	goWorkBody := "go 1.25.0\n\n"
	for _, mod := range modules {
		goWorkBody += fmt.Sprintf("use ./%s\n", mod)
		modDir := filepath.Join(root, mod)
		writeGenericTestFile(t, filepath.Join(modDir, "go.mod"),
			fmt.Sprintf("module example.test/%s\n\ngo 1.25.0\n", mod))
		writeGenericTestFile(t, filepath.Join(modDir, "main.go"),
			fmt.Sprintf("package main\n\nfunc %sMain() {}\n", mod))
	}
	writeGenericTestFile(t, filepath.Join(root, "go.work"), goWorkBody)
}

func setupStandaloneGoProject(t *testing.T, root, modName string) {
	t.Helper()
	writeGenericTestFile(t, filepath.Join(root, "go.mod"),
		fmt.Sprintf("module example.test/%s\n\ngo 1.25.0\n", modName))
	writeGenericTestFile(t, filepath.Join(root, "main.go"),
		fmt.Sprintf("package main\n\nfunc %sMain() {}\n", modName))
}
