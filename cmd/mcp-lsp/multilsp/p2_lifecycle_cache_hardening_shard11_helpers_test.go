package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func scopedManagerForTest(t *testing.T, mgr *manager, scope LSPToolScope) *manager {
	t.Helper()
	scoped, err := mgr.pool.ForScope(scope)
	if err != nil {
		t.Fatalf("ForScope(%#v): %v", scope, err)
	}
	typed, ok := scoped.Manager.(*manager)
	if !ok {
		t.Fatalf("ScopedManager.Manager type = %T, want *manager", scoped.Manager)
	}
	return typed
}

func mustBootstrapCoordinator(t *testing.T, mgr *manager) *bootstrapCoordinator {
	t.Helper()
	coordinator, err := bootstrapCoordinatorFor(mgr)
	if err != nil {
		t.Fatalf("bootstrapCoordinatorFor: %v", err)
	}
	return coordinator
}

func mustLSPCacheStore(t *testing.T, cfg lspCacheConfig) *lspCacheStore {
	t.Helper()
	store, err := newLSPCacheStore(cfg)
	if err != nil {
		t.Fatalf("newLSPCacheStore: %v", err)
	}
	return store
}

func mustLSPCacheStoreFromEnv(t *testing.T) *lspCacheStore {
	t.Helper()
	store, err := newLSPCacheStoreFromEnv(nil)
	if err != nil {
		t.Fatalf("newLSPCacheStoreFromEnv: %v", err)
	}
	return store
}

func cloneSnapshotsForShard(t *testing.T, pool *ManagerPool, shardIndex int) []pooledManager {
	t.Helper()
	if pool == nil || shardIndex < 0 || shardIndex >= len(pool.shards) {
		t.Fatalf("invalid shard index %d", shardIndex)
	}
	return pool.shards[shardIndex].snapshotClones()
}

func assertBusyReleaseScopeResult(t *testing.T, result ReleaseScopeResult, scoped *manager) {
	t.Helper()
	if result.BusyLeases == 0 {
		t.Fatalf("busy ReleaseScope result = %#v, want busy lease count", result)
	}
	if result.ClosedManagers != 0 {
		t.Fatalf("busy ReleaseScope result = %#v, want no closed managers", result)
	}
	if managerIsClosed(scoped) {
		t.Fatalf("busy ReleaseScope closed scoped manager unexpectedly: %#v", result)
	}
}

func assertDrainedReleaseScopeResult(t *testing.T, result ReleaseScopeResult, scoped *manager) {
	t.Helper()
	if result.BusyLeases != 0 {
		t.Fatalf("drain ReleaseScope result = %#v, want no busy leases", result)
	}
	if result.ClosedManagers != 1 {
		t.Fatalf("drain ReleaseScope result = %#v, want one closed manager", result)
	}
	if !result.Drained {
		t.Fatalf("drain ReleaseScope result = %#v, want Drained=true", result)
	}
	if !managerIsClosed(scoped) {
		t.Fatalf("drain ReleaseScope result = %#v closed=false, want drained close", result)
	}
}

type p2LifecycleFactory struct {
	mu                 sync.Mutex
	clients            []*p2LifecycleClient
	calls              []genericMatrixFactoryCall
	initializeFailures []error
	requestFailures    []error
}

func (f *p2LifecycleFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *p2LifecycleFactory) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := len(f.clients)
	client := &p2LifecycleClient{
		handler:           handler,
		healthy:           true,
		documents:         map[string]string{},
		initializeFailure: failureAt(f.initializeFailures, idx),
		requestFailure:    failureAt(f.requestFailures, idx),
	}
	f.clients = append(f.clients, client)
	f.calls = append(f.calls, genericMatrixFactoryCall{rootDir: rootDir, env: append([]string(nil), env...)})
	return client, nil
}

func (f *p2LifecycleFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *p2LifecycleFactory) callAt(t *testing.T, idx int) genericMatrixFactoryCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < 0 || idx >= len(f.calls) {
		t.Fatalf("factory call %d out of range; calls=%d", idx, len(f.calls))
	}
	return f.calls[idx]
}

func (f *p2LifecycleFactory) clientAt(t *testing.T, idx int) *p2LifecycleClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < 0 || idx >= len(f.clients) {
		t.Fatalf("factory client %d out of range; clients=%d", idx, len(f.clients))
	}
	return f.clients[idx]
}

func failureAt(values []error, idx int) error {
	if idx < 0 || idx >= len(values) {
		return nil
	}
	return values[idx]
}

type p2LifecycleClient struct {
	mu                sync.Mutex
	handler           protocol.NotificationHandler
	healthy           bool
	closed            bool
	documents         map[string]string
	opens             []genericOpenEvent
	requestLog        []string
	initializeFailure error
	requestFailure    error
	shutdownFailure   error
	closeFailure      error
}

func (c *p2LifecycleClient) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy && !c.closed
}

func (c *p2LifecycleClient) markUnhealthy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy = false
}

func (c *p2LifecycleClient) Initialize(context.Context, string) error {
	if c.initializeFailure != nil {
		c.markUnhealthy()
		return c.initializeFailure
	}
	return nil
}

func (c *p2LifecycleClient) Shutdown(context.Context) error { return c.shutdownFailure }

