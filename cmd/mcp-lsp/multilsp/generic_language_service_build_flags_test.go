package multilsp

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type genericMatrixClientFactory struct {
	mu      sync.Mutex
	calls   []genericMatrixFactoryCall
	clients []*genericMatrixClient
}

type genericMatrixFactoryCall struct {
	rootDir     string
	env         []string
	initOptions map[string]any
}

func (f *genericMatrixClientFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *genericMatrixClientFactory) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithOptions(rootDir, env, nil, handler)
}

func (f *genericMatrixClientFactory) NewClientWithOptions(rootDir string, env []string, initOptions map[string]any, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &genericMatrixClient{handler: handler, documents: map[string]string{}}
	f.calls = append(f.calls, genericMatrixFactoryCall{rootDir: rootDir, env: append([]string(nil), env...), initOptions: cloneAnyMap(initOptions)})
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *genericMatrixClientFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *genericMatrixClientFactory) callAt(t *testing.T, index int) genericMatrixFactoryCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.calls) {
		t.Fatalf("factory call %d out of range; calls=%d", index, len(f.calls))
	}
	call := f.calls[index]
	call.initOptions = cloneAnyMap(call.initOptions)
	return call
}

func (f *genericMatrixClientFactory) clientAt(t *testing.T, index int) *genericMatrixClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.clients) {
		t.Fatalf("factory client %d out of range; clients=%d", index, len(f.clients))
	}
	return f.clients[index]
}

func TestGoCustomBuildTagsReachFactoryAndSurviveRecycle(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module example.test/recycle-tags\n\ngo 1.25.0\n")
	target := filepath.Join(root, "tagged.go")
	writeGenericTestFile(t, target, "//go:build e2e\n\npackage tagged\n")
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}
	assertFactoryBuildFlags(t, factory.callAt(t, 0), []string{"-tags=e2e"})
	workspace := snapshotWorkspaceClients(mgr)
	resolved, ok := mustBootstrapCoordinator(t, mgr).cache.LastResolvedScope(fileURIFromPath(target))
	if len(workspace) != 1 || !ok {
		t.Fatalf("initial workspace state = %d, resolved=%v", len(workspace), ok)
	}
	ageWorkspaceForLifecycleTest(t, mgr, workspace[0].client)
	if _, err := recycleWorkspaceClient(mgr, resolved.LastResolvedScope, workspace[0]); err != nil {
		t.Fatalf("recycleWorkspaceClient: %v", err)
	}
	assertFactoryBuildFlags(t, factory.callAt(t, 1), []string{"-tags=e2e"})
}

func assertFactoryBuildFlags(t *testing.T, call genericMatrixFactoryCall, want []string) {
	t.Helper()
	if got := call.initOptions["buildFlags"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("factory buildFlags = %#v, want %#v", got, want)
	}
}
