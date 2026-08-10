package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestTypeHierarchyUnsupportedPrepareReturnsCapabilityErrorForJavaScript(t *testing.T) {
	root, target := writeJavaScriptHierarchyFixture(t)
	factory := &unsupportedHierarchyFactory{unsupportedMethod: protocol.MethodPrepareTypeHierarchy}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	_, err := mgr.TypeHierarchy(
		common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}, Family: "lsp"}),
		target,
		protocol.Position{Line: 0, Character: 16},
		"supertypes",
	)
	if !errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("TypeHierarchy error = %v, want ErrUnsupportedCapability", err)
	}
	client := factory.clientAt(t)
	if !client.opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("opened documents = %#v, want target opened as javascript", client.openEvents())
	}
}

func TestCallHierarchyUnsupportedPrepareReturnsCapabilityErrorForJavaScript(t *testing.T) {
	root, target := writeJavaScriptHierarchyFixture(t)
	factory := &unsupportedHierarchyFactory{unsupportedMethod: protocol.MethodPrepareCallHierarchy}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	_, err := mgr.CallHierarchy(
		common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}, Family: "lsp"}),
		target,
		protocol.Position{Line: 0, Character: 16},
		"both",
	)
	if !errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("CallHierarchy error = %v, want ErrUnsupportedCapability", err)
	}
	client := factory.clientAt(t)
	if !client.opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("opened documents = %#v, want target opened as javascript", client.openEvents())
	}
}

func TestCallHierarchyUnsupportedDirectionReturnsCapabilityErrorForJavaScript(t *testing.T) {
	root, target := writeJavaScriptHierarchyFixture(t)
	factory := &unsupportedHierarchyDirectionFactory{unsupportedMethod: protocol.MethodCallHierarchyIncoming}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	_, err := mgr.CallHierarchy(
		common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}, Family: "lsp"}),
		target,
		protocol.Position{Line: 0, Character: 16},
		"incoming",
	)
	if !errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("CallHierarchy direction error = %v, want ErrUnsupportedCapability", err)
	}
}

