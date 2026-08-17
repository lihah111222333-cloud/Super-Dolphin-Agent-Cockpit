package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestDiagnosticsFailsFastWhenClientReportsTransportUnavailable(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"diagnostics-unavailable"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", "export const value = 1\\n")
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    unavailableDiagnosticsFactory{},
		DisableInitialWorkspaceBootstrap: true,
	})
	ctx, cancel := diagnosticsDeadlineContext(root, time.Second)
	defer cancel()
	_, err := mgr.Diagnostics(ctx, []string{fileURIFromPath(target)})
	if !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Diagnostics() error = %v, want ErrTransportClosed", err)
	}
}

func TestWaitDiagnosticsStableUsesManagerDeadlineBeforeCallerContext(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"diagnostics-manager-deadline"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", strings.Join([]string{
		"export function value() {",
		"  return 1;",
		"}",
		"",
	}, "\n"))
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:           root,
		DiagnosticsInitialDelay: time.Millisecond,
		DiagnosticsPollInterval: time.Millisecond,
		DiagnosticsMaxWait:      20 * time.Millisecond,
	})
	ctx, cancel := diagnosticsDeadlineContext(root, 250*time.Millisecond)
	defer cancel()
	uri := fileURIFromPath(target)

	started := time.Now()
	err := mgr.WaitDiagnosticsStable(ctx, []string{uri})
	elapsed := time.Since(started)
	if err == nil || !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want ErrDiagnosticsNotReady", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("caller context expired before manager deadline returned: %v", ctx.Err())
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("WaitDiagnosticsStable() elapsed = %s, want manager deadline before caller context", elapsed)
	}
}

func TestWaitDiagnosticsStableStartsDeadlineAfterBootstrapSucceeds(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"diagnostics-bootstrap-budget"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", "export const value = 1\n")
	openStarted := make(chan struct{})
	releaseOpen := make(chan struct{})
	bootstrapComplete := make(chan struct{})
	releaseDiagnostics := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	factory := &delayedBootstrapDiagnosticsFactory{
		goroutines:         goroutines,
		openStarted:        openStarted,
		releaseOpen:        releaseOpen,
		bootstrapComplete:  bootstrapComplete,
		releaseDiagnostics: releaseDiagnostics,
	}
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		DiagnosticsInitialDelay:          time.Millisecond,
		DiagnosticsPollInterval:          time.Millisecond,
		DiagnosticsMaxWait:               30 * time.Millisecond,
		DisableInitialWorkspaceBootstrap: true,
	})
	ctx, cancel := diagnosticsDeadlineContext(root, time.Second)
	defer cancel()
	uri := fileURIFromPath(target)

	resultCh := make(chan error, 1)
	goroutines.Go(func() {
		resultCh <- mgr.WaitDiagnosticsStable(ctx, []string{uri})
	})
	select {
	case <-openStarted:
	case <-ctx.Done():
		t.Fatalf("DidOpen did not start before caller context finished: %v", ctx.Err())
	}
	bootstrapHold := time.NewTimer(80 * time.Millisecond)
	select {
	case <-bootstrapHold.C:
	case <-ctx.Done():
		bootstrapHold.Stop()
		t.Fatalf("caller context finished while holding bootstrap past diagnostics budget: %v", ctx.Err())
	}
	close(releaseOpen)
	select {
	case <-bootstrapComplete:
	case <-ctx.Done():
		t.Fatalf("DidOpen did not complete after release: %v", ctx.Err())
	}
	close(releaseDiagnostics)
	if err := <-resultCh; err != nil {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want diagnostics wait budget to start after bootstrap succeeds", err)
	}
	client := factory.currentClient(t)
	if got := client.openCount(); got != 1 {
		t.Fatalf("DidOpen count = %d, want one bootstrap sync", got)
	}
}

