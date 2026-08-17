//go:build windows

package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// windows122ManagerTestClient 是 manager.DidOpen 的最小确定性替身；它不启动进程，
// 只验证 Windows 启动恢复的 factory/cleanup/DidOpen 账本，不改变生产 transport。
type windows122ManagerTestClient struct {
	mu         sync.Mutex
	initialize error
	closeError error
	shutdowns  int
	closes     int
	opened     int
}

func (c *windows122ManagerTestClient) Initialize(context.Context, string) error { return c.initialize }
func (c *windows122ManagerTestClient) Shutdown(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdowns++
	return nil
}
func (c *windows122ManagerTestClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}
func (c *windows122ManagerTestClient) Notify(context.Context, string, any) error { return nil }
func (c *windows122ManagerTestClient) DidOpen(context.Context, string, string, int, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opened++
	return nil
}
func (c *windows122ManagerTestClient) DidChange(context.Context, string, int, []protocol.TextDocumentContentChangeEvent) error {
	return nil
}
func (c *windows122ManagerTestClient) DidClose(context.Context, string) error { return nil }
func (c *windows122ManagerTestClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	return c.closeError
}

type windows122ManagerTestFactory struct {
	mu      sync.Mutex
	clients []*windows122ManagerTestClient
	calls   int
}

func (f *windows122ManagerTestFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.clients) {
		return nil, errors.New("unexpected Windows manager test factory call")
	}
	client := f.clients[f.calls]
	f.calls++
	return client, nil
}

func (f *windows122ManagerTestFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestWindowsManagerDidOpenWin122UsesOneReplacementAndPublishesOnce(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.c")
	if err := os.WriteFile(target, []byte("int main(void) { return 0; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := &windows122ManagerTestClient{
		initialize: fmt.Errorf("initialize: %w: The data area passed to a system call is too small.", os.ErrClosed),
	}
	replacement := &windows122ManagerTestClient{}
	factory := &windows122ManagerTestFactory{clients: []*windows122ManagerTestClient{first, replacement}}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := ctxWithCWD(root, "windows-122-manager-agent", "windows-122-manager-thread")
	if err := mgr.DidOpen(ctx, fileURIFromPath(target), "c", 1, "int main(void) { return 0; }\n"); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls=%d, want exactly first+replacement", got)
	}
	if first.shutdowns != 0 || first.closes != 1 {
		t.Fatalf("first provisional cleanup shutdown=%d close=%d, want 0/1 for failed initialize", first.shutdowns, first.closes)
	}
	if replacement.opened != 1 {
		t.Fatalf("replacement DidOpen count=%d, want 1", replacement.opened)
	}
	mgr.mu.RLock()
	pending := len(mgr.provisionalCleanups)
	attempts := len(mgr.bootstrapAttempts)
	mgr.mu.RUnlock()
	if pending != 0 {
		t.Fatalf("provisional cleanup entries=%d, want 0", pending)
	}
	// A published workspace may retain its bootstrap owner until the normal lifecycle barrier;
	// the test only requires that no provisional owner remains and DidOpen committed once.
	if attempts > 1 {
		t.Fatalf("bootstrap attempts=%d, want at most one active owner", attempts)
	}
}

func TestWindowsManagerDidOpenWin122CleanupPendingDoesNotCreateReplacement(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.c")
	if err := os.WriteFile(target, []byte("int main(void) { return 0; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := &windows122ManagerTestClient{
		initialize: fmt.Errorf("initialize: %w: The data area passed to a system call is too small.", os.ErrClosed),
		closeError: errors.New("cleanup pending"),
	}
	replacement := &windows122ManagerTestClient{}
	factory := &windows122ManagerTestFactory{clients: []*windows122ManagerTestClient{first, replacement}}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := ctxWithCWD(root, "windows-122-pending-agent", "windows-122-pending-thread")
	err := mgr.DidOpen(ctx, fileURIFromPath(target), "c", 1, "int main(void) { return 0; }\n")
	if err == nil {
		t.Fatal("DidOpen unexpectedly succeeded with pending provisional cleanup")
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("factory calls=%d after pending cleanup, want 1", got)
	}
	if replacement.opened != 0 {
		t.Fatalf("replacement DidOpen=%d, want 0", replacement.opened)
	}
}
