package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestHoverRetriesRustAnalyzerContentModified(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"retry-hover\"\nversion = \"0.1.0\"\n")
	target := filepath.Join(root, "src", "main.rs")
	writeGenericTestFile(t, target, "fn main() {\n    let value = 1;\n}\n")

	client := &contentModifiedHoverClient{}
	mgr := NewManager(Config{
		WorkspaceRoot: root,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	}).(*manager)
	mgr.retryBaseDelay = time.Millisecond
	t.Cleanup(func() { _ = mgr.Close() })

	hover, err := mgr.Hover(ctxWithCWD(root, "agent-rust-content-modified", "thread-1"), target, protocol.Position{Line: 0, Character: 3})
	if err != nil {
		t.Fatalf("Hover() error = %v, want retry after content modified", err)
	}
	payload, err := json.Marshal(hover.Contents)
	if err != nil {
		t.Fatalf("marshal hover contents: %v", err)
	}
	if !strings.Contains(string(payload), "fn main") {
		t.Fatalf("Hover() contents = %s, want retried hover result", payload)
	}
	if got := client.hoverRequestCount(); got != 2 {
		t.Fatalf("hover request count = %d, want first failure plus one retry", got)
	}
}

type contentModifiedHoverClient struct {
	noopClient

	mu            sync.Mutex
	hoverRequests int
}

func (c *contentModifiedHoverClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != protocol.MethodHover {
		return json.RawMessage("null"), nil
	}

	c.mu.Lock()
	c.hoverRequests++
	attempt := c.hoverRequests
	c.mu.Unlock()

	if attempt == 1 {
		return nil, &responseError{Code: lspErrorContentModified, Message: "content modified"}
	}
	return json.Marshal(protocol.HoverResult{
		Contents: protocol.MarkupContent{
			Kind:  "markdown",
			Value: "```rust\nfn main()\n```",
		},
	})
}

func (c *contentModifiedHoverClient) hoverRequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hoverRequests
}

func TestIdempotentRequestRebuildsWhenIdleRecyclerDetachesBeforeLease(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"idle-wake\"\nversion = \"0.1.0\"\n")
	target := filepath.Join(root, "src", "main.rs")
	writeGenericTestFile(t, target, "fn main() {}\n")

	factory := &idleWakeClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    ClientFactoryFunc(factory.newClient),
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := ctxWithCWD(root, "agent-idle-wake", "thread-1")
	clientReady := make(chan Client, 1)
	resultReady := make(chan idleWakeRequestResult, 1)
	requestControl := startIdleWakeRequest(t, ctx, mgr, target, clientReady, resultReady)

	original := awaitIdleWakeClient(t, clientReady, resultReady)

	detached := detachEnsuredIdleWakeClient(t, mgr, original)
	_, closeErr := shutdownWorkspaceClient(detached.client)
	if closeErr != nil {
		t.Fatalf("close detached idle client: %v", closeErr)
	}
	requestControl.release()

	result := awaitIdleWakeRequestResult(t, resultReady)
	assertIdleWakeRequestResult(t, result, factory)
}

type idleWakeRequestControl struct {
	continueRequest chan struct{}
	done            chan struct{}
	releaseOnce     sync.Once
}

// startIdleWakeRequest 通过 SafeGo 启动测试请求，并在 cleanup 中释放阻塞点、等待 goroutine 退出。
func startIdleWakeRequest(
	t *testing.T,
	ctx context.Context,
	mgr *manager,
	target string,
	clientReady chan<- Client,
	resultReady chan<- idleWakeRequestResult,
) *idleWakeRequestControl {
	t.Helper()
	control := &idleWakeRequestControl{
		continueRequest: make(chan struct{}),
		done:            make(chan struct{}),
	}
	runtimesafe.SafeGo(ctx, nil, "multilsp.idleWakeRequest.test", func(runCtx context.Context) {
		defer close(control.done)
		runIdleWakeRequest(runCtx, mgr, target, clientReady, control.continueRequest, resultReady)
	})
	t.Cleanup(func() {
		control.release()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-control.done:
		case <-timer.C:
			t.Errorf("timed out waiting for controlled idle wake request goroutine")
		}
	})
	return control
}

