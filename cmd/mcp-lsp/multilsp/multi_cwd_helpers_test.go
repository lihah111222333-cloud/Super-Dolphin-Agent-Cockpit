package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// --- e2e mock client (prefixed to avoid collision with document_bootstrap_test) ---

type e2eClient struct {
	mu        sync.Mutex
	rootDir   string
	handler   protocol.NotificationHandler
	documents map[string]string
	opens     []genericOpenEvent
	changes   []string
	closes    []string
	requests  []e2eRecordedRequest
	closed    bool
}

type e2eRecordedRequest struct {
	method string
	params any
}

func (c *e2eClient) setWorkspaceFolders(folders []protocol.WorkspaceFolder) {
	_ = c
	_ = folders
}
func (c *e2eClient) Initialize(_ context.Context, _ string) error { return nil }
func (c *e2eClient) Shutdown(_ context.Context) error             { return nil }
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
	return e2eResponseForRequest(method, params), nil
}

func (c *e2eClient) Notify(_ context.Context, _ string, _ any) error { return nil }

func (c *e2eClient) DidOpen(_ context.Context, uri, languageID string, _ int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opens = append(c.opens, genericOpenEvent{uri: uri, language: languageID})
	c.documents[uri] = text
	if c.handler != nil {
		return c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri})
	}
	return nil
}

func (c *e2eClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	c.changes = append(c.changes, uri)
	if c.handler != nil {
		return c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri})
	}
	return nil
}

func (c *e2eClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes = append(c.closes, uri)
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
	return f.NewClientWithOptions(rootDir, env, nil, handler)
}

func (f *e2eFactory) NewClientWithOptions(rootDir string, env []string, initOptions map[string]any, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &e2eClient{rootDir: rootDir, handler: handler, documents: map[string]string{}}
	f.calls = append(f.calls, genericMatrixFactoryCall{rootDir: rootDir, env: append([]string(nil), env...), initOptions: cloneAnyMap(initOptions)})
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *e2eFactory) clientCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

func (f *e2eFactory) snapshot() []e2eFactorySnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshots := make([]e2eFactorySnapshot, 0, len(f.clients))
	for index, client := range f.clients {
		client.mu.Lock()
		snapshot := e2eFactorySnapshot{
			rootDir:  client.rootDir,
			requests: append([]e2eRecordedRequest(nil), client.requests...),
			opens:    append([]genericOpenEvent(nil), client.opens...),
			changes:  append([]string(nil), client.changes...),
			closes:   append([]string(nil), client.closes...),
			closed:   client.closed,
		}
		client.mu.Unlock()
		if index < len(f.calls) {
			snapshot.env = append([]string(nil), f.calls[index].env...)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

type e2eFactorySnapshot struct {
	rootDir  string
	env      []string
	requests []e2eRecordedRequest
	opens    []genericOpenEvent
	changes  []string
	closes   []string
	closed   bool
}

func e2eResponseForRequest(method string, params any) json.RawMessage {
	uri := e2eRequestURI(params)
	rng := protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 3, Character: 1}}
	switch method {
	case protocol.MethodPrepareCallHierarchy:
		return e2eJSON([]protocol.CallHierarchyItem{{
			Name: "caller", Kind: int(protocol.SymbolKindFunction), URI: uri, Range: rng, SelectionRange: rng,
		}})
	case protocol.MethodCallHierarchyIncoming:
		return e2eJSON([]protocol.CallHierarchyIncomingCall{{
			From: protocol.CallHierarchyItem{
				Name: "incoming", Kind: int(protocol.SymbolKindFunction), URI: uri, Range: rng, SelectionRange: rng,
			},
			FromRanges: []protocol.Range{rng},
		}})
	case protocol.MethodCallHierarchyOutgoing:
		return e2eJSON([]protocol.CallHierarchyOutgoingCall{{
			To: protocol.CallHierarchyItem{
				Name: "outgoing", Kind: int(protocol.SymbolKindFunction), URI: uri, Range: rng, SelectionRange: rng,
			},
			FromRanges: []protocol.Range{rng},
		}})
	case protocol.MethodPrepareTypeHierarchy:
		return e2eJSON([]protocol.TypeHierarchyItem{{
			Name: "subject", Kind: int(protocol.SymbolKindStruct), URI: uri, Range: rng, SelectionRange: rng,
		}})
	case protocol.MethodTypeHierarchySupertypes, protocol.MethodTypeHierarchySubtypes:
		return e2eJSON([]protocol.TypeHierarchyItem{{
			Name: "related", Kind: int(protocol.SymbolKindStruct), URI: uri, Range: rng, SelectionRange: rng,
		}})
	case protocol.MethodWorkspaceSymbol:
		return e2eJSON([]protocol.WorkspaceSymbol{{
			Name: "Main", Kind: int(protocol.SymbolKindFunction),
		}})
	default:
		return json.RawMessage("null")
	}
}

func e2eRequestURI(params any) string {
	payload, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	var envelope struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Item struct {
			URI string `json:"uri"`
		} `json:"item"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	if envelope.TextDocument.URI != "" {
		return envelope.TextDocument.URI
	}
	return envelope.Item.URI
}

func e2eJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return payload
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
	useGoWorkFallbackParser(t)
	goWorkBody := ""
	for _, mod := range modules {
		goWorkBody += fmt.Sprintf("use ./%s\n", mod)
		modDir := filepath.Join(root, mod)
		writeGenericTestFile(t, filepath.Join(modDir, "go.mod"),
			fmt.Sprintf("module example.test/%s\n", mod))
		writeGenericTestFile(t, filepath.Join(modDir, "main.go"),
			fmt.Sprintf("package main\n\nfunc %sMain() {}\n", mod))
	}
	writeGenericTestFile(t, filepath.Join(root, "go.work"), goWorkBody)
}

func setupStandaloneGoProject(t *testing.T, root, modName string) {
	t.Helper()
	useGoWorkFallbackParser(t)
	writeGenericTestFile(t, filepath.Join(root, "go.mod"),
		fmt.Sprintf("module example.test/%s\n", modName))
	writeGenericTestFile(t, filepath.Join(root, "main.go"),
		fmt.Sprintf("package main\n\nfunc %sMain() {}\n", modName))
}

func useGoWorkFallbackParser(t *testing.T) {
	t.Helper()
	goDir := filepath.Dir(runtimeGoExecutableForTest(t))
	// 保留真实 go 供 go.work JSON 解析与工具链探测使用；不可用场景由 parser 单元测试独立覆盖。
	t.Setenv("PATH", goDir)
}

func runtimeGoExecutableForTest(t *testing.T) string {
	t.Helper()
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate Go executable for multi-CWD fixture: %v", err)
	}
	return executable
}