func (c *p2LifecycleClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	c.mu.Lock()
	c.requestLog = append(c.requestLog, method)
	c.mu.Unlock()
	if c.requestFailure != nil {
		c.markUnhealthy()
		return nil, c.requestFailure
	}
	return json.Marshal([]protocol.DocumentSymbol{{
		Name:           "rebuilt",
		Kind:           protocol.SymbolKindVariable,
		Range:          protocol.Range{},
		SelectionRange: protocol.Range{},
	}})
}

func (c *p2LifecycleClient) Notify(context.Context, string, any) error { return nil }

func (c *p2LifecycleClient) DidOpen(_ context.Context, uri, languageID string, _ int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opens = append(c.opens, genericOpenEvent{uri: uri, language: languageID})
	c.documents[uri] = text
	return nil
}

func (c *p2LifecycleClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}

func (c *p2LifecycleClient) DidClose(context.Context, string) error { return nil }

func (c *p2LifecycleClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.healthy = false
	return c.closeFailure
}

func (c *p2LifecycleClient) opened(uri, languageID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.opens {
		if event.uri == uri && event.language == languageID {
			return true
		}
	}
	return false
}

func (c *p2LifecycleClient) openEvents() []genericOpenEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]genericOpenEvent(nil), c.opens...)
}

func (c *p2LifecycleClient) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requestLog)
}

func (c *p2LifecycleClient) requestMethods() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requestLog...)
}

type p2DiagnosticsFactory struct {
	mu              sync.Mutex
	clients         []*p2DiagnosticsClient
	publishOnOpen   string
	didChangeErrors []error
	didCloseErrors  []error
}

func (f *p2DiagnosticsFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := len(f.clients)
	client := &p2DiagnosticsClient{
		handler:        handler,
		healthy:        true,
		documents:      map[string]string{},
		publishOnOpen:  f.publishOnOpen,
		didChangeError: failureAt(f.didChangeErrors, idx),
		didCloseError:  failureAt(f.didCloseErrors, idx),
	}
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *p2DiagnosticsFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

func (f *p2DiagnosticsFactory) clientAt(t *testing.T, idx int) *p2DiagnosticsClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx < 0 || idx >= len(f.clients) {
		t.Fatalf("diagnostics client %d out of range; clients=%d", idx, len(f.clients))
	}
	return f.clients[idx]
}

type p2DiagnosticsClient struct {
	mu             sync.Mutex
	handler        protocol.NotificationHandler
	healthy        bool
	closed         bool
	documents      map[string]string
	opens          []genericOpenEvent
	didChanges     []string
	publishOnOpen  string
	didChangeError error
	didCloseError  error
	didCloses      int
}

func (c *p2DiagnosticsClient) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy && !c.closed
}

func (c *p2DiagnosticsClient) Initialize(context.Context, string) error { return nil }
func (c *p2DiagnosticsClient) Shutdown(context.Context) error           { return nil }
func (c *p2DiagnosticsClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}
func (c *p2DiagnosticsClient) Notify(context.Context, string, any) error { return nil }

func (c *p2DiagnosticsClient) DidOpen(_ context.Context, uri, languageID string, _ int, text string) error {
	c.mu.Lock()
	c.opens = append(c.opens, genericOpenEvent{uri: uri, language: languageID})
	c.documents[uri] = text
	message := c.publishOnOpen
	handler := c.handler
	c.mu.Unlock()
	if message != "" && handler != nil {
		return handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: message}}})
	}
	return nil
}

func (c *p2DiagnosticsClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didChanges = append(c.didChanges, uri)
	if c.didChangeError != nil {
		c.healthy = false
		return c.didChangeError
	}
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}

func (c *p2DiagnosticsClient) DidClose(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didCloses++
	if c.didCloseError != nil {
		c.healthy = false
		return c.didCloseError
	}
	return nil
}

func (c *p2DiagnosticsClient) didCloseCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didCloses
}

func (c *p2DiagnosticsClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.healthy = false
	return nil
}

func (c *p2DiagnosticsClient) opened(uri, languageID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.opens {
		if event.uri == uri && event.language == languageID {
			return true
		}
	}
	return false
}

func (c *p2DiagnosticsClient) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.opens)
}

func (c *p2DiagnosticsClient) didChangeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.didChanges)
}

func setupPersistentCacheEnv(t *testing.T) (string, string) {
	t.Helper()
	root := canonicalScopePath(t.TempDir(), "")
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	return root, cacheDir
}

func writeCacheDiskState(t *testing.T, cacheDir string, state lspCacheDiskState) {
	t.Helper()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal cache state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, lspCacheFileName), payload, 0o644); err != nil {
		t.Fatalf("write cache state: %v", err)
	}
}

func readCacheDiskState(t *testing.T, cacheDir string) lspCacheDiskState {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(cacheDir, lspCacheFileName))
	if err != nil {
		t.Fatalf("read cache state: %v", err)
	}
	var state lspCacheDiskState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("decode cache state: %v payload=%s", err, payload)
	}
	return state
}

func assertPersistentCacheDocumentCount(t *testing.T, cacheDir string, want int) {
	t.Helper()
	path := filepath.Join(cacheDir, lspCacheFileName)
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if want == 0 {
			return
		}
		t.Fatalf("persistent cache file missing, want %d documents", want)
	}
	if err != nil {
		t.Fatalf("read persistent cache: %v", err)
	}
	var state lspCacheDiskState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatalf("decode persistent cache: %v payload=%s", err, payload)
	}
	if len(state.Documents) != want {
		t.Fatalf("persistent cache documents = %#v, want count %d", state.Documents, want)
	}
}