// release 只关闭一次请求阻塞点。
func (c *idleWakeRequestControl) release() {
	c.releaseOnce.Do(func() { close(c.continueRequest) })
}

// runIdleWakeRequest 模拟 ensureClient 返回旧 client 后，请求被 recycler 插入并摘除的窗口。
func runIdleWakeRequest(
	ctx context.Context,
	mgr *manager,
	target string,
	clientReady chan<- Client,
	continueRequest <-chan struct{},
	resultReady chan<- idleWakeRequestResult,
) {
	client, err := mgr.ensureClientForFile(ctx, target, "rust")
	if err != nil {
		resultReady <- idleWakeRequestResult{err: err}
		return
	}
	clientReady <- client
	<-continueRequest
	raw, requestErr := mgr.request(ctx, client, protocol.MethodHover, protocol.HoverParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: fileURIFromPath(target)},
		Position:     protocol.Position{Line: 0, Character: 3},
	})
	resultReady <- idleWakeRequestResult{raw: raw, err: requestErr}
}

// awaitIdleWakeClient 等待 ensureClient 暴露旧 client，并把初始化失败或超时立即报告给测试。
func awaitIdleWakeClient(
	t *testing.T,
	clientReady <-chan Client,
	resultReady <-chan idleWakeRequestResult,
) Client {
	t.Helper()
	select {
	case client := <-clientReady:
		return client
	case result := <-resultReady:
		t.Fatalf("ensure client before idle detach: %v", result.err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ensureClient to return")
	}
	return nil
}

// awaitIdleWakeRequestResult 等待首个 wake 请求完成，超时即失败。
func awaitIdleWakeRequestResult(t *testing.T, resultReady <-chan idleWakeRequestResult) idleWakeRequestResult {
	t.Helper()
	select {
	case result := <-resultReady:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first request after idle detach")
	}
	return idleWakeRequestResult{}
}

// assertIdleWakeRequestResult 验证旧 client 未收到请求且 replacement 只收到一次首醒请求。
func assertIdleWakeRequestResult(t *testing.T, result idleWakeRequestResult, factory *idleWakeClientFactory) {
	t.Helper()
	if result.err != nil {
		t.Fatalf("first request after idle detach error = %v, want lazy rebuild", result.err)
	}
	if !strings.Contains(string(result.raw), "awake") {
		t.Fatalf("first request result = %s, want replacement client response", result.raw)
	}
	clients := factory.snapshot()
	if len(clients) != 2 {
		t.Fatalf("factory client count = %d, want original plus lazy replacement", len(clients))
	}
	if got := clients[0].requestCount(); got != 0 {
		t.Fatalf("detached client request count = %d, want request blocked before dispatch", got)
	}
	if got := clients[1].requestCount(); got != 1 {
		t.Fatalf("replacement client request count = %d, want one first-wake request", got)
	}
}

func TestUnboundClientNotificationStopsBeforeDispatch(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"idle-notify\"\nversion = \"0.1.0\"\n")
	target := filepath.Join(root, "src", "main.rs")
	writeGenericTestFile(t, target, "fn main() {}\n")

	factory := &idleWakeClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    ClientFactoryFunc(factory.newClient),
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	client, err := mgr.ensureClientForFile(ctxWithCWD(root, "agent-idle-notify", "thread-1"), target, "rust")
	if err != nil {
		t.Fatalf("ensure client before notification detach: %v", err)
	}
	dispatched := false
	callbackErr := mgr.withPooledClient(client, func() error {
		dispatched = true
		return ErrClientNotBound
	})
	if !dispatched {
		t.Fatal("bound client callback did not run")
	}
	if callbackErr == nil || errors.Is(callbackErr, ErrClientNotBound) {
		t.Fatalf("callback error = %v, reserved ErrClientNotBound must only mark pre-dispatch failure", callbackErr)
	}
	if !strings.Contains(callbackErr.Error(), "reserved ErrClientNotBound after dispatch") {
		t.Fatalf("callback error = %v, want reserved marker violation", callbackErr)
	}

	detached := detachEnsuredIdleWakeClient(t, mgr, client)
	_, closeErr := shutdownWorkspaceClient(detached.client)
	if closeErr != nil {
		t.Fatalf("close detached notification client: %v", closeErr)
	}

	dispatched = false
	err = mgr.withPooledClient(client, func() error {
		dispatched = true
		return client.Notify(context.Background(), "workspace/didChangeConfiguration", struct{}{})
	})
	if !errors.Is(err, ErrClientNotBound) {
		t.Fatalf("notification lease error = %v, want ErrClientNotBound", err)
	}
	if dispatched {
		t.Fatal("notification callback ran after client detach; non-idempotent notification must not be replayed")
	}
	if got := len(factory.snapshot()); got != 1 {
		t.Fatalf("factory client count = %d, want no implicit rebuild for notification", got)
	}
}