func TestWaitDiagnosticsStableDoesNotApplyStableDeadlineToInitialPullDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"diagnostics-pull-budget"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", "export const value = 1\n")
	factory := &delayedPullDiagnosticsFactory{
		pullDelay: 80 * time.Millisecond,
	}
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		DiagnosticsInitialDelay:          time.Millisecond,
		DiagnosticsPollInterval:          time.Millisecond,
		DiagnosticsMaxWait:               30 * time.Millisecond,
		DisableInitialWorkspaceBootstrap: true,
	})
	ctx, cancel := diagnosticsDeadlineContext(root, time.Second)
	defer cancel()
	uri := fileURIFromPath(target)

	if err := mgr.WaitDiagnosticsStable(ctx, []string{uri}); err != nil {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want slow initial pull diagnostics to use caller context before stable wait deadline", err)
	}
	if got := factory.currentClient(t).requestCount(); got != 1 {
		t.Fatalf("pull diagnostics request count = %d, want one initial pull", got)
	}
}

func TestInternalBootstrapStillWaitsForDiagnosticsWhileDirectOpenSkips(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"diagnostics-bootstrap-origin"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", "export const value = 1\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		DiagnosticsInitialDelay:          time.Millisecond,
		DiagnosticsPollInterval:          time.Millisecond,
		DiagnosticsMaxWait:               20 * time.Millisecond,
		DisableInitialWorkspaceBootstrap: true,
	})
	ctx, cancel := diagnosticsDeadlineContext(root, 250*time.Millisecond)
	defer cancel()
	ref, err := mgr.resolveDocumentRef(ctx, target, "javascript")
	if err != nil {
		t.Fatalf("resolve diagnostics origin fixture: %v", err)
	}
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("internal BootstrapDocument: %v", err)
	}
	if err := mgr.waitDocumentDiagnosticsReady(ctx, ref); !errors.Is(err, lspmanager.ErrDiagnosticsNotReady) {
		t.Fatalf("internal bootstrap diagnostics wait error = %v, want ErrDiagnosticsNotReady", err)
	}
	if err := mgr.DidOpen(ctx, ref.uri, ref.languageID, 2, "export const value = 2\n"); err != nil {
		t.Fatalf("direct DidOpen after bootstrap: %v", err)
	}
	if err := mgr.waitDocumentDiagnosticsReady(ctx, ref); err != nil {
		t.Fatalf("direct user-opened document should skip diagnostics wait: %v", err)
	}
}

func diagnosticsDeadlineContext(root string, timeout time.Duration) (context.Context, context.CancelFunc) {
	scope := common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}
	return context.WithTimeout(common.WithToolScope(context.Background(), scope), timeout)
}

type delayedBootstrapDiagnosticsFactory struct {
	goroutines         *testGoroutineGroup
	openStarted        chan<- struct{}
	releaseOpen        <-chan struct{}
	bootstrapComplete  chan<- struct{}
	releaseDiagnostics <-chan struct{}
	client             *delayedBootstrapDiagnosticsClient
}

func (f *delayedBootstrapDiagnosticsFactory) NewClient(_ string, handler protocol.NotificationHandler) (Client, error) {
	f.client = &delayedBootstrapDiagnosticsClient{
		goroutines:         f.goroutines,
		openStarted:        f.openStarted,
		releaseOpen:        f.releaseOpen,
		bootstrapComplete:  f.bootstrapComplete,
		releaseDiagnostics: f.releaseDiagnostics,
		handler:            handler,
	}
	return f.client, nil
}

func (f *delayedBootstrapDiagnosticsFactory) currentClient(t *testing.T) *delayedBootstrapDiagnosticsClient {
	t.Helper()
	if f.client == nil {
		t.Fatal("client was not created")
	}
	return f.client
}

type delayedBootstrapDiagnosticsClient struct {
	goroutines         *testGoroutineGroup
	mu                 sync.Mutex
	openStarted        chan<- struct{}
	releaseOpen        <-chan struct{}
	bootstrapComplete  chan<- struct{}
	releaseDiagnostics <-chan struct{}
	handler            protocol.NotificationHandler
	opens              int
}

func (c *delayedBootstrapDiagnosticsClient) Initialize(context.Context, string) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) Shutdown(context.Context) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}