func TestCallHierarchyRetriesEmptyPrepareForTypeScriptAfterBootstrap(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{}}`)
	target := filepath.Join(root, "app.ts")
	writeGenericTestFile(t, target, "export function greet(name: string) { return name }\n")
	factory := &delayedHierarchyPrepareFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	results, err := mgr.CallHierarchy(
		common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}, Family: "lsp"}),
		target,
		protocol.Position{Line: 0, Character: 16},
		"outgoing",
	)
	if err != nil {
		t.Fatalf("CallHierarchy: %v", err)
	}
	if len(results) != 1 || results[0].Item.Name != "greet" || len(results[0].Outgoing) != 1 {
		t.Fatalf("CallHierarchy results = %#v, want retried greet hierarchy with outgoing call", results)
	}
	client := factory.clientAt(t)
	if got := client.prepareCalls; got != 2 {
		t.Fatalf("prepareCallHierarchy calls = %d, want first empty result plus retry", got)
	}
	if !client.opened(fileURIFromPath(target), "typescript") {
		t.Fatalf("opened documents = %#v, want target opened before hierarchy retry", client.openEvents())
	}
}

func TestCodeActionSendsEmptyDiagnosticsArray(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &codeActionParamFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	_, err := mgr.CodeAction(
		common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}, Family: "lsp"}),
		target,
		protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}},
		nil,
	)
	if err != nil {
		t.Fatalf("CodeAction: %v", err)
	}
	client := factory.clientAt(t)
	assertCodeActionOpenedJavaScript(t, client, target)
	assertCodeActionParams(t, client.codeActionParams, target)
}

func TestWorkspaceSymbolUsesResolvedFileScopeForJavaScript(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	nestedRoot := filepath.Join(root, "frontend")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"root-web"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "export function rootSymbol() {}\n")
	writeGenericTestFile(t, filepath.Join(nestedRoot, "package.json"), `{"name":"nested-web"}`)
	target := filepath.Join(nestedRoot, "app.js")
	writeGenericTestFile(t, target, "export function nestedSymbol() {}\n")
	factory := &workspaceSymbolScopeFactory{symbolName: "nestedSymbol", symbolURI: fileURIFromPath(target)}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}, Family: "lsp"})
	ctx = WithResolvedLSPToolScope(ctx, resolvedWorkspaceSymbolScope(t, root, nestedRoot, target))
	results, err := mgr.WorkspaceSymbol(ctx, "nestedSymbol", "javascript")
	if err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	if got := factory.rootDirAt(t, 0); got != nestedRoot {
		t.Fatalf("workspace symbol client root = %q, want resolved nested root %q", got, nestedRoot)
	}
	client := factory.clientAt(t)
	if !client.opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("opened documents = %#v, want exact resolved file target", client.openEvents())
	}
	if client.requestScope.WorkspaceRoot != nestedRoot {
		t.Fatalf("request scope workspace root = %q, want %q", client.requestScope.WorkspaceRoot, nestedRoot)
	}
	if client.requestParams.Query != "nestedSymbol" {
		t.Fatalf("workspace symbol query = %q, want nestedSymbol", client.requestParams.Query)
	}
	if len(results) != 1 || results[0].WorkspaceSymbol == nil || results[0].WorkspaceSymbol.Name != "nestedSymbol" {
		t.Fatalf("workspace symbol results = %#v, want nestedSymbol", results)
	}
}

func TestWorkspaceSymbolResolvedLanguageScopeBootstrapsJavaScript(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "export function workspaceSymbol() {}\n")
	factory := &workspaceSymbolScopeFactory{symbolName: "workspaceSymbol", symbolURI: fileURIFromPath(target)}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}, Family: "lsp"})
	ctx = WithResolvedLSPToolScope(ctx, resolvedWorkspaceSymbolLanguageScope(t, root))
	if _, err := mgr.WorkspaceSymbol(ctx, "workspaceSymbol", "javascript"); err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	client := factory.clientAt(t)
	if !client.opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("opened documents = %#v, want language-scope bootstrap document", client.openEvents())
	}
}

func resolvedWorkspaceSymbolScope(t *testing.T, root, nestedRoot, target string) ResolvedLSPToolScope {
	t.Helper()
	resolved, err := ResolveLSPToolScope(LSPToolScope{
		AgentID:               "agent-js",
		ThreadID:              "thread-js",
		CWD:                   root,
		WorkspaceRoots:        []string{root},
		Family:                "lsp",
		LanguageID:            "javascript",
		TargetPath:            target,
		WorkspaceRoot:         nestedRoot,
		LanguageWorkspaceRoot: nestedRoot,
		ProjectRoot:           nestedRoot,
		RootKind:              "jsts_project",
		LanguageSpecific: map[string]string{
			"adapterRoot":     nestedRoot,
			"adapterRootKind": "jsts_project",
		},
	})
	if err != nil {
		t.Fatalf("resolve workspace symbol scope: %v", err)
	}
	return resolved
}

func resolvedWorkspaceSymbolLanguageScope(t *testing.T, root string) ResolvedLSPToolScope {
	t.Helper()
	resolved, err := ResolveLSPToolScope(LSPToolScope{
		AgentID:               "agent-js",
		ThreadID:              "thread-js",
		CWD:                   root,
		WorkspaceRoots:        []string{root},
		Family:                "lsp",
		LanguageID:            "javascript",
		TargetPath:            root,
		WorkspaceRoot:         root,
		LanguageWorkspaceRoot: root,
		ProjectRoot:           root,
		RootKind:              "jsts_project",
		LanguageSpecific: map[string]string{
			"adapterRoot":     root,
			"adapterRootKind": "jsts_project",
		},
	})
	if err != nil {
		t.Fatalf("resolve workspace symbol language scope: %v", err)
	}
	return resolved
}

type workspaceSymbolScopeFactory struct {
	rootDirs   []string
	client     *workspaceSymbolScopeClient
	symbolName string
	symbolURI  string
}

func (f *workspaceSymbolScopeFactory) NewClient(rootDir string, _ protocol.NotificationHandler) (Client, error) {
	f.rootDirs = append(f.rootDirs, rootDir)
	f.client = &workspaceSymbolScopeClient{
		genericMatrixClient: &genericMatrixClient{documents: map[string]string{}},
		symbolName:          f.symbolName,
		symbolURI:           f.symbolURI,
	}
	return f.client, nil
}

func (f *workspaceSymbolScopeFactory) rootDirAt(t *testing.T, index int) string {
	t.Helper()
	if index < 0 || index >= len(f.rootDirs) {
		t.Fatalf("factory root dir %d out of range; roots=%#v", index, f.rootDirs)
	}
	return f.rootDirs[index]
}

func (f *workspaceSymbolScopeFactory) clientAt(t *testing.T) *workspaceSymbolScopeClient {
	t.Helper()
	if f.client == nil {
		t.Fatal("client was not created")
	}
	return f.client
}

type workspaceSymbolScopeClient struct {
	*genericMatrixClient
	requestScope  ResolvedLSPToolScope
	requestParams protocol.WorkspaceSymbolParams
	symbolName    string
	symbolURI     string
}

func (c *workspaceSymbolScopeClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method != protocol.MethodWorkspaceSymbol {
		return nil, fmt.Errorf("unexpected request method %q, want %q", method, protocol.MethodWorkspaceSymbol)
	}
	resolved, ok := resolvedLSPToolScopeFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("workspace symbol request missing resolved scope")
	}
	c.requestScope = resolved
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &c.requestParams); err != nil {
		return nil, err
	}
	return json.Marshal([]protocol.WorkspaceSymbol{{
		Name:     c.symbolName,
		Kind:     int(protocol.SymbolKindFunction),
		Location: protocol.WorkspaceSymbolLocation{URI: c.symbolURI},
	}})
}

func assertCodeActionOpenedJavaScript(t *testing.T, client *codeActionParamClient, target string) {
	t.Helper()
	if !client.opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("opened documents = %#v, want target opened as javascript", client.openEvents())
	}
}

func assertCodeActionParams(t *testing.T, params json.RawMessage, target string) {
	t.Helper()
	if len(params) == 0 {
		t.Fatal("code action params were not captured")
	}
	var decoded protocol.CodeActionParams
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("decode code action params: %v; raw=%s", err, params)
	}
	if decoded.TextDocument.URI != fileURIFromPath(target) {
		t.Fatalf("code action textDocument.uri = %q, want %q", decoded.TextDocument.URI, fileURIFromPath(target))
	}
	assertCodeActionDiagnosticsArray(t, params)
}

func assertCodeActionDiagnosticsArray(t *testing.T, params json.RawMessage) {
	t.Helper()
	rawDiagnostics := rawCodeActionDiagnostics(t, params)
	diagnosticsArray := decodeJSONArray(t, rawDiagnostics)
	if len(diagnosticsArray) != 0 {
		t.Fatalf("code action diagnostics = %#v, want empty array", diagnosticsArray)
	}
}

func rawCodeActionDiagnostics(t *testing.T, params json.RawMessage) json.RawMessage {
	t.Helper()
	var raw struct {
		Context map[string]json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(params, &raw); err != nil {
		t.Fatalf("decode raw code action params: %v; raw=%s", err, params)
	}
	rawDiagnostics, ok := raw.Context["diagnostics"]
	if !ok {
		t.Fatalf("code action params missing diagnostics field: %s", params)
	}
	return rawDiagnostics
}

func decodeJSONArray(t *testing.T, raw json.RawMessage) []any {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode diagnostics JSON: %v; raw=%s", err, raw)
	}
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("code action diagnostics JSON type = %T, want empty array; raw=%s", value, raw)
	}
	return array
}

type codeActionParamFactory struct {
	client *codeActionParamClient
}

func (f *codeActionParamFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.client = &codeActionParamClient{
		genericMatrixClient: &genericMatrixClient{documents: map[string]string{}},
	}
	return f.client, nil
}

func (f *codeActionParamFactory) clientAt(t *testing.T) *codeActionParamClient {
	t.Helper()
	if f.client == nil {
		t.Fatal("client was not created")
	}
	return f.client
}

type codeActionParamClient struct {
	*genericMatrixClient
	codeActionParams json.RawMessage
}

func (c *codeActionParamClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method != protocol.MethodCodeAction {
		return nil, fmt.Errorf("unexpected request method %q, want %q", method, protocol.MethodCodeAction)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	c.codeActionParams = raw
	return json.RawMessage("[]"), nil
}

func writeJavaScriptHierarchyFixture(t *testing.T) (string, string) {
	t.Helper()
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "export function greet(name) { return name }\n")
	return root, target
}

type unsupportedHierarchyFactory struct {
	unsupportedMethod string
	client            *unsupportedHierarchyClient
}

func (f *unsupportedHierarchyFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.client = &unsupportedHierarchyClient{
		genericMatrixClient: &genericMatrixClient{documents: map[string]string{}},
		unsupportedMethod:   f.unsupportedMethod,
	}
	return f.client, nil
}

func (f *unsupportedHierarchyFactory) clientAt(t *testing.T) *unsupportedHierarchyClient {
	t.Helper()
	if f.client == nil {
		t.Fatal("client was not created")
	}
	return f.client
}

type unsupportedHierarchyClient struct {
	*genericMatrixClient
	unsupportedMethod string
}

func (c *unsupportedHierarchyClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != c.unsupportedMethod {
		return nil, fmt.Errorf("unexpected hierarchy request method %q, want %q", method, c.unsupportedMethod)
	}
	return nil, &responseError{Code: jsonRPCMethodNotFound, Message: "Unhandled method " + method}
}

type delayedHierarchyPrepareFactory struct {
	client *delayedHierarchyPrepareClient
}

func (f *delayedHierarchyPrepareFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.client = &delayedHierarchyPrepareClient{
		genericMatrixClient: &genericMatrixClient{documents: map[string]string{}},
	}
	return f.client, nil
}

func (f *delayedHierarchyPrepareFactory) clientAt(t *testing.T) *delayedHierarchyPrepareClient {
	t.Helper()
	if f.client == nil {
		t.Fatal("client was not created")
	}
	return f.client
}

type delayedHierarchyPrepareClient struct {
	*genericMatrixClient
	prepareCalls int
}

func (c *delayedHierarchyPrepareClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	uri := e2eRequestURI(params)
	rng := protocol.Range{Start: protocol.Position{Line: 0, Character: 7}, End: protocol.Position{Line: 0, Character: 12}}
	switch method {
	case protocol.MethodPrepareCallHierarchy:
		c.prepareCalls++
		if !c.opened(uri, "typescript") {
			return nil, fmt.Errorf("prepareCallHierarchy ran before target bootstrap; opens=%#v", c.openEvents())
		}
		if c.prepareCalls == 1 {
			return json.RawMessage("[]"), nil
		}
		return json.Marshal([]protocol.CallHierarchyItem{{
			Name:           "greet",
			Kind:           int(protocol.SymbolKindFunction),
			URI:            uri,
			Range:          rng,
			SelectionRange: rng,
		}})
	case protocol.MethodCallHierarchyOutgoing:
		return json.Marshal([]protocol.CallHierarchyOutgoingCall{{
			To: protocol.CallHierarchyItem{
				Name:           "printName",
				Kind:           int(protocol.SymbolKindFunction),
				URI:            uri,
				Range:          rng,
				SelectionRange: rng,
			},
			FromRanges: []protocol.Range{rng},
		}})
	default:
		return nil, fmt.Errorf("unexpected hierarchy request method %q", method)
	}
}

type unsupportedHierarchyDirectionFactory struct {
	unsupportedMethod string
	client            *unsupportedHierarchyDirectionClient
}

func (f *unsupportedHierarchyDirectionFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.client = &unsupportedHierarchyDirectionClient{
		genericMatrixClient: &genericMatrixClient{documents: map[string]string{}},
		unsupportedMethod:   f.unsupportedMethod,
	}
	return f.client, nil
}

type unsupportedHierarchyDirectionClient struct {
	*genericMatrixClient
	unsupportedMethod string
}

func (c *unsupportedHierarchyDirectionClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	switch method {
	case protocol.MethodPrepareCallHierarchy:
		return json.Marshal([]protocol.CallHierarchyItem{{
			Name:           "greet",
			Kind:           int(protocol.SymbolKindFunction),
			URI:            "file:///fixture/app.js",
			Range:          protocol.Range{},
			SelectionRange: protocol.Range{},
		}})
	case c.unsupportedMethod:
		return nil, &responseError{Code: jsonRPCMethodNotFound, Message: "Unhandled method " + method}
	default:
		return nil, fmt.Errorf("unexpected hierarchy request method %q", method)
	}
}