func TestUnboundIdempotentRequestWithoutDocumentURIFailsFast(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"idle-no-uri\"\nversion = \"0.1.0\"\n")
	target := filepath.Join(root, "src", "main.rs")
	writeGenericTestFile(t, target, "fn main() {}\n")

	factory := &idleWakeClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    ClientFactoryFunc(factory.newClient),
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := ctxWithCWD(root, "agent-idle-no-uri", "thread-1")
	client, err := mgr.ensureClientForFile(ctx, target, "rust")
	if err != nil {
		t.Fatalf("ensure client before URI-less request detach: %v", err)
	}
	detached := detachEnsuredIdleWakeClient(t, mgr, client)
	_, closeErr := shutdownWorkspaceClient(detached.client)
	if closeErr != nil {
		t.Fatalf("close detached URI-less request client: %v", closeErr)
	}

	_, err = mgr.request(ctx, client, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: "main"})
	if !errors.Is(err, ErrClientNotBound) {
		t.Fatalf("URI-less request error = %v, want ErrClientNotBound", err)
	}
	if err == nil || !strings.Contains(err.Error(), "has no document URI") {
		t.Fatalf("URI-less request error = %v, want fail-fast document URI context", err)
	}
	if !strings.Contains(err.Error(), protocol.MethodWorkspaceSymbol+":") {
		t.Fatalf("URI-less request error = %v, want method context", err)
	}
	if got := len(factory.snapshot()); got != 1 {
		t.Fatalf("factory client count = %d, want no guessed workspace rebuild", got)
	}
}

type idleWakeRequestResult struct {
	raw json.RawMessage
	err error
}

type idleWakeClientFactory struct {
	mu      sync.Mutex
	clients []*idleWakeClient
}

func (f *idleWakeClientFactory) newClient(string, protocol.NotificationHandler) (Client, error) {
	client := &idleWakeClient{}
	f.mu.Lock()
	f.clients = append(f.clients, client)
	f.mu.Unlock()
	return client, nil
}

func (f *idleWakeClientFactory) snapshot() []*idleWakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*idleWakeClient(nil), f.clients...)
}

type idleWakeClient struct {
	noopClient

	mu       sync.Mutex
	requests int
}

func (c *idleWakeClient) Request(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != protocol.MethodHover {
		return json.RawMessage("null"), nil
	}
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
	return json.Marshal(protocol.HoverResult{
		Contents: protocol.MarkupContent{Kind: "plaintext", Value: "awake"},
	})
}

func (c *idleWakeClient) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func detachEnsuredIdleWakeClient(t *testing.T, mgr *manager, client Client) *workspaceClient {
	t.Helper()
	var key string
	mgr.mu.Lock()
	for candidateKey, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client == client {
			key = candidateKey
			workspace.generation = 1
			workspace.state = workspaceStateIdleCountdown
			workspace.idleSince = time.Now().Add(-time.Hour)
			workspace.lastActivity = workspace.idleSince
			break
		}
	}
	mgr.mu.Unlock()
	if key == "" {
		t.Fatal("ensured client was not bound before idle detach")
	}
	detached := detachWorkspaceClientGeneration(mgr, key, client, 1, time.Now().Add(-time.Minute))
	if detached == nil || detached.client == nil {
		t.Fatal("idle recycler did not detach the ensured client")
	}
	return detached
}