func (c *delayedBootstrapDiagnosticsClient) Notify(context.Context, string, any) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) DidOpen(ctx context.Context, uri, _ string, _ int, _ string) error {
	if c.openStarted != nil {
		close(c.openStarted)
	}
	if c.releaseOpen != nil {
		select {
		case <-c.releaseOpen:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	c.opens++
	c.mu.Unlock()
	c.goroutines.Go(func() { c.publishDiagnosticsAfterRelease(ctx, uri) })
	if c.bootstrapComplete != nil {
		close(c.bootstrapComplete)
	}
	return nil
}

func (c *delayedBootstrapDiagnosticsClient) DidChange(ctx context.Context, uri string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	return c.DidOpen(ctx, uri, "", 0, "")
}

func (c *delayedBootstrapDiagnosticsClient) DidClose(context.Context, string) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) Close() error { return nil }

func (c *delayedBootstrapDiagnosticsClient) publishDiagnosticsAfterRelease(ctx context.Context, uri string) {
	if c.releaseDiagnostics != nil {
		select {
		case <-c.releaseDiagnostics:
		case <-ctx.Done():
			return
		}
	}
	if c.handler != nil {
		_ = c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri})
	}
}

func (c *delayedBootstrapDiagnosticsClient) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens
}

type delayedPullDiagnosticsFactory struct {
	pullDelay time.Duration
	client    *delayedPullDiagnosticsClient
}

func (f *delayedPullDiagnosticsFactory) NewClient(_ string, handler protocol.NotificationHandler) (Client, error) {
	f.client = &delayedPullDiagnosticsClient{
		handler:   handler,
		pullDelay: f.pullDelay,
	}
	return f.client, nil
}

func (f *delayedPullDiagnosticsFactory) currentClient(t *testing.T) *delayedPullDiagnosticsClient {
	t.Helper()
	if f.client == nil {
		t.Fatal("client was not created")
	}
	return f.client
}

type delayedPullDiagnosticsClient struct {
	mu          sync.Mutex
	handler     protocol.NotificationHandler
	pullDelay   time.Duration
	requests    int
	openedURI   string
	openedReady bool
}

func (c *delayedPullDiagnosticsClient) Initialize(context.Context, string) error { return nil }

func (c *delayedPullDiagnosticsClient) Shutdown(context.Context) error { return nil }

func (c *delayedPullDiagnosticsClient) Request(ctx context.Context, method string, _ any) (json.RawMessage, error) {
	if method != protocol.MethodTextDocumentDiagnostic {
		return json.RawMessage("null"), nil
	}
	c.mu.Lock()
	c.requests++
	uri := c.openedURI
	c.mu.Unlock()
	if err := sleepContext(ctx, c.pullDelay); err != nil {
		return nil, err
	}
	raw := json.RawMessage(`{"kind":"full","items":[{"message":"delayed pulled diagnostic"}]}`)
	if c.handler != nil {
		_ = c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: []protocol.Diagnostic{{Message: "delayed pulled diagnostic"}},
		})
	}
	return raw, nil
}

func (c *delayedPullDiagnosticsClient) ServerCapabilities() protocol.ServerCapabilities {
	return protocol.ServerCapabilities{DiagnosticProvider: true}
}

func (c *delayedPullDiagnosticsClient) Notify(context.Context, string, any) error { return nil }

func (c *delayedPullDiagnosticsClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}

func (c *delayedPullDiagnosticsClient) DidChange(_ context.Context, uri string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	c.openedURI = uri
	c.openedReady = true
	c.mu.Unlock()
	return nil
}

func (c *delayedPullDiagnosticsClient) DidClose(context.Context, string) error { return nil }

func (c *delayedPullDiagnosticsClient) Close() error { return nil }

func (c *delayedPullDiagnosticsClient) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

type unavailableDiagnosticsFactory struct{}

func (unavailableDiagnosticsFactory) NewClient(_ string, _ protocol.NotificationHandler) (Client, error) {
	return unavailableDiagnosticsClient{}, nil
}

type unavailableDiagnosticsClient struct{}

func (unavailableDiagnosticsClient) Initialize(context.Context, string) error { return nil }
func (unavailableDiagnosticsClient) Shutdown(context.Context) error           { return nil }
func (unavailableDiagnosticsClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("[]"), nil
}
func (unavailableDiagnosticsClient) Notify(context.Context, string, any) error { return nil }
func (unavailableDiagnosticsClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}
func (unavailableDiagnosticsClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}
func (unavailableDiagnosticsClient) DidClose(context.Context, string) error { return nil }
func (unavailableDiagnosticsClient) Close() error                           { return nil }
func (unavailableDiagnosticsClient) Healthy() bool                          { return false }
