package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

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
	factory := &delayedBootstrapDiagnosticsFactory{
		goroutines: newTestGoroutineGroup(t),
		openDelay:  80 * time.Millisecond,
		readyDelay: 10 * time.Millisecond,
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
		t.Fatalf("WaitDiagnosticsStable() error = %v, want diagnostics wait budget to start after bootstrap succeeds", err)
	}
	client := factory.currentClient(t)
	if got := client.openCount(); got != 1 {
		t.Fatalf("DidOpen count = %d, want one bootstrap sync", got)
	}
}

func diagnosticsDeadlineContext(root string, timeout time.Duration) (context.Context, context.CancelFunc) {
	scope := common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}
	return context.WithTimeout(common.WithToolScope(context.Background(), scope), timeout)
}

type delayedBootstrapDiagnosticsFactory struct {
	goroutines *testGoroutineGroup
	openDelay  time.Duration
	readyDelay time.Duration
	client     *delayedBootstrapDiagnosticsClient
}

func (f *delayedBootstrapDiagnosticsFactory) NewClient(_ string, handler protocol.NotificationHandler) (Client, error) {
	f.client = &delayedBootstrapDiagnosticsClient{
		goroutines: f.goroutines,
		openDelay:  f.openDelay,
		readyDelay: f.readyDelay,
		handler:    handler,
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
	goroutines *testGoroutineGroup
	mu         sync.Mutex
	openDelay  time.Duration
	readyDelay time.Duration
	handler    protocol.NotificationHandler
	opens      int
}

func (c *delayedBootstrapDiagnosticsClient) Initialize(context.Context, string) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) Shutdown(context.Context) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}

func (c *delayedBootstrapDiagnosticsClient) Notify(context.Context, string, any) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) DidOpen(ctx context.Context, uri, _ string, _ int, _ string) error {
	if err := sleepContext(ctx, c.openDelay); err != nil {
		return err
	}
	c.mu.Lock()
	c.opens++
	c.mu.Unlock()
	c.goroutines.Go(func() { c.publishDiagnosticsAfterDelay(ctx, uri) })
	return nil
}

func (c *delayedBootstrapDiagnosticsClient) DidChange(ctx context.Context, uri string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	return c.DidOpen(ctx, uri, "", 0, "")
}

func (c *delayedBootstrapDiagnosticsClient) DidClose(context.Context, string) error { return nil }

func (c *delayedBootstrapDiagnosticsClient) Close() error { return nil }

func (c *delayedBootstrapDiagnosticsClient) publishDiagnosticsAfterDelay(ctx context.Context, uri string) {
	if err := sleepContext(ctx, c.readyDelay); err != nil {
		return
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