func TestRebuildDeadClientRetainsCleanupOwnerUntilCloseSucceeds(t *testing.T) {
	closeFailure := errors.New("close failed once")
	original := &cleanupRetryClient{closeErrors: []error{closeFailure, nil}}
	factory := &cleanupRetryFactory{}
	root := t.TempDir()
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    ClientFactoryFunc(factory.newClient),
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close manager after cleanup retry test: %v", err)
		}
	})
	cfg := workspaceConfig{
		key:        "cleanup-retry",
		rootPath:   root,
		rootURI:    fileURIFromPath(root),
		languageID: "rust",
	}
	mgr.mu.Lock()
	mgr.workspaces[cfg.key] = &workspaceClient{
		key:          cfg.key,
		rootPath:     cfg.rootPath,
		rootURI:      cfg.rootURI,
		languageID:   cfg.languageID,
		client:       original,
		generation:   1,
		state:        workspaceStateActive,
		lastActivity: time.Now(),
	}
	mgr.mu.Unlock()

	replacement, err := mgr.rebuildClientAfterFailure(context.Background(), original, false)
	assertFailedCleanupRetry(t, mgr, cfg.key, factory, original, replacement, err, closeFailure)

	replacement, err = mgr.rebuildClientAfterFailure(context.Background(), original, false)
	assertSuccessfulCleanupRetry(t, mgr, cfg.key, factory, original, replacement, err)
}

func assertFailedCleanupRetry(
	t *testing.T,
	mgr *manager,
	key string,
	factory *cleanupRetryFactory,
	original *cleanupRetryClient,
	replacement Client,
	err error,
	wantErr error,
) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("first rebuild error = %v, want close failure", err)
	}
	if replacement != nil {
		t.Fatalf("first rebuild replacement = %T, want nil until cleanup succeeds", replacement)
	}
	if got := factory.callCount(); got != 0 {
		t.Fatalf("factory calls after failed cleanup = %d, want 0", got)
	}
	if got := boundClientForKey(mgr, key); got != original {
		t.Fatalf("cleanup owner after failed Close = %T, want original client", got)
	}
}

func assertSuccessfulCleanupRetry(
	t *testing.T,
	mgr *manager,
	key string,
	factory *cleanupRetryFactory,
	original *cleanupRetryClient,
	replacement Client,
	err error,
) {
	t.Helper()
	if err != nil {
		t.Fatalf("retry rebuild error = %v", err)
	}
	if replacement == nil || replacement == original {
		t.Fatalf("retry rebuild replacement = %T, want one new client", replacement)
	}
	if got := original.closeCallCount(); got != 2 {
		t.Fatalf("original Close calls = %d, want failed attempt plus successful retry", got)
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("factory calls after cleanup retry = %d, want exactly 1 replacement", got)
	}
	if got := boundClientForKey(mgr, key); got != replacement {
		t.Fatalf("bound client after cleanup retry = %T, want replacement", got)
	}
}

type cleanupRetryFactory struct {
	mu      sync.Mutex
	clients []*cleanupRetryClient
}

func (f *cleanupRetryFactory) newClient(string, protocol.NotificationHandler) (Client, error) {
	client := &cleanupRetryClient{}
	f.mu.Lock()
	f.clients = append(f.clients, client)
	f.mu.Unlock()
	return client, nil
}

func (f *cleanupRetryFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.clients)
}

type cleanupRetryClient struct {
	noopClient

	mu          sync.Mutex
	closeErrors []error
	closeCalls  int
}

func (c *cleanupRetryClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := c.closeCalls
	c.closeCalls++
	if index < len(c.closeErrors) {
		return c.closeErrors[index]
	}
	return nil
}

func (c *cleanupRetryClient) closeCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls
}

func boundClientForKey(mgr *manager, key string) Client {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	workspace := mgr.workspaces[key]
	if workspace == nil {
		return nil
	}
	return workspace.client
}
